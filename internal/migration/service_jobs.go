package migration

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
)

// WithQueue memasang antrian job. Tanpa ini, enqueue mengembalikan kesalahan server.
func (s *Service) WithQueue(q *queue.PgQueue) *Service {
	s.jobs = q
	return s
}

// Enqueue memasukkan execution yang sudah disetujui ke antrian dan mengembalikan job ID.
// Publish kedua untuk execution yang sama tidak membuat job baru.
func (s *Service) Enqueue(ctx context.Context, p authz.Principal, execID int64, carrier map[string]string) (int64, error) {
	return s.publish(ctx, p, execID, carrier, false)
}

// Resume melanjutkan execution partial dengan approval yang sudah ada, tanpa ronde baru.
func (s *Service) Resume(ctx context.Context, p authz.Principal, execID int64, carrier map[string]string) (int64, error) {
	return s.publish(ctx, p, execID, carrier, true)
}

func apperrInternal() error {
	return apperr.Internal("Antrian job belum dihubungkan", nil)
}

func (s *Service) publish(ctx context.Context, p authz.Principal, execID int64, carrier map[string]string, resume bool) (int64, error) {
	if s.jobs == nil {
		return 0, apperrInternal()
	}
	var jobID int64
	var dbID int64
	err := s.inTx(ctx, func(q platformdb.DBTX) error {
		databaseID, status, _, err := s.repo.LockExecution(ctx, q, execID)
		if err != nil {
			return err
		}
		dbID = databaseID
		if resume {
			if status != StatusPartial && status != StatusPartiallyReverted {
				return ErrNotResumable
			}
			if status == StatusPartial {
				// Batch yang sudah/sedang dibatalkan tidak boleh diterapkan ulang lewat resume.
				reverted, err := s.repo.HasActiveRevert(ctx, q, execID)
				if err != nil {
					return err
				}
				if reverted {
					return ErrReverted
				}
			}
		} else if status == StatusQueued {
			id, err := s.jobs.Enqueue(ctx, q, execID, databaseID, carrier)
			if errors.Is(err, queue.ErrRunning) {
				return ErrJobRunning
			}
			if err != nil {
				return err
			}
			jobID = id
			return nil
		} else if status != StatusApproved {
			return ErrNotApproved
		}
		blocked, err := s.repo.BlocksDatabase(ctx, q, databaseID, execID)
		if err != nil {
			return err
		}
		if blocked {
			return ErrBlocked
		}
		id, err := s.jobs.Enqueue(ctx, q, execID, databaseID, carrier)
		if errors.Is(err, queue.ErrRunning) {
			return ErrJobRunning
		}
		if err != nil {
			return err
		}
		if err := s.repo.SetQueued(ctx, q, execID); err != nil {
			return err
		}
		jobID = id
		return nil
	})
	if err != nil {
		return 0, err
	}
	action := "enqueue"
	if resume {
		action = "resume"
	}
	logx.Audit(logx.WithActor(ctx, p.Username), action,
		slog.Int64("execution_id", execID),
		slog.Int64("job_id", jobID),
		slog.Int64("database", dbID),
	)
	return jobID, nil
}

// Cancel membatalkan execution yang belum diambil worker.
func (s *Service) Cancel(ctx context.Context, p authz.Principal, execID int64) error {
	if s.jobs == nil {
		return apperrInternal()
	}
	var dbID int64
	err := s.inTx(ctx, func(q platformdb.DBTX) error {
		databaseID, status, _, err := s.repo.LockExecution(ctx, q, execID)
		if err != nil {
			return err
		}
		dbID = databaseID
		switch status {
		case StatusApproved:
			return s.repo.SetCancelled(ctx, q, execID)
		case StatusQueued:
			jobStatus, err := s.jobs.JobStatus(ctx, q, execID)
			if err != nil {
				return err
			}
			if jobStatus == queue.StatusRunning {
				return ErrJobRunning
			}
			if _, err := s.jobs.FinishQueued(ctx, q, execID); err != nil {
				return err
			}
			return s.repo.SetCancelled(ctx, q, execID)
		default:
			return ErrNotCancellable
		}
	})
	if err != nil {
		return err
	}
	logx.Audit(logx.WithActor(ctx, p.Username), "cancel",
		slog.Int64("execution_id", execID), slog.Int64("database", dbID))
	return nil
}

// Job mengembalikan status job beserta execution-nya untuk polling.
func (s *Service) Job(ctx context.Context, jobID int64) (queue.Job, Execution, error) {
	if s.jobs == nil {
		return queue.Job{}, Execution{}, apperrInternal()
	}
	job, err := s.jobs.Get(ctx, jobID)
	if errors.Is(err, queue.ErrNotFound) {
		return queue.Job{}, Execution{}, ErrJobNotFound
	}
	if err != nil {
		return queue.Job{}, Execution{}, err
	}
	exec, err := s.repo.GetExecution(ctx, s.pool, job.ExecutionID)
	if err != nil {
		return queue.Job{}, Execution{}, err
	}
	return job, exec, nil
}
