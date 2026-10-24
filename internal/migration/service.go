package migration

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/clock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
)

// Service applies migration rules. It does not read HTTP requests.
type Service struct {
	pool  *pgxpool.Pool
	repo  Repo
	conns dbconn.Manager
	clock clock.Clock
}

// NewService wires the migration service.
func NewService(pool *pgxpool.Pool, repo Repo, conns dbconn.Manager, clk clock.Clock) *Service {
	return &Service{pool: pool, repo: repo, conns: conns, clock: clk}
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock.Now()
}

// CreateDraft stores a new migration that has not been executed.
func (s *Service) CreateDraft(ctx context.Context, p authz.Principal, in CreateInput) (int64, error) {
	if err := in.Validate(); err != nil {
		return 0, err
	}
	return s.repo.Create(ctx, s.pool, Migration{
		DatabaseID: in.DatabaseID,
		Title:      strings.TrimSpace(in.Title),
		SQLText:    strings.TrimSpace(in.SQLText),
		Status:     StatusDraft,
		CreatedBy:  p.UserID,
	})
}

// UpdateDraft changes the title and SQL of a draft or failed migration.
func (s *Service) UpdateDraft(ctx context.Context, p authz.Principal, dbID, id int64, in CreateInput) error {
	current, err := s.repo.GetByID(ctx, s.pool, dbID, id)
	if err != nil {
		return err
	}
	if err := checkEditable(current.Status); err != nil {
		return err
	}
	in.DatabaseID = dbID
	if err := in.Validate(); err != nil {
		return err
	}
	uid := p.UserID
	return s.repo.UpdateDraft(ctx, s.pool, Migration{
		ID:         id,
		DatabaseID: dbID,
		Title:      strings.TrimSpace(in.Title),
		SQLText:    strings.TrimSpace(in.SQLText),
		UpdatedBy:  &uid,
		UpdatedAt:  s.now(),
	})
}

// DeleteDraft removes a migration that has never been executed.
func (s *Service) DeleteDraft(ctx context.Context, p authz.Principal, dbID, id int64) error {
	current, err := s.repo.GetByID(ctx, s.pool, dbID, id)
	if err != nil {
		return err
	}
	if err := checkDeletable(current.Status); err != nil {
		return err
	}
	return s.repo.DeleteDraft(ctx, s.pool, dbID, id)
}

// List returns applied migrations first (by version), then drafts and failures.
func (s *Service) List(ctx context.Context, dbID int64) ([]Migration, error) {
	return s.repo.ListByDatabase(ctx, s.pool, dbID)
}

// Get returns one migration and its execution attempts. The id must belong to dbID.
func (s *Service) Get(ctx context.Context, dbID, id int64) (Migration, []Run, error) {
	m, err := s.repo.GetByID(ctx, s.pool, dbID, id)
	if err != nil {
		return Migration{}, nil, err
	}
	runs, err := s.repo.ListRuns(ctx, s.pool, dbID, id)
	if err != nil {
		return Migration{}, nil, err
	}
	return m, runs, nil
}
