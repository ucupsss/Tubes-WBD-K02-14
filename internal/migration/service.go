package migration

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
)

// RoundOpener membuka baris approval. Dipenuhi approval.Service.
type RoundOpener interface {
	OpenRound(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) error
}

// Service menerapkan aturan migration. SQL target tidak dijalankan di sini.
type Service struct {
	pool   *pgxpool.Pool
	repo   *PgRepo
	rounds RoundOpener
	jobs   *queue.PgQueue
}

func NewService(pool *pgxpool.Pool, repo *PgRepo, rounds RoundOpener) *Service {
	if repo == nil {
		repo = NewPgRepo()
	}
	return &Service{pool: pool, repo: repo, rounds: rounds}
}

func (s *Service) inTx(ctx context.Context, fn func(q platformdb.DBTX) error) error {
	return platformdb.WithTx(ctx, s.pool, func(tx pgx.Tx) error { return fn(tx) })
}

func (s *Service) Create(ctx context.Context, p authz.Principal, databaseID int64, in Input) (Migration, error) {
	in = in.Normalized()
	if err := in.Validate(); err != nil {
		return Migration{}, err
	}
	var created Migration
	err := s.inTx(ctx, func(q platformdb.DBTX) error {
		m, err := s.repo.Create(ctx, q, databaseID, p.UserID, in.Title, revisionFrom(in, p.UserID))
		if err != nil {
			return err
		}
		created = m
		return nil
	})
	return created, err
}

func (s *Service) Update(ctx context.Context, p authz.Principal, databaseID, id int64, in Input) (Migration, error) {
	in = in.Normalized()
	if err := in.Validate(); err != nil {
		return Migration{}, err
	}
	var updated Migration
	err := s.inTx(ctx, func(q platformdb.DBTX) error {
		current, h, err := s.repo.LockHead(ctx, q, databaseID, id)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateTitle(ctx, q, databaseID, id, p.UserID, in.Title); err != nil {
			return err
		}
		rev := revisionFrom(in, p.UserID)
		if current.Revision.FrozenAt == nil {
			rev.ID = current.Revision.ID
			if err := s.repo.UpdateRevision(ctx, q, rev); err != nil {
				return err
			}
		} else {
			if h.Open {
				return ErrBlocked
			}
			rev.MigrationID = id
			rev.RevisionNo = current.Revision.RevisionNo + 1
			if err := s.repo.AddRevision(ctx, q, databaseID, id, rev); err != nil {
				return err
			}
		}
		updated, err = s.repo.GetMigration(ctx, q, databaseID, id)
		return err
	})
	return updated, err
}

func (s *Service) Delete(ctx context.Context, databaseID, id int64) error {
	return s.inTx(ctx, func(q platformdb.DBTX) error {
		if _, h, err := s.repo.LockHead(ctx, q, databaseID, id); err != nil {
			return err
		} else if h.Frozen || h.HasFailed || h.HasRolledBack || h.ActiveSuccess || h.Open {
			return ErrNotDeletable
		}
		return s.repo.DeleteDraft(ctx, q, databaseID, id)
	})
}

func (s *Service) Get(ctx context.Context, databaseID, id int64) (Migration, error) {
	return s.repo.GetMigration(ctx, s.pool, databaseID, id)
}

func (s *Service) List(ctx context.Context, databaseID int64, page, size int) ([]Migration, int, error) {
	page, size = normalizePage(page, size)
	return s.repo.List(ctx, s.pool, databaseID, size, (page-1)*size)
}

func (s *Service) Submit(ctx context.Context, p authz.Principal, databaseID, id int64) (Execution, error) {
	return s.open(ctx, p, databaseID, id, KindApply)
}

func (s *Service) Forward(ctx context.Context, p authz.Principal, databaseID, id int64) (Execution, error) {
	return s.open(ctx, p, databaseID, id, KindForward)
}

func (s *Service) Rollback(ctx context.Context, p authz.Principal, databaseID, id int64) (Execution, error) {
	return s.open(ctx, p, databaseID, id, KindRollback)
}

func (s *Service) open(ctx context.Context, p authz.Principal, databaseID, id int64, kind string) (Execution, error) {
	var execID int64
	err := s.inTx(ctx, func(q platformdb.DBTX) error {
		m, h, err := s.repo.LockHead(ctx, q, databaseID, id)
		if err != nil {
			return err
		}
		switch kind {
		case KindApply:
			err = h.canSubmit()
		case KindForward:
			err = h.canForward()
		default:
			err = h.canRollback()
		}
		if err != nil {
			return err
		}
		e := Execution{
			DatabaseID:    databaseID,
			MigrationID:   id,
			RevisionID:    m.Revision.ID,
			Kind:          kind,
			Status:        StatusPendingReview,
			RequestedBy:   p.UserID,
			ApprovalRound: 1,
		}
		if kind == KindRollback {
			target, revisionID, err := s.repo.ActiveSuccessID(ctx, q, id)
			if err != nil {
				return err
			}
			e.RollsBackExecutionID = &target
			e.RevisionID = revisionID
		}
		execID, err = s.repo.InsertExecution(ctx, q, e)
		if err != nil {
			return err
		}
		if kind == KindApply {
			if err := s.repo.Freeze(ctx, q, m.Revision.ID); err != nil {
				return err
			}
		}
		if s.rounds == nil {
			return apperr.Internal("approval belum dihubungkan", nil)
		}
		return s.rounds.OpenRound(ctx, q, execID, databaseID, 1)
	})
	if err != nil {
		return Execution{}, err
	}
	action := "submit"
	if kind == KindRollback {
		action = "rollback"
	}
	logx.Audit(logx.WithActor(ctx, p.Username), action,
		slog.Int64("migration_id", id),
		slog.Int64("execution_id", execID),
		slog.String("kind", kind),
		slog.Int64("database", databaseID),
	)
	return s.repo.GetExecution(ctx, s.pool, execID)
}

func (s *Service) ListExecutions(ctx context.Context, databaseID int64, hide bool, page, size int) ([]Execution, int, error) {
	page, size = normalizePage(page, size)
	return s.repo.ListExecutions(ctx, s.pool, databaseID, hide, size, (page-1)*size)
}

func (s *Service) GetExecution(ctx context.Context, id int64) (Execution, []Attempt, error) {
	e, err := s.repo.GetExecution(ctx, s.pool, id)
	if err != nil {
		return Execution{}, nil, err
	}
	attempts, err := s.repo.ListAttempts(ctx, s.pool, id)
	if err != nil {
		return Execution{}, nil, err
	}
	return e, attempts, nil
}

// ExplainSource mengembalikan SQL up revisi yang belum diajukan.
func (s *Service) ExplainSource(ctx context.Context, databaseID, id int64) (title, sql string, err error) {
	m, err := s.repo.GetMigration(ctx, s.pool, databaseID, id)
	if err != nil {
		return "", "", err
	}
	if m.Revision.FrozenAt != nil {
		return "", "", ErrNotAnalyzable
	}
	return m.Title, m.Revision.UpSQL, nil
}

func revisionFrom(in Input, userID int64) Revision {
	down := in.DownSQL
	rev := Revision{
		UpSQL:        in.UpSQL,
		Irreversible: in.Irreversible,
		ExecMode:     in.ExecMode,
		CreatedBy:    userID,
		SQLHash:      HashRevision(in.UpSQL, down, in.ExecMode, in.BatchTable, in.BatchColumn, in.BatchSize),
	}
	if in.Irreversible {
		reason := in.IrreversibleReason
		rev.IrreversibleReason = &reason
		rev.DownSQL = nil
		rev.SQLHash = HashRevision(in.UpSQL, "", in.ExecMode, in.BatchTable, in.BatchColumn, in.BatchSize)
	} else {
		rev.DownSQL = &down
	}
	if in.ExecMode == ModeBatch {
		table, column, size := in.BatchTable, in.BatchColumn, in.BatchSize
		rev.BatchTable = &table
		rev.BatchColumn = &column
		rev.BatchSize = &size
	}
	return rev
}

func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 200 {
		size = 200
	}
	return page, size
}
