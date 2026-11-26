package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// PgQueue menyimpan job di tabel migration_jobs.
type PgQueue struct {
	pool  *pgxpool.Pool
	stale time.Duration
}

// New membuat antrian di pool platform. Job running yang diam lebih dari 30 detik boleh diambil lagi.
func New(pool *pgxpool.Pool) *PgQueue {
	return &PgQueue{pool: pool, stale: 30 * time.Second}
}

// Enqueue membuat job atau mengembalikan baris yang sama ke status queued.
// execution_id unik, jadi publish ganda tidak membuat job kedua.
func (q *PgQueue) Enqueue(ctx context.Context, db platformdb.DBTX, execID, dbID int64, carrier map[string]string) (int64, error) {
	raw, err := json.Marshal(carrier)
	if err != nil {
		return 0, fmt.Errorf("queue.Enqueue: %w", err)
	}
	if carrier == nil {
		raw = []byte("{}")
	}
	var id int64
	err = db.QueryRow(ctx, `
		INSERT INTO migration_jobs (execution_id, database_id, status, trace_context, attempts)
		VALUES (
			$1, $2, 'queued', $3::jsonb,
			COALESCE((SELECT MAX(attempt_no) FROM execution_attempts WHERE execution_id = $1), 0))
		ON CONFLICT (execution_id) DO UPDATE
		SET status = 'queued',
		    database_id = EXCLUDED.database_id,
		    run_after = CASE WHEN migration_jobs.status = 'queued' THEN migration_jobs.run_after ELSE now() END,
		    claimed_by = NULL,
		    claimed_at = NULL,
		    trace_context = EXCLUDED.trace_context,
		    enqueued_at = CASE WHEN migration_jobs.status = 'queued' THEN migration_jobs.enqueued_at ELSE now() END,
		    attempts = CASE
		        WHEN migration_jobs.status = 'queued' THEN migration_jobs.attempts
		        ELSE COALESCE((SELECT MAX(attempt_no) FROM execution_attempts WHERE execution_id = migration_jobs.execution_id), 0)
		    END
		WHERE migration_jobs.status <> 'running'
		RETURNING id`, execID, dbID, string(raw)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errRunning
	}
	if err != nil {
		return 0, fmt.Errorf("queue.Enqueue: %w", err)
	}
	return id, nil
}

// Claim mengambil satu job yang sudah waktunya, atau nil bila antrian kosong.
func (q *PgQueue) Claim(ctx context.Context, workerID string) (*Job, error) {
	var job *Job
	err := platformdb.WithTx(ctx, q.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			WITH picked AS (
				SELECT id, status FROM migration_jobs
				WHERE (status = 'queued' AND run_after <= now())
				   OR (status = 'running' AND claimed_at <= now() - make_interval(secs => $2))
				ORDER BY id
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			UPDATE migration_jobs j
			SET status = 'running',
			    claimed_by = $1,
			    claimed_at = now(),
			    attempts = CASE WHEN picked.status = 'queued' THEN j.attempts + 1 ELSE j.attempts END
			FROM picked
			WHERE j.id = picked.id
			RETURNING j.id, j.execution_id, j.database_id, j.status, j.attempts, j.claimed_by,
			          j.enqueued_at, j.claimed_at, j.run_after, j.trace_context`, workerID, q.stale.Seconds())
		got, err := scanJob(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		job = &got
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("queue.Claim: %w", err)
	}
	return job, nil
}

// Complete menutup job sebagai done atau dead.
func (q *PgQueue) Complete(ctx context.Context, jobID int64, status string) error {
	if status != StatusDone && status != StatusDead {
		return fmt.Errorf("queue.Complete: status %s", status)
	}
	tag, err := q.pool.Exec(ctx, `
		UPDATE migration_jobs
		SET status = $2, claimed_by = NULL
		WHERE id = $1`, jobID, status)
	if err != nil {
		return fmt.Errorf("queue.Complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Touch memperbarui claimed_at job yang masih dipegang workerID. Dipanggil dari heartbeat selama eksekusi
// berjalan, supaya job yang lebih lama dari batas stale tidak dianggap ditinggalkan lalu diklaim lagi
// (oleh worker lain, atau oleh worker yang sama, sehingga dua run berjalan bersamaan).
func Touch(ctx context.Context, db platformdb.DBTX, jobID int64, workerID string) error {
	_, err := db.Exec(ctx, `
		UPDATE migration_jobs SET claimed_at = now()
		WHERE id = $1 AND claimed_by = $2 AND status = 'running'`, jobID, workerID)
	if err != nil {
		return fmt.Errorf("queue.Touch: %w", err)
	}
	return nil
}

// RestoreAttempt mengurangi hitungan attempt yang terpakai hanya karena kunci sedang sibuk.
func (q *PgQueue) RestoreAttempt(ctx context.Context, jobID int64) error {
	_, err := q.pool.Exec(ctx, `UPDATE migration_jobs SET attempts = GREATEST(attempts - 1, 0) WHERE id = $1`, jobID)
	if err != nil {
		return fmt.Errorf("queue.RestoreAttempt: %w", err)
	}
	return nil
}

// Requeue mengembalikan job yang sedang dipegang ke antrian setelah jeda.
func (q *PgQueue) Requeue(ctx context.Context, jobID int64, after time.Duration) error {
	tag, err := q.pool.Exec(ctx, `
		UPDATE migration_jobs
		SET status = 'queued', run_after = now() + make_interval(secs => $2), claimed_by = NULL, claimed_at = NULL
		WHERE id = $1 AND status = 'running'`, jobID, after.Seconds())
	if err != nil {
		return fmt.Errorf("queue.Requeue: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get membaca satu job.
func (q *PgQueue) Get(ctx context.Context, jobID int64) (Job, error) {
	job, err := scanJob(q.pool.QueryRow(ctx, `
		SELECT id, execution_id, database_id, status, attempts, COALESCE(claimed_by, ''),
		       enqueued_at, claimed_at, run_after, trace_context
		FROM migration_jobs WHERE id = $1`, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("queue.Get: %w", err)
	}
	return job, nil
}

// FinishQueued menutup job queued milik execution. Mengembalikan false bila tidak ada job queued.
func (q *PgQueue) FinishQueued(ctx context.Context, db platformdb.DBTX, execID int64) (bool, error) {
	tag, err := db.Exec(ctx, `
		UPDATE migration_jobs SET status = 'done', claimed_by = NULL
		WHERE execution_id = $1 AND status = 'queued'`, execID)
	if err != nil {
		return false, fmt.Errorf("queue.FinishQueued: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// JobStatus membaca status job milik satu execution. Kosong bila belum ada job.
func (q *PgQueue) JobStatus(ctx context.Context, db platformdb.DBTX, execID int64) (string, error) {
	var status string
	err := db.QueryRow(ctx, `SELECT status FROM migration_jobs WHERE execution_id = $1`, execID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("queue.JobStatus: %w", err)
	}
	return status, nil
}

// ErrNotFound berarti baris migration_jobs tidak ada.
var (
	ErrNotFound = fmt.Errorf("queue: job tidak ditemukan")
	errRunning  = fmt.Errorf("queue: job sedang berjalan")
)

// ErrRunning adalah publish yang bentrok dengan job yang masih dipegang worker.
var ErrRunning = errRunning

func scanJob(row interface{ Scan(dest ...any) error }) (Job, error) {
	var j Job
	var raw []byte
	err := row.Scan(&j.ID, &j.ExecutionID, &j.DatabaseID, &j.Status, &j.Attempts, &j.ClaimedBy,
		&j.EnqueuedAt, &j.ClaimedAt, &j.RunAfter, &raw)
	if err != nil {
		return Job{}, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &j.TraceContext)
	}
	if j.TraceContext == nil {
		j.TraceContext = map[string]string{}
	}
	return j, nil
}
