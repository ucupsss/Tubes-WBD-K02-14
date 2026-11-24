// Package worker mengambil job dari antrian dan mencatat hasilnya.
//
// Percobaan SQL target dilakukan oleh Runner. Pada paket ini runner bawaan
// belum menjalankan SQL; lock dan eksekusi menyusul.
//
// Pemilik: C.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
)

// Runner menjalankan satu job. Nil berarti sukses.
type Runner interface {
	Run(ctx context.Context, job queue.Job) error
}

// Jobs adalah bagian antrian yang dipakai loop worker.
type Jobs interface {
	Claim(ctx context.Context, workerID string) (*queue.Job, error)
	Complete(ctx context.Context, jobID int64, status string) error
	Requeue(ctx context.Context, jobID int64, after time.Duration) error
}

// Executions mengubah status execution dan mencatat attempt.
type Executions interface {
	ExecutionStatus(ctx context.Context, execID int64) (string, error)
	MarkRunning(ctx context.Context, execID int64) (bool, error)
	MarkQueued(ctx context.Context, execID int64) error
	MarkFailed(ctx context.Context, execID int64, message string) error
	MarkSucceeded(ctx context.Context, execID int64) error
	InsertAttempt(ctx context.Context, execID int64, attemptNo int, workerID, status, sqlstate, message string) error
}

// Config mengatur loop pengambilan job.
type Config struct {
	WorkerID    string
	Concurrency int
	Poll        time.Duration
}

// Unavailable adalah runner sementara: job dicatat gagal tanpa menyentuh database target.
type Unavailable struct{}

func (Unavailable) Run(context.Context, queue.Job) error {
	return &queue.SQLError{SQLState: "0A000", Message: "Eksekusi target belum diaktifkan"}
}

// Run mengambil job sampai ctx dibatalkan.
func Run(ctx context.Context, jobs Jobs, exec Executions, runner Runner, cfg Config) error {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Poll <= 0 {
		cfg.Poll = time.Second
	}
	if runner == nil {
		runner = Unavailable{}
	}
	slots := make(chan struct{}, cfg.Concurrency)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case slots <- struct{}{}:
		}
		job, err := jobs.Claim(ctx, cfg.WorkerID)
		if err != nil {
			<-slots
			return err
		}
		if job == nil {
			<-slots
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.Poll):
			}
			continue
		}
		go func(job queue.Job) {
			defer func() { <-slots }()
			if err := Process(ctx, jobs, exec, runner, cfg.WorkerID, job); err != nil {
				slog.ErrorContext(ctx, "job.process", "err", err, "job_id", job.ID, "execution_id", job.ExecutionID)
			}
		}(*job)
	}
}

// Process mengerjakan satu job yang sudah di-claim: menjalankan runner, lalu selesai, retry, atau dead.
func Process(ctx context.Context, jobs Jobs, exec Executions, runner Runner, workerID string, job queue.Job) error {
	if id := job.TraceContext["correlation_id"]; id != "" {
		ctx = logx.WithCorrelation(ctx, id)
	}
	status, err := exec.ExecutionStatus(ctx, job.ExecutionID)
	if err != nil {
		return err
	}
	if status == "cancelled" {
		return jobs.Complete(ctx, job.ID, queue.StatusDone)
	}
	ok, err := exec.MarkRunning(ctx, job.ExecutionID)
	if err != nil {
		return err
	}
	if !ok {
		return jobs.Complete(ctx, job.ID, queue.StatusDone)
	}
	slog.InfoContext(ctx, "job.claimed", "job_id", job.ID, "execution_id", job.ExecutionID, "attempt", job.Attempts, "worker_id", workerID)

	runErr := runner.Run(ctx, job)
	sqlstate, message := "", ""
	var sqlErr *queue.SQLError
	if errors.As(runErr, &sqlErr) {
		sqlstate = sqlErr.SQLState
		message = sqlErr.Error()
	} else if runErr != nil {
		message = "Migration gagal dijalankan"
	}
	attemptStatus := "success"
	if runErr != nil {
		attemptStatus = "failed"
	}
	if err := exec.InsertAttempt(ctx, job.ExecutionID, job.Attempts, workerID, attemptStatus, sqlstate, message); err != nil {
		return err
	}
	if runErr == nil {
		if err := exec.MarkSucceeded(ctx, job.ExecutionID); err != nil {
			return err
		}
		return jobs.Complete(ctx, job.ID, queue.StatusDone)
	}
	if sqlErr != nil && queue.Retryable(sqlErr.SQLState) {
		if wait, again := queue.Backoff(job.Attempts); again {
			if err := exec.MarkQueued(ctx, job.ExecutionID); err != nil {
				return err
			}
			slog.InfoContext(ctx, "job.requeue", "job_id", job.ID, "attempt", job.Attempts, "sqlstate", sqlstate, "wait", wait.String())
			return jobs.Requeue(ctx, job.ID, wait)
		}
	}
	if message == "" {
		message = "Migration gagal dijalankan"
	}
	if err := exec.MarkFailed(ctx, job.ExecutionID, message); err != nil {
		return err
	}
	slog.InfoContext(ctx, "job.dead", "job_id", job.ID, "execution_id", job.ExecutionID, "sqlstate", sqlstate)
	return jobs.Complete(ctx, job.ID, queue.StatusDead)
}
