package migration

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/batch"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/lock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/metrics"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/telemetry"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Checker adalah bagian approval yang dipakai saat revalidasi.
type Checker interface {
	Verify(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) (complete bool, err error)
	Revoke(ctx context.Context, q platformdb.DBTX, execID, dbID int64) (int, error)
}

// WrapApproval menyesuaikan approval.Service ke Checker.
func WrapApproval(s *approval.Service) Checker {
	return approvalChecker{s}
}

type approvalChecker struct{ s *approval.Service }

func (a approvalChecker) Verify(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) (bool, error) {
	v, err := a.s.Verify(ctx, q, execID, dbID, round)
	if err != nil {
		return false, err
	}
	return v.Complete, nil
}

func (a approvalChecker) Revoke(ctx context.Context, q platformdb.DBTX, execID, dbID int64) (int, error) {
	return a.s.Revoke(ctx, q, execID, dbID)
}

// Executor menjalankan SQL normal maupun mode batch di database target setelah kunci dan revalidasi.
type Executor struct {
	repo        *PgRepo
	pool        *pgxpool.Pool
	conns       dbconn.Manager
	locks       *lock.PgLocker
	check       Checker
	authz       authz.Authorizer
	batchRunner batch.Runner
	proof       func(ctx context.Context, pool *pgxpool.Pool, execID int64) (bool, error)
}

// NewExecutor merakit pelaksana SQL target. check dan az boleh nil di tes yang tidak memeriksa approval atau akses.
func NewExecutor(pool *pgxpool.Pool, repo *PgRepo, conns dbconn.Manager, locks *lock.PgLocker, check Checker, az authz.Authorizer) *Executor {
	if repo != nil && repo.db == nil {
		repo = repo.Bind(pool)
	}
	return &Executor{
		repo:        repo,
		pool:        pool,
		conns:       conns,
		locks:       locks,
		check:       check,
		authz:       az,
		batchRunner: batch.NewRunner(),
		proof:       proofExists,
	}
}

// SetBatchRunner mengganti batch.Runner (mis. dengan mock atau nil untuk menguji kegagalan tertutup).
func (e *Executor) SetBatchRunner(r batch.Runner) {
	e.batchRunner = r
}

// SetProof mengganti pemeriksaan bukti. Dipakai tes untuk hasil commit yang tidak pasti.
func (e *Executor) SetProof(fn func(ctx context.Context, pool *pgxpool.Pool, execID int64) (bool, error)) {
	e.proof = fn
}

// Run memenuhi worker.Runner.
func (e *Executor) Run(ctx context.Context, job queue.Job) error {
	w, err := e.repo.LoadWork(ctx, job.ExecutionID)
	if err != nil {
		return err
	}
	if w.ExecMode == ModeBatch && e.batchRunner == nil {
		return e.fail(ctx, w, job, "batch_unavailable", "Mode batch belum tersedia", "")
	}
	if w.Status == StatusUncertain || w.Status == StatusRunning {
		done, err := e.recover(ctx, &w, job)
		if err != nil || done {
			return err
		}
	}
	lockCtx, lockSpan := telemetry.Tracer().Start(ctx, "lock.acquire")
	lockStart := time.Now()
	lease, err := e.locks.Acquire(lockCtx, w.DatabaseID, w.ExecutionID, job.ClaimedBy, lock.DefaultLease)
	telemetry.End(lockSpan, err)
	observeLock(lockStart, err)
	if errors.Is(err, lock.ErrBusy) {
		return &worker.Halt{Disposition: worker.DispositionRetry, Message: lock.ErrBusy.Msg, Recorded: true, Reason: "lock_busy"}
	}
	if err != nil {
		return err
	}
	metrics.LockWait.Observe(metrics.Since(job.EnqueuedAt))
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	beat, stopBeat := context.WithCancel(runCtx)
	defer stopBeat()
	go heartbeat(beat, lease, func(ctx context.Context) error {
		return queue.Touch(ctx, e.pool, job.ID, job.ClaimedBy)
	}, cancelRun)

	release := true
	defer func() {
		if release {
			_ = lease.Release(context.Background())
		}
	}()

	revalCtx, revalSpan := telemetry.Tracer().Start(runCtx, "revalidate")
	err = e.revalidate(revalCtx, w)
	telemetry.End(revalSpan, err)
	if err != nil {
		var halt *worker.Halt
		if errors.As(err, &halt) {
			return e.record(ctx, w, job, halt)
		}
		return err
	}

	target, err := e.conns.Pool(runCtx, w.DatabaseID)
	if err != nil {
		if w.BatchesCommitted > 0 {
			return e.restorePartial(ctx, w, job, "target_unavailable", "Database tidak dapat dihubungi", "", w.BatchesCommitted, w.RowsCommitted)
		}
		return e.fail(ctx, w, job, "target_unavailable", "Database tidak dapat dihubungi", "")
	}
	found, err := e.proof(runCtx, target, w.ExecutionID)
	if err != nil {
		release = false
		return e.uncertain(ctx, w, job, "Hasil commit belum dapat dipastikan")
	}
	if found {
		return e.succeed(ctx, w, job, "")
	}

	if w.ExecMode == ModeBatch {
		return e.runBatch(runCtx, target, w, job, &release)
	}

	sqlText, err := script(w)
	if err != nil {
		return e.fail(ctx, w, job, "not_active", err.Error(), "")
	}
	version := int32(0)
	if w.Kind == KindRollback {
		if w.Version == nil {
			return e.fail(ctx, w, job, "not_active", "Migration ini tidak sedang aktif", sqlText)
		}
		version = *w.Version
	} else {
		version, err = e.repo.NextChainVersion(runCtx, w.DatabaseID)
		if err != nil {
			return err
		}
	}

	logx.Audit(ctx, "start", slog.Int64("execution_id", w.ExecutionID), slog.Int64("database", w.DatabaseID), slog.String("kind", w.Kind))
	start := time.Now()
	execCtx, execSpan := telemetry.Tracer().Start(runCtx, "target.exec", trace.WithAttributes(
		attribute.Int64("execution_id", w.ExecutionID), attribute.String("kind", w.Kind), attribute.Int("attempt", job.Attempts)))
	runErr := applyTarget(execCtx, target, w, job, version, sqlText)
	telemetry.End(execSpan, runErr)
	observeMigration(w.Kind, ModeNormal, start, runErr)
	logTxOutcome(ctx, w, job, runErr)
	logx.SQL(ctx, logx.SQLEvent{
		Query: sqlText, Duration: time.Since(start), Rows: -1, Err: runErr,
		MigrationID: w.MigrationID, ExecutionID: w.ExecutionID, JobID: job.ID, Attempt: job.Attempts,
	})
	if runErr != nil {
		if uncertainErr(runErr) {
			found, probeErr := e.proof(ctx, target, w.ExecutionID)
			if probeErr == nil && found {
				return e.succeed(ctx, w, job, sqlText)
			}
			release = false
			return e.uncertain(ctx, w, job, "Hasil commit belum dapat dipastikan")
		}
		var pgErr *pgconn.PgError
		state, msg := "", "Migration gagal dijalankan"
		if errors.As(runErr, &pgErr) {
			state, msg = pgErr.Code, pgErr.Message
		}
		if state != "" && queue.Retryable(state) {
			if err := e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", state, msg, sqlText); err != nil {
				return err
			}
			return &queue.SQLError{SQLState: state, Message: msg, Recorded: true}
		}
		return e.fail(ctx, w, job, state, msg, sqlText)
	}
	return e.succeed(ctx, w, job, sqlText)
}

func (e *Executor) runBatch(ctx context.Context, target *pgxpool.Pool, w Work, job queue.Job, release *bool) error {
	table, column := "", ""
	size := 0
	if w.BatchTable != nil {
		table = *w.BatchTable
	}
	if w.BatchColumn != nil {
		column = *w.BatchColumn
	}
	if w.BatchSize != nil {
		size = *w.BatchSize
	}
	down := ""
	if w.DownSQL != nil {
		down = *w.DownSQL
	}

	sourceExecID := w.ExecutionID
	if w.Kind == KindRollback && w.RollsBackExecutionID != nil {
		sourceExecID = *w.RollsBackExecutionID
	}

	spec := batch.Spec{
		ExecutionID:       w.ExecutionID,
		SourceExecutionID: sourceExecID,
		MigrationID:       w.MigrationID,
		JobID:             job.ID,
		Attempt:           job.Attempts,
		Table:             table,
		Column:            column,
		Size:              size,
		UpSQL:             w.UpSQL,
		DownSQL:           down,
	}

	mirror := batch.NewMirrorRepo(e.pool)
	totalBatches := w.BatchesCommitted
	totalRows := w.RowsCommitted
	sink := batch.ProgressFunc(func(ctx context.Context, dir string, batchNo int, from, to, rows int64) error {
		totalBatches++
		totalRows += rows
		metrics.BatchesCommitted.WithLabelValues(dir).Inc()
		metrics.BatchRows.WithLabelValues(dir).Add(float64(rows))
		_ = mirror.RecordBatch(ctx, batch.Checkpoint{
			ExecutionID:  w.ExecutionID,
			Direction:    dir,
			BatchNo:      batchNo,
			FromValue:    from,
			ToValue:      to,
			RowsAffected: rows,
			CommittedAt:  time.Now().UTC(),
		})
		return e.repo.UpdateBatchProgress(ctx, w.ExecutionID, totalBatches, totalRows)
	})

	version := int32(0)
	if w.RevertsPartial() {
		// Tidak ada posisi rantai yang dilepas: execution partial belum pernah menjadi aktif.
	} else if w.Kind == KindRollback {
		if w.Version == nil {
			return e.fail(ctx, w, job, "not_active", "Migration ini tidak sedang aktif", down)
		}
		version = *w.Version
	} else {
		var err error
		version, err = e.repo.NextChainVersion(ctx, w.DatabaseID)
		if err != nil {
			return err
		}
	}

	logx.Audit(ctx, "start", slog.Int64("execution_id", w.ExecutionID), slog.Int64("database", w.DatabaseID), slog.String("kind", w.Kind), slog.String("exec_mode", ModeBatch))

	var (
		res    batch.Result
		runErr error
	)
	batchStart := time.Now()
	if w.Kind == KindRollback {
		res, runErr = e.batchRunner.Revert(ctx, target, spec, sink)
	} else {
		res, runErr = e.batchRunner.Apply(ctx, target, spec, sink)
	}
	observeMigration(w.Kind, ModeBatch, batchStart, runErr)

	execSQL := res.LastExecutedSQL
	if execSQL == "" {
		if w.Kind == KindRollback {
			execSQL = down
		} else {
			execSQL = w.UpSQL
		}
	}

	if runErr != nil {
		if uncertainErr(runErr) {
			*release = false
			return e.uncertain(ctx, w, job, "Hasil commit batch belum dapat dipastikan")
		}
		var pgErr *pgconn.PgError
		state, msg := "", runErr.Error()
		if errors.As(runErr, &pgErr) {
			state, msg = pgErr.Code, pgErr.Message
		}
		if state != "" && queue.Retryable(state) {
			if res.BatchesCommitted > 0 {
				_ = e.repo.UpdateBatchProgress(ctx, w.ExecutionID, res.BatchesCommitted, res.RowsCommitted)
			}
			if _, again := queue.Backoff(job.Attempts); again {
				if err := e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", state, msg, execSQL); err != nil {
					return err
				}
				return &queue.SQLError{SQLState: state, Message: msg, Recorded: true}
			}
		}
		if res.Status == batch.StatusPartial || res.Status == batch.StatusPartiallyReverted {
			partialStatus := StatusPartial
			if res.Status == batch.StatusPartiallyReverted {
				partialStatus = StatusPartiallyReverted
			}
			histCtx, span := historySpan(ctx, w, partialStatus)
			_ = e.repo.MarkPartial(histCtx, w.ExecutionID, partialStatus, state, msg, res.BatchesCommitted, res.RowsCommitted)
			_ = e.repo.InsertAttempt(histCtx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", state, msg, execSQL)
			span.End()
			logx.Audit(ctx, "fail", slog.Int64("execution_id", w.ExecutionID), slog.String("status", partialStatus), slog.String("reason", state), slog.Int64("database", w.DatabaseID))
			return &worker.Halt{Disposition: worker.DispositionDone, Message: msg, SQLState: state, Recorded: true}
		}
		if state == "" {
			state = "batch_failed"
		}
		return e.fail(ctx, w, job, state, msg, execSQL)
	}

	_ = e.repo.UpdateBatchProgress(ctx, w.ExecutionID, res.BatchesCommitted, res.RowsCommitted)
	if err := finalizeBatchTarget(ctx, target, w, job, version); err != nil {
		if uncertainErr(err) {
			*release = false
			return e.uncertain(ctx, w, job, "Hasil commit bukti akhir batch belum dapat dipastikan")
		}
		var pgErr *pgconn.PgError
		state, msg := "target_finalize_failed", "Gagal menulis bukti akhir batch di database target"
		if errors.As(err, &pgErr) {
			state, msg = pgErr.Code, pgErr.Message
			if queue.Retryable(state) {
				if _, again := queue.Backoff(job.Attempts); again {
					if ierr := e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", state, msg, execSQL); ierr != nil {
						return ierr
					}
					return &queue.SQLError{SQLState: state, Message: msg, Recorded: true}
				}
			}
		}
		if res.BatchesCommitted > 0 {
			return e.restorePartial(ctx, w, job, state, msg, execSQL, res.BatchesCommitted, res.RowsCommitted)
		}
		return e.fail(ctx, w, job, state, msg, execSQL)
	}
	return e.succeed(ctx, w, job, execSQL)
}

func finalizeBatchTarget(ctx context.Context, pool *pgxpool.Pool, w Work, job queue.Job, version int32) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := execSimple(ctx, tx, `
		CREATE SCHEMA IF NOT EXISTS _dbmp;
		CREATE TABLE IF NOT EXISTS _dbmp.applied_migrations (
			version INT PRIMARY KEY,
			migration_id BIGINT NOT NULL,
			applied_by BIGINT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE IF NOT EXISTS _dbmp.executions (
			execution_id BIGINT PRIMARY KEY,
			attempt_no INT NOT NULL,
			kind TEXT NOT NULL,
			committed_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		return err
	}
	if w.Kind == KindRollback {
		if _, err := tx.Exec(ctx, `DELETE FROM _dbmp.applied_migrations WHERE migration_id = $1`, w.MigrationID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, `
		INSERT INTO _dbmp.applied_migrations (version, migration_id, applied_by, applied_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (version) DO NOTHING`, version, w.MigrationID, w.RequestedBy); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO _dbmp.executions (execution_id, attempt_no, kind, committed_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (execution_id) DO NOTHING`, w.ExecutionID, job.Attempts, w.Kind); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// recover memeriksa bukti di target. done berarti hasil sudah diputuskan.
// Bila tidak ada bukti dan koneksi sehat, eksekusi boleh diulang.
func (e *Executor) recover(ctx context.Context, w *Work, job queue.Job) (bool, error) {
	lockStart := time.Now()
	lease, err := e.locks.Acquire(ctx, w.DatabaseID, w.ExecutionID, job.ClaimedBy, lock.DefaultLease)
	observeLock(lockStart, err)
	if errors.Is(err, lock.ErrBusy) {
		return true, &worker.Halt{Disposition: worker.DispositionRetry, Message: lock.ErrBusy.Msg, Recorded: true, Reason: "lock_busy"}
	}
	if err != nil {
		return true, err
	}
	target, err := e.conns.Pool(ctx, w.DatabaseID)
	if err != nil {
		return true, e.uncertain(ctx, *w, job, "Database tidak dapat dihubungi untuk memeriksa bukti eksekusi")
	}
	found, err := e.proof(ctx, target, w.ExecutionID)
	if err != nil {
		return true, e.uncertain(ctx, *w, job, "Hasil commit belum dapat dipastikan")
	}
	if found {
		defer lease.Release(context.Background())
		return true, e.succeed(ctx, *w, job, "")
	}
	_ = lease.Release(context.Background())
	if w.Status == StatusUncertain {
		if _, err := e.repo.db.Exec(ctx, `UPDATE executions SET status = 'queued', error_code = NULL WHERE id = $1`, w.ExecutionID); err != nil {
			return true, err
		}
		w.Status = StatusQueued
	}
	return false, nil
}

func (e *Executor) revalidate(ctx context.Context, w Work) error {
	if w.Status != StatusQueued && w.Status != StatusRunning {
		return &worker.Halt{Disposition: worker.DispositionDone, Message: "Eksekusi tidak lagi menunggu dijalankan", Recorded: true}
	}
	down := ""
	if w.DownSQL != nil {
		down = *w.DownSQL
	}
	table, column := "", ""
	size := 0
	if w.BatchTable != nil {
		table = *w.BatchTable
	}
	if w.BatchColumn != nil {
		column = *w.BatchColumn
	}
	if w.BatchSize != nil {
		size = *w.BatchSize
	}
	if HashRevision(w.UpSQL, down, w.ExecMode, table, column, size) != w.SQLHash {
		return haltFail("hash_mismatch", "SQL revisi tidak lagi sama dengan yang disetujui")
	}
	scriptText, err := script(w)
	if err != nil {
		return haltFail("invalid", err.Error())
	}
	if err := ValidateSQL(scriptText); err != nil {
		return haltFail("invalid", "SQL tidak lagi lolos validasi")
	}
	if e.authz != nil {
		ok, err := e.authz.Can(ctx, authz.Principal{UserID: w.RequestedBy}, authz.ActMigrationRun, w.DatabaseID)
		if err != nil {
			return err
		}
		if !ok {
			return haltFail("access_lost", "Peminta tidak lagi punya akses edit")
		}
	}
	blocked, err := e.repo.HardBlocksDatabase(ctx, e.repo.db, w.DatabaseID, w.ExecutionID)
	if err != nil {
		return err
	}
	if blocked {
		return haltFail("blocked", "Masih ada eksekusi yang belum selesai pada database ini")
	}
	running, err := e.repo.HasRunningExecution(ctx, e.repo.db, w.DatabaseID, w.ExecutionID)
	if err != nil {
		return err
	}
	if running {
		return &worker.Halt{Disposition: worker.DispositionRetry, Message: "Eksekusi lain sedang berjalan pada database ini", Recorded: true, Reason: "db_busy"}
	}
	if w.RevertsPartial() {
		// Revert batch committed: hanya mungkin untuk mode batch, dan migration-nya tidak pernah aktif.
		if w.ExecMode != ModeBatch {
			return haltFail("invalid", "Hanya eksekusi batch yang dapat di-revert dari status partial")
		}
		if w.Irreversible {
			return haltFail("irreversible", "Migration ini ditandai tidak dapat di-rollback")
		}
	} else if w.Kind == KindRollback {
		top, err := e.repo.TopVersion(ctx, w.DatabaseID)
		if err != nil {
			return err
		}
		if w.Version == nil || top == 0 || *w.Version != top {
			return haltFail("not_top_of_chain", "Hanya migration teratas pada rantai aktif yang dapat di-rollback")
		}
		if w.Irreversible {
			return haltFail("irreversible", "Migration ini ditandai tidak dapat di-rollback")
		}
	} else if w.Version != nil {
		return haltFail("already_active", "Migration ini masih aktif di rantai")
	}
	if e.check != nil {
		complete, err := e.check.Verify(ctx, e.repo.db, w.ExecutionID, w.DatabaseID, w.ApprovalRound)
		if err != nil {
			return err
		}
		if !complete {
			if err := platformdb.WithTx(ctx, e.pool, func(tx pgx.Tx) error {
				_, err := e.check.Revoke(ctx, tx, w.ExecutionID, w.DatabaseID)
				return err
			}); err != nil {
				return err
			}
			return &worker.Halt{
				Disposition: worker.DispositionDone, AttemptStatus: "failed",
				Message: "Approval tidak lagi lengkap", SQLState: "approval_incomplete",
			}
		}
	}
	return nil
}

func (e *Executor) succeed(ctx context.Context, w Work, job queue.Job, sqlText string) (result error) {
	ctx, span := historySpan(ctx, w, "success")
	defer func() { endHistorySpan(span, result) }()
	version := int32(0)
	if w.Kind == KindRollback {
		if w.Version != nil {
			version = *w.Version
		}
	} else if w.Version != nil {
		version = *w.Version
	} else {
		next, err := e.repo.NextChainVersion(ctx, w.DatabaseID)
		if err != nil {
			return err
		}
		version = next
	}
	if err := e.repo.MarkApplied(ctx, w, version); err != nil {
		// Target sudah commit. Jangan menandai execution gagal; catatan platform dicoba lagi.
		return &worker.Halt{Disposition: worker.DispositionRetry, Message: "Gagal mencatat hasil di database platform", Recorded: true, Reason: "platform_record"}
	}
	_ = e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "success", "", "", sqlText)
	action := "finish"
	if w.Kind == KindRollback {
		action = "rollback"
	}
	logx.Audit(ctx, action, slog.Int64("execution_id", w.ExecutionID), slog.Int64("database", w.DatabaseID), slog.Int64("migration_id", w.MigrationID))
	return &worker.Halt{Disposition: worker.DispositionSuccess, Recorded: true}
}

func (e *Executor) fail(ctx context.Context, w Work, job queue.Job, code, message, sqlText string) error {
	ctx, span := historySpan(ctx, w, StatusFailed)
	defer span.End()
	_ = e.repo.MarkFailedCode(ctx, w.ExecutionID, code, message)
	_ = e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", code, message, sqlText)
	logx.Audit(ctx, "fail", slog.Int64("execution_id", w.ExecutionID), slog.String("reason", code), slog.Int64("database", w.DatabaseID))
	return &worker.Halt{Disposition: worker.DispositionDead, Message: message, SQLState: code, Recorded: true}
}

func (e *Executor) uncertain(ctx context.Context, w Work, job queue.Job, message string) error {
	ctx, span := historySpan(ctx, w, StatusUncertain)
	defer span.End()
	_ = e.repo.MarkUncertain(ctx, w.ExecutionID, message)
	_ = e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "uncertain", "uncertain", message, "")
	return &worker.Halt{Disposition: worker.DispositionUncertain, Message: message, Recorded: true}
}

func (e *Executor) restorePartial(ctx context.Context, w Work, job queue.Job, code, message, sqlText string, batches int, rows int64) error {
	partialStatus := StatusPartial
	if w.Kind == KindRollback {
		partialStatus = StatusPartiallyReverted
	}
	histCtx, span := historySpan(ctx, w, partialStatus)
	_ = e.repo.MarkPartial(histCtx, w.ExecutionID, partialStatus, code, message, batches, rows)
	_ = e.repo.InsertAttempt(histCtx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", code, message, sqlText)
	span.End()
	logx.Audit(ctx, "fail", slog.Int64("execution_id", w.ExecutionID), slog.String("status", partialStatus), slog.String("reason", code), slog.Int64("database", w.DatabaseID))
	return &worker.Halt{Disposition: worker.DispositionDone, Message: message, SQLState: code, Recorded: true}
}

func (e *Executor) record(ctx context.Context, w Work, job queue.Job, halt *worker.Halt) error {
	if halt.Disposition == worker.DispositionDead && halt.SQLState != "" {
		if w.BatchesCommitted > 0 {
			return e.restorePartial(ctx, w, job, halt.SQLState, halt.Message, "", w.BatchesCommitted, w.RowsCommitted)
		}
		_ = e.repo.MarkFailedCode(ctx, w.ExecutionID, halt.SQLState, halt.Message)
		_ = e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", halt.SQLState, halt.Message, "")
		logx.Audit(ctx, "fail", slog.Int64("execution_id", w.ExecutionID), slog.String("reason", halt.SQLState))
		halt.Recorded = true
	}
	if halt.Disposition == worker.DispositionDone && halt.AttemptStatus == "failed" && !halt.Recorded {
		_ = e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", halt.SQLState, halt.Message, "")
		halt.Recorded = true
	}
	return halt
}

// observeLock mencatat lama satu percobaan mengambil kunci database target per hasil.
func observeLock(start time.Time, err error) {
	result := "acquired"
	switch {
	case errors.Is(err, lock.ErrBusy):
		result = "busy"
	case err != nil:
		result = "error"
	}
	metrics.LockAcquire.WithLabelValues(result).Observe(metrics.Since(start))
}

// observeMigration mencatat lama SQL migration di database target. Label hanya kind, mode, dan
// hasil (success atau failed), tanpa ID atau teks SQL.
func observeMigration(kind, mode string, start time.Time, runErr error) {
	result := "success"
	if runErr != nil {
		result = "failed"
	}
	metrics.MigrationDuration.WithLabelValues(kind, mode, result).Observe(metrics.Since(start))
}

func script(w Work) (string, error) {
	if w.Kind == KindRollback {
		if w.DownSQL == nil || *w.DownSQL == "" {
			return "", errors.New("SQL down tidak tersedia")
		}
		return *w.DownSQL, nil
	}
	return w.UpSQL, nil
}

func haltFail(code, message string) error {
	return &worker.Halt{Disposition: worker.DispositionDead, SQLState: code, Message: message}
}

// heartbeatEvery bisa dipendekkan tes. Produksi memakai jarak heartbeat kunci.
var heartbeatEvery = lock.HeartbeatEvery

// heartbeat memperpanjang lease kunci database dan menyegarkan claimed_at job selama eksekusi berjalan.
// Jika lease direbut (lock.ErrBusy), onLost dipanggil untuk membatalkan eksekusi yang sedang berjalan.
func heartbeat(ctx context.Context, lease lock.Lease, touchJob func(context.Context) error, onLost ...func()) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := lease.Heartbeat(ctx); err != nil {
				slog.WarnContext(ctx, "lock.heartbeat", "err", err, "database", lease.DatabaseID(), "execution_id", lease.ExecutionID())
				if errors.Is(err, lock.ErrBusy) {
					for _, fn := range onLost {
						if fn != nil {
							fn()
						}
					}
					return
				}
			}
			if touchJob != nil {
				if err := touchJob(ctx); err != nil {
					slog.WarnContext(ctx, "job.heartbeat", "err", err, "execution_id", lease.ExecutionID())
				}
			}
		}
	}
}

func applyTarget(ctx context.Context, pool *pgxpool.Pool, w Work, job queue.Job, version int32, sqlText string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := execSimple(ctx, tx, "SET LOCAL statement_timeout = '60s'"); err != nil {
		return err
	}
	if err := execSimple(ctx, tx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return err
	}
	if err := execSimple(ctx, tx, `
		CREATE SCHEMA IF NOT EXISTS _dbmp;
		CREATE TABLE IF NOT EXISTS _dbmp.applied_migrations (
			version INT PRIMARY KEY,
			migration_id BIGINT NOT NULL,
			applied_by BIGINT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE IF NOT EXISTS _dbmp.executions (
			execution_id BIGINT PRIMARY KEY,
			attempt_no INT NOT NULL,
			kind TEXT NOT NULL,
			committed_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		return err
	}
	if err := execSimple(ctx, tx, sqlText); err != nil {
		return err
	}
	if w.Kind == KindRollback {
		if _, err := tx.Exec(ctx, `DELETE FROM _dbmp.applied_migrations WHERE migration_id = $1`, w.MigrationID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, `
		INSERT INTO _dbmp.applied_migrations (version, migration_id, applied_by, applied_at)
		VALUES ($1, $2, $3, now())`, version, w.MigrationID, w.RequestedBy); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO _dbmp.executions (execution_id, attempt_no, kind, committed_at)
		VALUES ($1, $2, $3, now())`, w.ExecutionID, job.Attempts, w.Kind); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func proofExists(ctx context.Context, pool *pgxpool.Pool, execID int64) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = '_dbmp' AND table_name = 'executions')`).Scan(&exists)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	var found bool
	err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM _dbmp.executions WHERE execution_id = $1)`, execID).Scan(&found)
	return found, err
}

func execSimple(ctx context.Context, tx pgx.Tx, sql string) error {
	results, err := tx.Conn().PgConn().Exec(ctx, sql).ReadAll()
	if err != nil {
		return err
	}
	for _, r := range results {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

// uncertainErr hanya untuk hasil commit yang benar-benar belum pasti: kelas 08,
// shutdown server, atau deadline. Deadlock, serialization failure, dan too_many_connections
// sudah pasti membatalkan transaksi, jadi tidak masuk sini.
func uncertainErr(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := strings.ToUpper(strings.TrimSpace(pgErr.Code))
		if strings.HasPrefix(code, "08") || code == "57P01" {
			return true
		}
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// Pastikan Executor tetap memenuhi Runner.
var _ worker.Runner = (*Executor)(nil)

// historySpan membuka span untuk pencatatan hasil execution di database platform (status, attempt, rantai).
func historySpan(ctx context.Context, w Work, status string) (context.Context, trace.Span) {
	return telemetry.Tracer().Start(ctx, "platform.update_history", trace.WithAttributes(
		attribute.Int64("execution_id", w.ExecutionID), attribute.String("status", status)))
}

// endHistorySpan menandai span error hanya bila pencatatan platform gagal dan akan dicoba lagi;
// Halt sukses adalah hasil normal, bukan error.
func endHistorySpan(span trace.Span, result error) {
	var halt *worker.Halt
	if errors.As(result, &halt) && halt.Disposition == worker.DispositionRetry {
		telemetry.End(span, result)
		return
	}
	span.End()
}

// logTxOutcome mencatat commit atau rollback transaction target secara terpisah dari log SQL,
// sama seperti event commit/rollback per batch di mode batch.
func logTxOutcome(ctx context.Context, w Work, job queue.Job, runErr error) {
	attrs := []any{"execution_id", w.ExecutionID, "migration_id", w.MigrationID, "kind", w.Kind, "attempt", job.Attempts, "database", w.DatabaseID}
	switch {
	case runErr == nil:
		slog.InfoContext(ctx, "commit", append(attrs, "status", "ok")...)
	case uncertainErr(runErr):
		slog.WarnContext(ctx, "commit", append(attrs, "status", "uncertain", "err", logx.RedactString(runErr.Error()))...)
	default:
		var pgErr *pgconn.PgError
		state := ""
		if errors.As(runErr, &pgErr) {
			state = pgErr.Code
		}
		slog.WarnContext(ctx, "rollback", append(attrs, "status", "error", "sqlstate", state, "err", logx.RedactString(runErr.Error()))...)
	}
}
