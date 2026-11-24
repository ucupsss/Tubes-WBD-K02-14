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
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *PgQueue { return &PgQueue{pool: pool} }

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
		INSERT INTO migration_jobs (execution_id, database_id, status, trace_context)
		VALUES ($1, $2, 'queued', $3::jsonb)
		ON CONFLICT (execution_id) DO UPDATE
		SET status = 'queued',
		    database_id = EXCLUDED.database_id,
		    run_after = now(),
		    claimed_by = NULL,
		    claimed_at = NULL,
		    trace_context = EXCLUDED.trace_context,
		    enqueued_at = now(),
		    attempts = 0
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
			UPDATE migration_jobs j
			SET status = 'running', claimed_by = $1, claimed_at = now(), attempts = j.attempts + 1
			WHERE j.id = (
				SELECT id FROM migration_jobs
				WHERE status = 'queued' AND run_after <= now()
				ORDER BY id
				FOR UPDATE SKIP LOCKED
				LIMIT 1)
			RETURNING id, execution_id, database_id, status, attempts, claimed_by,
			          enqueued_at, claimed_at, run_after, trace_context`, workerID)
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
