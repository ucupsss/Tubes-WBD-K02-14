package batch

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Runner mengeksekusi dan membatalkan (revert) perubahan database dalam mode per batch (D36, D39).
type Runner interface {
	// Apply menjalankan (atau melanjutkan dari checkpoint terakhir) execution batch di database target.
	// Mengembalikan Result dengan Status: StatusDone, StatusPartial, atau StatusFailed.
	Apply(ctx context.Context, target *pgxpool.Pool, spec Spec, progress ProgressSink) (Result, error)
	// Revert menjalankan SQL down per batch yang sudah committed dalam urutan terbalik.
	// Mengembalikan Result dengan Status: StatusDone, StatusPartiallyReverted, atau StatusFailed.
	Revert(ctx context.Context, target *pgxpool.Pool, spec Spec, progress ProgressSink) (Result, error)
}

// DefaultRunner adalah implementasi standar Runner.
type DefaultRunner struct{}

// NewRunner membuat DefaultRunner baru.
func NewRunner() *DefaultRunner {
	return &DefaultRunner{}
}

// Apply mengeksekusi UpSQL secara bertahap per batch.
// Setiap batch memiliki transaction sendiri di target DB yang juga menulis bukti ke _dbmp.batch_checkpoints.
// Jika dipanggil lagi setelah kegagalan sebagian (resume), Apply membaca checkpoint terakhir dari
// _dbmp.batch_checkpoints dan melanjutkan dari cursor terakhir tanpa mengulang batch yang sudah committed.
func (r *DefaultRunner) Apply(ctx context.Context, target *pgxpool.Pool, spec Spec, progress ProgressSink) (Result, error) {
	if err := ValidateSpec(spec, DirectionApply); err != nil {
		return Result{Status: StatusFailed}, err
	}
	// Validasi aturan SQL berbahaya menggunakan contoh ekspansi dummy (0, 1).
	sampleSQL, err := ExpandBounds(spec.UpSQL, 0, 1)
	if err != nil {
		return Result{Status: StatusFailed}, err
	}
	if err := migration.ValidateSQL(sampleSQL); err != nil {
		return Result{Status: StatusFailed}, err
	}

	if err := EnsureTargetSchema(ctx, target); err != nil {
		return Result{Status: StatusFailed}, err
	}

	schemaName, tableName, colName, err := validateAndResolveTableColumn(ctx, target, spec.Table, spec.Column)
	if err != nil {
		return Result{Status: StatusFailed}, err
	}

	existing, err := ListTargetCheckpoints(ctx, target, spec.ExecutionID, DirectionApply)
	if err != nil {
		return Result{Status: StatusFailed}, err
	}

	var (
		res    Result
		cursor int64 = math.MinInt64
	)
	for _, cp := range existing {
		res.BatchesCommitted++
		res.RowsCommitted += cp.RowsAffected
		res.LastBatchNo = cp.BatchNo
		res.LastFrom = cp.FromValue
		res.LastTo = cp.ToValue
		cursor = cp.ToValue
	}

	// Jika belum ada batch sama sekali, tentukan batas bawah awal (min(col) - 1) agar batch pertama
	// memiliki rentang (:from, :to] yang bersih dan ramah dibaca (mis. 0..1000 untuk id mulai dari 1).
	if len(existing) == 0 {
		minVal, hasRows, err := findMinColumnValue(ctx, target, schemaName, tableName, colName)
		if err != nil {
			return Result{Status: StatusFailed}, err
		}
		if !hasRows {
			res.Status = StatusDone
			return res, nil
		}
		if minVal > 0 {
			cursor = 0
		} else if minVal > math.MinInt64 {
			cursor = minVal - 1
		}
	}

	quotedTable := pgx.Identifier{schemaName, tableName}.Sanitize()
	quotedCol := pgx.Identifier{colName}.Sanitize()
	boundQuery := fmt.Sprintf(
		"SELECT max(%s) FROM (SELECT %s FROM %s WHERE %s > $1 ORDER BY %s ASC LIMIT $2) AS _dbmp_sub",
		quotedCol, quotedCol, quotedTable, quotedCol, quotedCol,
	)

	for {
		if err := ctx.Err(); err != nil {
			res.Status = partialOrFailed(res.BatchesCommitted, DirectionApply)
			return res, err
		}

		nextBatchNo := res.LastBatchNo + 1
		done, batchFrom, batchTo, batchRows, execSQL, err := r.runApplyBatch(
			ctx, target, spec, boundQuery, cursor, nextBatchNo,
		)
		if execSQL != "" {
			res.LastExecutedSQL = execSQL
		}
		if err != nil {
			res.Status = partialOrFailed(res.BatchesCommitted, DirectionApply)
			return res, err
		}
		if done {
			res.Status = StatusDone
			return res, nil
		}

		res.BatchesCommitted++
		res.RowsCommitted += batchRows
		res.LastBatchNo = nextBatchNo
		res.LastFrom = batchFrom
		res.LastTo = batchTo
		cursor = batchTo

		if progress != nil {
			if err := progress.BatchCommitted(ctx, DirectionApply, nextBatchNo, batchFrom, batchTo, batchRows); err != nil {
				slog.WarnContext(ctx, "batch.progress_sink_error",
					"execution_id", spec.ExecutionID, "batch_no", nextBatchNo, "err", err)
			}
		}
	}
}

func (r *DefaultRunner) runApplyBatch(
	ctx context.Context,
	target *pgxpool.Pool,
	spec Spec,
	boundQuery string,
	cursor int64,
	batchNo int,
) (done bool, from, to, rowsAffected int64, executedSQL string, err error) {
	ctx = telemetry.WithSQLMeta(ctx, telemetry.SQLMeta{
		MigrationID: spec.MigrationID,
		ExecutionID: spec.ExecutionID,
		JobID:       spec.JobID,
		Attempt:     spec.Attempt,
		BatchNo:     batchNo,
	})
	ctx, span := telemetry.Tracer().Start(ctx, "target.batch")
	span.SetAttributes(
		attribute.Int64("execution_id", spec.ExecutionID),
		attribute.String("batch.direction", DirectionApply),
		attribute.Int("batch.no", batchNo),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	tx, err := target.Begin(ctx)
	if err != nil {
		return false, 0, 0, 0, "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := applyTimeouts(ctx, tx, spec); err != nil {
		return false, 0, 0, 0, "", err
	}

	var maxTo *int64
	if err := tx.QueryRow(ctx, boundQuery, cursor, spec.Size).Scan(&maxTo); err != nil {
		return false, 0, 0, 0, "", err
	}
	if maxTo == nil {
		if err := tx.Commit(ctx); err != nil {
			return false, 0, 0, 0, "", err
		}
		return true, 0, 0, 0, "", nil
	}

	from = cursor
	to = *maxTo
	span.SetAttributes(
		attribute.Int64("batch.from", from),
		attribute.Int64("batch.to", to),
	)

	executedSQL, err = ExpandBounds(spec.UpSQL, from, to)
	if err != nil {
		return false, from, to, 0, "", err
	}

	rowsAffected, err = execBatchStatements(ctx, tx, executedSQL)
	if err != nil {
		slog.WarnContext(ctx, "rollback",
			"execution_id", spec.ExecutionID,
			"batch_no", batchNo,
			"direction", DirectionApply,
			"from", from,
			"to", to,
			"status", "error",
			"err", err,
		)
		return false, from, to, 0, executedSQL, err
	}

	cp := Checkpoint{
		ExecutionID:  spec.ExecutionID,
		Direction:    DirectionApply,
		BatchNo:      batchNo,
		FromValue:    from,
		ToValue:      to,
		RowsAffected: rowsAffected,
		CommittedAt:  time.Now().UTC(),
	}
	if err := insertTargetCheckpoint(ctx, tx, cp); err != nil {
		return false, from, to, 0, executedSQL, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, from, to, 0, executedSQL, err
	}

	span.SetAttributes(attribute.Int64("batch.rows_affected", rowsAffected))
	slog.InfoContext(ctx, "batch.committed",
		"execution_id", spec.ExecutionID,
		"direction", DirectionApply,
		"batch_no", batchNo,
		"from", from,
		"to", to,
		"rows", rowsAffected,
		"status", "ok",
	)
	slog.InfoContext(ctx, "commit",
		"execution_id", spec.ExecutionID,
		"batch_no", batchNo,
		"direction", DirectionApply,
		"status", "ok",
	)
	return false, from, to, rowsAffected, executedSQL, nil
}

// Revert menjalankan DownSQL untuk setiap batch apply yang sudah committed dalam urutan terbalik
// (dari batch_no terbesar ke terkecil). Jika sebelumnya pernah gagal di tengah (partially_reverted)
// lalu di-resume, batch yang sudah punya checkpoint direction='revert' akan dilewati.
func (r *DefaultRunner) Revert(ctx context.Context, target *pgxpool.Pool, spec Spec, progress ProgressSink) (Result, error) {
	if err := ValidateSpec(spec, DirectionRevert); err != nil {
		return Result{Status: StatusFailed}, err
	}
	sampleSQL, err := ExpandBounds(spec.DownSQL, 0, 1)
	if err != nil {
		return Result{Status: StatusFailed}, err
	}
	if err := migration.ValidateSQL(sampleSQL); err != nil {
		return Result{Status: StatusFailed}, err
	}

	if err := EnsureTargetSchema(ctx, target); err != nil {
		return Result{Status: StatusFailed}, err
	}

	applied, err := ListTargetCheckpoints(ctx, target, spec.ExecutionID, DirectionApply)
	if err != nil {
		return Result{Status: StatusFailed}, err
	}
	if len(applied) == 0 {
		return Result{Status: StatusDone}, nil
	}

	revertedList, err := ListTargetCheckpoints(ctx, target, spec.ExecutionID, DirectionRevert)
	if err != nil {
		return Result{Status: StatusFailed}, err
	}

	revertedSet := make(map[int]Checkpoint, len(revertedList))
	var res Result
	for _, cp := range revertedList {
		revertedSet[cp.BatchNo] = cp
		res.BatchesCommitted++
		res.RowsCommitted += cp.RowsAffected
		res.LastBatchNo = cp.BatchNo
		res.LastFrom = cp.FromValue
		res.LastTo = cp.ToValue
	}

	// Iterasi dari batch terbesar ke terkecil (reverse order).
	for i := len(applied) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			res.Status = partialOrFailed(res.BatchesCommitted, DirectionRevert)
			return res, err
		}

		ap := applied[i]
		if _, already := revertedSet[ap.BatchNo]; already {
			continue
		}

		rows, execSQL, err := r.runRevertBatch(ctx, target, spec, ap.BatchNo, ap.FromValue, ap.ToValue)
		if execSQL != "" {
			res.LastExecutedSQL = execSQL
		}
		if err != nil {
			res.Status = partialOrFailed(res.BatchesCommitted, DirectionRevert)
			return res, err
		}

		res.BatchesCommitted++
		res.RowsCommitted += rows
		res.LastBatchNo = ap.BatchNo
		res.LastFrom = ap.FromValue
		res.LastTo = ap.ToValue

		if progress != nil {
			if err := progress.BatchCommitted(ctx, DirectionRevert, ap.BatchNo, ap.FromValue, ap.ToValue, rows); err != nil {
				slog.WarnContext(ctx, "batch.progress_sink_error",
					"execution_id", spec.ExecutionID, "batch_no", ap.BatchNo, "err", err)
			}
		}
	}

	res.Status = StatusDone
	return res, nil
}

func (r *DefaultRunner) runRevertBatch(
	ctx context.Context,
	target *pgxpool.Pool,
	spec Spec,
	batchNo int,
	from, to int64,
) (rowsAffected int64, executedSQL string, err error) {
	ctx = telemetry.WithSQLMeta(ctx, telemetry.SQLMeta{
		MigrationID: spec.MigrationID,
		ExecutionID: spec.ExecutionID,
		JobID:       spec.JobID,
		Attempt:     spec.Attempt,
		BatchNo:     batchNo,
	})
	ctx, span := telemetry.Tracer().Start(ctx, "target.batch")
	span.SetAttributes(
		attribute.Int64("execution_id", spec.ExecutionID),
		attribute.String("batch.direction", DirectionRevert),
		attribute.Int("batch.no", batchNo),
		attribute.Int64("batch.from", from),
		attribute.Int64("batch.to", to),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	executedSQL, err = ExpandBounds(spec.DownSQL, from, to)
	if err != nil {
		return 0, "", err
	}

	tx, err := target.Begin(ctx)
	if err != nil {
		return 0, executedSQL, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := applyTimeouts(ctx, tx, spec); err != nil {
		return 0, executedSQL, err
	}

	rowsAffected, err = execBatchStatements(ctx, tx, executedSQL)
	if err != nil {
		slog.WarnContext(ctx, "rollback",
			"execution_id", spec.ExecutionID,
			"batch_no", batchNo,
			"direction", DirectionRevert,
			"from", from,
			"to", to,
			"status", "error",
			"err", err,
		)
		return 0, executedSQL, err
	}

	cp := Checkpoint{
		ExecutionID:  spec.ExecutionID,
		Direction:    DirectionRevert,
		BatchNo:      batchNo,
		FromValue:    from,
		ToValue:      to,
		RowsAffected: rowsAffected,
		CommittedAt:  time.Now().UTC(),
	}
	if err := insertTargetCheckpoint(ctx, tx, cp); err != nil {
		return 0, executedSQL, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, executedSQL, err
	}

	span.SetAttributes(attribute.Int64("batch.rows_affected", rowsAffected))
	slog.InfoContext(ctx, "batch.committed",
		"execution_id", spec.ExecutionID,
		"direction", DirectionRevert,
		"batch_no", batchNo,
		"from", from,
		"to", to,
		"rows", rowsAffected,
		"status", "ok",
	)
	slog.InfoContext(ctx, "commit",
		"execution_id", spec.ExecutionID,
		"batch_no", batchNo,
		"direction", DirectionRevert,
		"status", "ok",
	)
	return rowsAffected, executedSQL, nil
}

func partialOrFailed(committed int, direction string) Status {
	if committed <= 0 {
		return StatusFailed
	}
	if direction == DirectionRevert {
		return StatusPartiallyReverted
	}
	return StatusPartial
}

func applyTimeouts(ctx context.Context, tx pgx.Tx, spec Spec) error {
	stmtTimeout := spec.StatementTimeout
	if stmtTimeout == "" {
		stmtTimeout = "30s"
	}
	lockTimeout := spec.LockTimeout
	if lockTimeout == "" {
		lockTimeout = "5s"
	}
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = "+quoteLiteral(stmtTimeout)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = "+quoteLiteral(lockTimeout)); err != nil {
		return err
	}
	return nil
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// execBatchStatements memecah teks SQL batch menjadi statement individual dan mengeksekusinya
// melalui tx.Exec (dengan QueryExecModeSimpleProtocol) supaya PgxTracer mencatat setiap SQL
// sekaligus menghitung total rows_affected.
func execBatchStatements(ctx context.Context, tx pgx.Tx, sqlText string) (int64, error) {
	stmts, err := migration.SplitStatements(sqlText)
	if err != nil {
		return 0, err
	}
	var totalRows int64
	for _, st := range stmts {
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, st.SQL, pgx.QueryExecModeSimpleProtocol)
		if err != nil {
			return totalRows, err
		}
		totalRows += tag.RowsAffected()
	}
	return totalRows, nil
}

// validateAndResolveTableColumn memverifikasi bahwa tabel dan kolom batch benar-benar ada di katalog
// database target dan kolom tersebut bertipe integer (smallint, integer, bigint) sesuai D39 & Jebakan #10.
func validateAndResolveTableColumn(ctx context.Context, target *pgxpool.Pool, rawTable, rawCol string) (schemaName, tableName, colName string, err error) {
	schemaName = "public"
	tableName = strings.TrimSpace(rawTable)
	if parts := strings.SplitN(tableName, ".", 2); len(parts) == 2 {
		schemaName = strings.TrimSpace(parts[0])
		tableName = strings.TrimSpace(parts[1])
	}
	colName = strings.TrimSpace(rawCol)
	if schemaName == "" || tableName == "" || colName == "" {
		return "", "", "", ErrInvalidBatchTable
	}

	var dataType string
	err = target.QueryRow(ctx, `
		SELECT data_type
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`,
		schemaName, tableName, colName,
	).Scan(&dataType)
	if err != nil {
		return "", "", "", ErrInvalidBatchTable
	}

	switch strings.ToLower(dataType) {
	case "smallint", "integer", "bigint":
		return schemaName, tableName, colName, nil
	default:
		return "", "", "", ErrInvalidBatchTable
	}
}

func findMinColumnValue(ctx context.Context, target *pgxpool.Pool, schemaName, tableName, colName string) (int64, bool, error) {
	quotedTable := pgx.Identifier{schemaName, tableName}.Sanitize()
	quotedCol := pgx.Identifier{colName}.Sanitize()
	q := fmt.Sprintf("SELECT min(%s) FROM %s", quotedCol, quotedTable)

	var minVal *int64
	if err := target.QueryRow(ctx, q).Scan(&minVal); err != nil {
		return 0, false, err
	}
	if minVal == nil {
		return 0, false, nil
	}
	return *minVal, true, nil
}
