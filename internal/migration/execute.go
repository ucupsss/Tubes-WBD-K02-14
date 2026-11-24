package migration

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/lock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
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

// Executor menjalankan SQL normal di database target setelah kunci dan revalidasi.
// Mode batch ditolak sampai paket batch tersedia.
type Executor struct {
	repo  *PgRepo
	pool  *pgxpool.Pool
	conns dbconn.Manager
	locks *lock.PgLocker
	check Checker
	authz authz.Authorizer
	proof func(ctx context.Context, pool *pgxpool.Pool, execID int64) (bool, error)
}

func NewExecutor(pool *pgxpool.Pool, repo *PgRepo, conns dbconn.Manager, locks *lock.PgLocker, check Checker, az authz.Authorizer) *Executor {
	if repo != nil && repo.db == nil {
		repo = repo.Bind(pool)
	}
	return &Executor{repo: repo, pool: pool, conns: conns, locks: locks, check: check, authz: az, proof: proofExists}
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
	if w.ExecMode == ModeBatch {
		return e.fail(ctx, w, job, "batch_unavailable", "Mode batch belum tersedia", "")
	}
	if w.Status == StatusUncertain || w.Status == StatusRunning {
		done, err := e.recover(ctx, &w, job)
		if err != nil || done {
			return err
		}
	}
	if err := e.revalidate(ctx, w); err != nil {
		var halt *worker.Halt
		if errors.As(err, &halt) {
			return e.record(ctx, w, job, halt)
		}
		return err
	}
	lease, err := e.locks.Acquire(ctx, w.DatabaseID, w.ExecutionID, job.ClaimedBy, lock.DefaultLease)
	if errors.Is(err, lock.ErrBusy) {
		return &worker.Halt{Disposition: worker.DispositionRetry, Message: lock.ErrBusy.Msg, Recorded: true}
	}
	if err != nil {
		return err
	}
	beat, stopBeat := context.WithCancel(ctx)
	defer stopBeat()
	go heartbeat(beat, lease)

	release := true
	defer func() {
		if release {
			_ = lease.Release(context.Background())
		}
	}()

	target, err := e.conns.Pool(ctx, w.DatabaseID)
	if err != nil {
		return e.fail(ctx, w, job, "target_unavailable", "Database tidak dapat dihubungi", "")
	}
	found, err := e.proof(ctx, target, w.ExecutionID)
	if err != nil {
		release = false
		return e.uncertain(ctx, w, job, "Hasil commit belum dapat dipastikan")
	}
	if found {
		return e.succeed(ctx, w, job, "")
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
		version, err = e.repo.NextChainVersion(ctx, w.DatabaseID)
		if err != nil {
			return err
		}
	}

	logx.Audit(ctx, "start", slog.Int64("execution_id", w.ExecutionID), slog.Int64("database", w.DatabaseID), slog.String("kind", w.Kind))
	start := time.Now()
	runErr := applyTarget(ctx, target, w, job, version, sqlText)
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
			_ = e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", state, msg, sqlText)
			return &queue.SQLError{SQLState: state, Message: msg}
		}
		return e.fail(ctx, w, job, state, msg, sqlText)
	}
	return e.succeed(ctx, w, job, sqlText)
}

// recover memeriksa bukti di target. done berarti hasil sudah diputuskan.
// Bila tidak ada bukti dan koneksi sehat, eksekusi boleh diulang.
func (e *Executor) recover(ctx context.Context, w *Work, job queue.Job) (bool, error) {
	lease, err := e.locks.Acquire(ctx, w.DatabaseID, w.ExecutionID, job.ClaimedBy, lock.DefaultLease)
	if errors.Is(err, lock.ErrBusy) {
		return true, &worker.Halt{Disposition: worker.DispositionRetry, Message: lock.ErrBusy.Msg, Recorded: true}
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
	blocked, err := e.repo.BlocksDatabase(ctx, e.repo.db, w.DatabaseID, w.ExecutionID)
	if err != nil {
		return err
	}
	if blocked {
		return haltFail("blocked", "Masih ada eksekusi yang belum selesai pada database ini")
	}
	if w.Kind == KindRollback {
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
			if _, err := e.check.Revoke(ctx, e.repo.db, w.ExecutionID, w.DatabaseID); err != nil {
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

func (e *Executor) succeed(ctx context.Context, w Work, job queue.Job, sqlText string) error {
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
		return err
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
	_ = e.repo.MarkFailedCode(ctx, w.ExecutionID, code, message)
	_ = e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "failed", code, message, sqlText)
	logx.Audit(ctx, "fail", slog.Int64("execution_id", w.ExecutionID), slog.String("reason", code), slog.Int64("database", w.DatabaseID))
	return &worker.Halt{Disposition: worker.DispositionDead, Message: message, SQLState: code, Recorded: true}
}

func (e *Executor) uncertain(ctx context.Context, w Work, job queue.Job, message string) error {
	_ = e.repo.MarkUncertain(ctx, w.ExecutionID, message)
	_ = e.repo.InsertAttempt(ctx, w.ExecutionID, job.Attempts, job.ClaimedBy, "uncertain", "uncertain", message, "")
	return &worker.Halt{Disposition: worker.DispositionUncertain, Message: message, Recorded: true}
}

func (e *Executor) record(ctx context.Context, w Work, job queue.Job, halt *worker.Halt) error {
	if halt.Disposition == worker.DispositionDead && halt.SQLState != "" {
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

func heartbeat(ctx context.Context, lease lock.Lease) {
	t := time.NewTicker(lock.HeartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := lease.Heartbeat(ctx); err != nil {
				slog.WarnContext(ctx, "lock.heartbeat", "err", err, "database", lease.DatabaseID(), "execution_id", lease.ExecutionID())
				return
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

func uncertainErr(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && queue.Retryable(pgErr.Code) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// Pastikan Executor tetap memenuhi Runner.
var _ worker.Runner = (*Executor)(nil)
