package migration

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// PgRepo menyimpan migration, revisi, dan execution di database platform.
// db dipakai method yang dipanggil worker di luar transaksi pemanggil. Method lain menerima DBTX sendiri.
type PgRepo struct {
	db platformdb.DBTX
}

// NewPgRepo membuat repositori tanpa koneksi tetap. Bind memasang pool untuk panggilan worker.
func NewPgRepo() *PgRepo { return &PgRepo{} }

// Bind mengembalikan repo yang memakai koneksi ini untuk panggilan worker.
func (r *PgRepo) Bind(db platformdb.DBTX) *PgRepo {
	return &PgRepo{db: db}
}

const revisionCols = `
	r.id, r.migration_id, r.revision_no, r.up_sql, r.down_sql, r.irreversible, r.irreversible_reason,
	r.exec_mode, r.batch_table, r.batch_column, r.batch_size, r.sql_hash, r.frozen_at, r.created_by, r.created_at`

const migrationCols = `
	m.id, m.database_id, m.version, m.title, m.current_revision_id, m.created_by, u.username, m.created_at, ` + revisionCols

const migrationFrom = `
	FROM migrations m
	JOIN migration_revisions r ON r.id = m.current_revision_id
	JOIN users u ON u.id = m.created_by`

// Create menyisipkan migration dan revisi pertama, lalu menautkan current_revision_id.
func (r *PgRepo) Create(ctx context.Context, db platformdb.DBTX, databaseID, userID int64, title string, rev Revision) (Migration, error) {
	var id int64
	err := db.QueryRow(ctx, `
		INSERT INTO migrations (database_id, title, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`, databaseID, title, userID).Scan(&id)
	if err != nil {
		return Migration{}, fmt.Errorf("migration.Create: %w", err)
	}
	rev.MigrationID = id
	rev.RevisionNo = 1
	rev.CreatedBy = userID
	revID, err := insertRevision(ctx, db, rev)
	if err != nil {
		return Migration{}, err
	}
	if _, err := db.Exec(ctx, `UPDATE migrations SET current_revision_id = $1 WHERE id = $2`, revID, id); err != nil {
		return Migration{}, fmt.Errorf("migration.Create link: %w", err)
	}
	return r.GetMigration(ctx, db, databaseID, id)
}

func insertRevision(ctx context.Context, db platformdb.DBTX, rev Revision) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `
		INSERT INTO migration_revisions (
			migration_id, revision_no, up_sql, down_sql, irreversible, irreversible_reason,
			exec_mode, batch_table, batch_column, batch_size, sql_hash, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id`,
		rev.MigrationID, rev.RevisionNo, rev.UpSQL, rev.DownSQL, rev.Irreversible, rev.IrreversibleReason,
		rev.ExecMode, rev.BatchTable, rev.BatchColumn, rev.BatchSize, rev.SQLHash, rev.CreatedBy,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("migration.insertRevision: %w", err)
	}
	return id, nil
}

// GetMigration membaca migration beserta revisi yang sedang berlaku.
func (r *PgRepo) GetMigration(ctx context.Context, db platformdb.DBTX, databaseID, id int64) (Migration, error) {
	row := db.QueryRow(ctx, `SELECT `+migrationCols+migrationFrom+` WHERE m.id = $1 AND m.database_id = $2`, id, databaseID)
	m, err := scanMigration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, ErrNotFound
	}
	if err != nil {
		return Migration{}, fmt.Errorf("migration.Get: %w", err)
	}
	return m, nil
}

// List mengembalikan satu halaman migration dan jumlah seluruh baris pada database itu.
func (r *PgRepo) List(ctx context.Context, db platformdb.DBTX, databaseID int64, limit, offset int) ([]Migration, int, error) {
	var total int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM migrations WHERE database_id = $1`, databaseID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("migration.List count: %w", err)
	}
	rows, err := db.Query(ctx, `
		SELECT `+migrationCols+migrationFrom+`
		WHERE m.database_id = $1
		ORDER BY m.version ASC NULLS LAST, m.id
		LIMIT $2 OFFSET $3`, databaseID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("migration.List: %w", err)
	}
	defer rows.Close()
	var list []Migration
	for rows.Next() {
		m, err := scanMigration(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("migration.List: %w", err)
	}
	return list, total, nil
}

// UpdateTitle mengubah judul migration.
func (r *PgRepo) UpdateTitle(ctx context.Context, db platformdb.DBTX, databaseID, id, userID int64, title string) error {
	tag, err := db.Exec(ctx, `
		UPDATE migrations SET title = $1, updated_by = $2, updated_at = now()
		WHERE id = $3 AND database_id = $4`, title, userID, id, databaseID)
	if err != nil {
		return fmt.Errorf("migration.UpdateTitle: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRevision menulis ulang revisi yang frozen_at-nya masih kosong.
func (r *PgRepo) UpdateRevision(ctx context.Context, db platformdb.DBTX, rev Revision) error {
	tag, err := db.Exec(ctx, `
		UPDATE migration_revisions
		SET up_sql = $1, down_sql = $2, irreversible = $3, irreversible_reason = $4,
		    exec_mode = $5, batch_table = $6, batch_column = $7, batch_size = $8, sql_hash = $9
		WHERE id = $10 AND frozen_at IS NULL`,
		rev.UpSQL, rev.DownSQL, rev.Irreversible, rev.IrreversibleReason,
		rev.ExecMode, rev.BatchTable, rev.BatchColumn, rev.BatchSize, rev.SQLHash, rev.ID)
	if err != nil {
		return fmt.Errorf("migration.UpdateRevision: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrImmutable
	}
	return nil
}

// AddRevision menambah revisi baru dan menjadikannya revisi yang berlaku.
func (r *PgRepo) AddRevision(ctx context.Context, db platformdb.DBTX, databaseID, migrationID int64, rev Revision) error {
	revID, err := insertRevision(ctx, db, rev)
	if err != nil {
		return err
	}
	tag, err := db.Exec(ctx, `
		UPDATE migrations SET current_revision_id = $1
		WHERE id = $2 AND database_id = $3`, revID, migrationID, databaseID)
	if err != nil {
		return fmt.Errorf("migration.AddRevision: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDraft melepas tautan revisi lalu menghapus migration yang belum punya execution.
func (r *PgRepo) DeleteDraft(ctx context.Context, db platformdb.DBTX, databaseID, id int64) error {
	if _, err := db.Exec(ctx, `UPDATE migrations SET current_revision_id = NULL WHERE id = $1 AND database_id = $2`, id, databaseID); err != nil {
		return fmt.Errorf("migration.DeleteDraft unlink: %w", err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM migration_revisions WHERE migration_id = $1`, id); err != nil {
		return fmt.Errorf("migration.DeleteDraft revisions: %w", err)
	}
	tag, err := db.Exec(ctx, `DELETE FROM migrations WHERE id = $1 AND database_id = $2`, id, databaseID)
	if err != nil {
		return fmt.Errorf("migration.DeleteDraft: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// revertedPartial bernilai benar untuk execution e yang berhenti partial lalu batch committed-nya
// sudah dibatalkan oleh rollback sukses. Execution seperti itu tidak lagi terbuka dan tidak menahan database.
const revertedPartial = `(e.status = 'partial' AND EXISTS (
	SELECT 1 FROM executions rv WHERE rv.rolls_back_execution_id = e.id AND rv.status = 'success'))`

// LockHead mengunci baris migration dan mengembalikan data untuk aturan submit/forward/rollback.
func (r *PgRepo) LockHead(ctx context.Context, db platformdb.DBTX, databaseID, id int64) (Migration, head, error) {
	m, err := scanMigration(db.QueryRow(ctx, `
		SELECT `+migrationCols+migrationFrom+`
		WHERE m.id = $1 AND m.database_id = $2
		FOR UPDATE OF m`, id, databaseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, head{}, ErrNotFound
	}
	if err != nil {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead: %w", err)
	}
	h := head{
		Version:      m.Version,
		Frozen:       m.Revision.FrozenAt != nil,
		Irreversible: m.Revision.Irreversible,
	}
	var top int32
	if err := db.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM migrations WHERE database_id = $1`, databaseID).Scan(&top); err != nil {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead top: %w", err)
	}
	if top > 0 {
		h.Top = &top
	}
	var partialID *int64
	err = db.QueryRow(ctx, `
		SELECT e.id, e.revision_id, r.irreversible
		FROM executions e
		JOIN migration_revisions r ON r.id = e.revision_id
		WHERE e.migration_id = $1 AND e.kind IN ('apply', 'forward') AND e.status = 'partial'
		  AND NOT `+revertedPartial+`
		ORDER BY e.id DESC
		LIMIT 1`, id).Scan(&partialID, &h.PartialRevisionID, &h.PartialIrreversible)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead partial: %w", err)
	}
	h.PartialID = partialID
	if err := db.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM executions e WHERE e.migration_id = $1 AND e.status = ANY($2) AND NOT `+revertedPartial+`),
			EXISTS (SELECT 1 FROM executions e WHERE e.migration_id = $1 AND e.status = ANY($2) AND NOT `+revertedPartial+`
			        AND e.id <> COALESCE($3, 0))`,
		id, openStatuses(), partialID).Scan(&h.Open, &h.OpenOther); err != nil {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead open: %w", err)
	}
	if err := db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM executions WHERE migration_id = $1)`, id).Scan(&h.HasExecutions); err != nil {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead executions: %w", err)
	}
	if err := db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM executions WHERE migration_id = $1 AND status = 'failed')`, id).Scan(&h.HasFailed); err != nil {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead failed: %w", err)
	}
	if err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM executions
			WHERE migration_id = $1 AND kind = 'rollback' AND status = 'success')`, id).Scan(&h.HasRolledBack); err != nil {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead rolled: %w", err)
	}
	if err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM executions e
			WHERE e.migration_id = $1 AND e.kind IN ('apply', 'forward') AND e.status = 'success'
			  AND NOT EXISTS (
				SELECT 1 FROM executions r
				WHERE r.rolls_back_execution_id = e.id AND r.status = 'success'))`, id).Scan(&h.ActiveSuccess); err != nil {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead active: %w", err)
	}
	var executedIrreversible bool
	err = db.QueryRow(ctx, `
		SELECT r.irreversible
		FROM executions e
		JOIN migration_revisions r ON r.id = e.revision_id
		WHERE e.migration_id = $1 AND e.kind IN ('apply', 'forward') AND e.status = 'success'
		  AND NOT EXISTS (
			SELECT 1 FROM executions rb
			WHERE rb.rolls_back_execution_id = e.id AND rb.status = 'success')
		ORDER BY e.id DESC
		LIMIT 1`, id).Scan(&executedIrreversible)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, head{}, fmt.Errorf("migration.LockHead irreversible: %w", err)
	}
	if err == nil {
		h.Irreversible = executedIrreversible
	}
	return m, h, nil
}

// Freeze menandai revisi tidak boleh diubah lagi. Dipanggil saat submit pertama.
func (r *PgRepo) Freeze(ctx context.Context, db platformdb.DBTX, revisionID int64) error {
	_, err := db.Exec(ctx, `UPDATE migration_revisions SET frozen_at = now() WHERE id = $1 AND frozen_at IS NULL`, revisionID)
	if err != nil {
		return fmt.Errorf("migration.Freeze: %w", err)
	}
	return nil
}

// InsertExecution menyimpan satu permintaan apply, forward, atau rollback.
func (r *PgRepo) InsertExecution(ctx context.Context, db platformdb.DBTX, e Execution) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `
		INSERT INTO executions (
			database_id, migration_id, revision_id, kind, status, requested_by,
			rolls_back_execution_id, approval_round)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id`,
		e.DatabaseID, e.MigrationID, e.RevisionID, e.Kind, e.Status, e.RequestedBy,
		e.RollsBackExecutionID, e.ApprovalRound,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("migration.InsertExecution: %w", err)
	}
	return id, nil
}

func (r *PgRepo) ActiveSuccessID(ctx context.Context, db platformdb.DBTX, migrationID int64) (int64, int64, error) {
	var id, revisionID int64
	err := db.QueryRow(ctx, `
		SELECT e.id, e.revision_id FROM executions e
		WHERE e.migration_id = $1 AND e.kind IN ('apply', 'forward') AND e.status = 'success'
		  AND NOT EXISTS (
			SELECT 1 FROM executions r
			WHERE r.rolls_back_execution_id = e.id AND r.status = 'success')
		ORDER BY e.id DESC
		LIMIT 1`, migrationID).Scan(&id, &revisionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrNotActive
	}
	if err != nil {
		return 0, 0, fmt.Errorf("migration.ActiveSuccessID: %w", err)
	}
	return id, revisionID, nil
}

// ListExecutions membaca riwayat. hide menyaring pasangan rollback sukses lewat rolls_back_execution_id.
func (r *PgRepo) ListExecutions(ctx context.Context, db platformdb.DBTX, databaseID int64, hide bool, limit, offset int) ([]Execution, int, error) {
	filter := `
		WHERE e.database_id = $1
		  AND ($2 = false OR NOT (
			EXISTS (SELECT 1 FROM executions r WHERE r.rolls_back_execution_id = e.id AND r.status = 'success')
			OR (e.kind = 'rollback' AND e.status = 'success')))`
	var total int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM executions e `+filter, databaseID, hide).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("migration.ListExecutions count: %w", err)
	}
	rows, err := db.Query(ctx, `
		SELECT `+executionCols+`
		FROM executions e
		JOIN migrations m ON m.id = e.migration_id
		JOIN users u ON u.id = e.requested_by
		`+filter+`
		ORDER BY e.id
		LIMIT $3 OFFSET $4`, databaseID, hide, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("migration.ListExecutions: %w", err)
	}
	defer rows.Close()
	list, err := scanExecutions(rows)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// GetExecution membaca satu execution beserta judul migration-nya.
func (r *PgRepo) GetExecution(ctx context.Context, db platformdb.DBTX, id int64) (Execution, error) {
	row := db.QueryRow(ctx, `
		SELECT `+executionCols+`
		FROM executions e
		JOIN migrations m ON m.id = e.migration_id
		JOIN users u ON u.id = e.requested_by
		WHERE e.id = $1`, id)
	e, err := scanExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrExecNotFound
	}
	if err != nil {
		return Execution{}, fmt.Errorf("migration.GetExecution: %w", err)
	}
	return e, nil
}

// ListAttempts mengembalikan percobaan teknis satu execution, terurut nomor attempt.
func (r *PgRepo) ListAttempts(ctx context.Context, db platformdb.DBTX, executionID int64) ([]Attempt, error) {
	rows, err := db.Query(ctx, `
		SELECT id, execution_id, attempt_no, worker_id, status, sql_executed, sqlstate, error_message,
		       started_at, finished_at, trace_id
		FROM execution_attempts
		WHERE execution_id = $1
		ORDER BY attempt_no`, executionID)
	if err != nil {
		return nil, fmt.Errorf("migration.ListAttempts: %w", err)
	}
	defer rows.Close()
	var list []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.ExecutionID, &a.AttemptNo, &a.WorkerID, &a.Status, &a.SQLExecuted,
			&a.SQLState, &a.ErrorMessage, &a.StartedAt, &a.FinishedAt, &a.TraceID); err != nil {
			return nil, fmt.Errorf("migration.ListAttempts: %w", err)
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migration.ListAttempts: %w", err)
	}
	return list, nil
}

// Get, SetStatus, dan SetRound memenuhi approval.Executions.
func (r *PgRepo) Get(ctx context.Context, db platformdb.DBTX, execID int64) (approval.ExecInfo, error) {
	var info approval.ExecInfo
	err := db.QueryRow(ctx, `SELECT database_id, approval_round, status FROM executions WHERE id = $1`, execID).
		Scan(&info.DatabaseID, &info.Round, &info.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return approval.ExecInfo{}, ErrExecNotFound
	}
	if err != nil {
		return approval.ExecInfo{}, fmt.Errorf("migration.ExecGet: %w", err)
	}
	return info, nil
}

// SetStatus mengubah status execution. Dipakai approval saat approve, reject, dan revoke.
func (r *PgRepo) SetStatus(ctx context.Context, db platformdb.DBTX, execID int64, status string) error {
	tag, err := db.Exec(ctx, `UPDATE executions SET status = $1 WHERE id = $2`, status, execID)
	if err != nil {
		return fmt.Errorf("migration.SetStatus: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrExecNotFound
	}
	return nil
}

// SetRound menyimpan nomor ronde approval yang sedang berlaku.
func (r *PgRepo) SetRound(ctx context.Context, db platformdb.DBTX, execID int64, round int) error {
	tag, err := db.Exec(ctx, `UPDATE executions SET approval_round = $1 WHERE id = $2`, round, execID)
	if err != nil {
		return fmt.Errorf("migration.SetRound: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrExecNotFound
	}
	return nil
}

const executionCols = `
	e.id, e.database_id, e.migration_id, m.title, e.revision_id, e.kind, e.status,
	e.requested_by, u.username, e.rolls_back_execution_id, e.approval_round, e.version_applied,
	e.batches_committed, e.rows_committed, e.created_at, e.started_at, e.finished_at, e.error_code, e.error_message`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMigration(row rowScanner) (Migration, error) {
	var m Migration
	err := row.Scan(
		&m.ID, &m.DatabaseID, &m.Version, &m.Title, &m.CurrentRevisionID, &m.CreatedBy, &m.CreatedByUsername, &m.CreatedAt,
		&m.Revision.ID, &m.Revision.MigrationID, &m.Revision.RevisionNo, &m.Revision.UpSQL, &m.Revision.DownSQL,
		&m.Revision.Irreversible, &m.Revision.IrreversibleReason, &m.Revision.ExecMode, &m.Revision.BatchTable,
		&m.Revision.BatchColumn, &m.Revision.BatchSize, &m.Revision.SQLHash, &m.Revision.FrozenAt,
		&m.Revision.CreatedBy, &m.Revision.CreatedAt,
	)
	return m, err
}

func scanExecution(row rowScanner) (Execution, error) {
	var e Execution
	err := row.Scan(
		&e.ID, &e.DatabaseID, &e.MigrationID, &e.MigrationTitle, &e.RevisionID, &e.Kind, &e.Status,
		&e.RequestedBy, &e.RequestedByUsername, &e.RollsBackExecutionID, &e.ApprovalRound, &e.VersionApplied,
		&e.BatchesCommitted, &e.RowsCommitted, &e.CreatedAt, &e.StartedAt, &e.FinishedAt, &e.ErrorCode, &e.ErrorMessage,
	)
	return e, err
}

func scanExecutions(rows pgx.Rows) ([]Execution, error) {
	var list []Execution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("migration.scanExecution: %w", err)
		}
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migration.scanExecution: %w", err)
	}
	return list, nil
}
