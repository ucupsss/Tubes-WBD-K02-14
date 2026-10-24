package migration

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// ExecuteResult describes a finished attempt. Failed is set when the target
// database rejected the script or could not be reached; the platform row is
// already saved as failed in that case.
type ExecuteResult struct {
	Failed  bool
	Message string
}

// Execute runs the migration inside a target transaction and records the outcome.
// Versions are assigned only after a successful commit, and only one execution
// per database proceeds at a time.
func (s *Service) Execute(ctx context.Context, p authz.Principal, dbID, id int64) (ExecuteResult, error) {
	current, err := s.repo.GetByID(ctx, s.pool, dbID, id)
	if err != nil {
		return ExecuteResult{}, err
	}
	if err := checkExecutable(current.Status); err != nil {
		return ExecuteResult{}, err
	}
	if err := validateSQL(current.SQLText); err != nil {
		return ExecuteResult{}, err
	}

	var result ExecuteResult
	err = platformdb.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockConnection(ctx, tx, dbID); err != nil {
			return err
		}
		m, err := s.repo.GetByIDForUpdate(ctx, tx, dbID, id)
		if err != nil {
			return err
		}
		if err := checkExecutable(m.Status); err != nil {
			return err
		}
		if err := validateSQL(m.SQLText); err != nil {
			return err
		}
		if err := s.repo.SetRunning(ctx, tx, dbID, id); err != nil {
			return err
		}

		started := s.now()
		target, poolErr := s.conns.Pool(ctx, dbID)
		if poolErr != nil {
			msg := "Database tidak dapat dihubungi"
			slog.Warn("migration target unavailable",
				"user_id", p.UserID,
				"db_id", dbID,
				"migration_id", id,
			)
			if err := s.finishFailed(ctx, tx, m, p, started, msg); err != nil {
				return err
			}
			result = ExecuteResult{Failed: true, Message: msg}
			return nil
		}

		maxVersion, err := s.repo.MaxVersion(ctx, tx, dbID)
		if err != nil {
			return err
		}
		version := NextVersion(maxVersion)
		applyErr := applyTarget(ctx, target, m, p, version, s.now())
		if applyErr != nil {
			msg := safeSQLError(applyErr)
			slog.Info("migration failed",
				"user_id", p.UserID,
				"db_id", dbID,
				"migration_id", id,
				"status", "failed",
			)
			if err := s.finishFailed(ctx, tx, m, p, started, msg); err != nil {
				return err
			}
			result = ExecuteResult{Failed: true, Message: msg}
			return nil
		}

		finished := s.now()
		ms := durationMS(started, finished)
		if err := s.repo.MarkSuccess(ctx, tx, dbID, id, version, p.UserID, started, finished, ms); err != nil {
			return err
		}
		if err := s.repo.InsertRun(ctx, tx, Run{
			MigrationID: id,
			ExecutedBy:  p.UserID,
			SQLSnapshot: m.SQLText,
			Status:      StatusSuccess,
			StartedAt:   started,
			FinishedAt:  finished,
		}); err != nil {
			return err
		}
		slog.Info("migration executed",
			"user_id", p.UserID,
			"db_id", dbID,
			"migration_id", id,
			"version", version,
			"duration_ms", ms,
			"status", "success",
		)
		return nil
	})
	if err != nil {
		return ExecuteResult{}, err
	}
	return result, nil
}

func (s *Service) finishFailed(ctx context.Context, tx pgx.Tx, m Migration, p authz.Principal, started time.Time, message string) error {
	finished := s.now()
	ms := durationMS(started, finished)
	if err := s.repo.MarkFailed(ctx, tx, m.DatabaseID, m.ID, p.UserID, started, finished, ms, message); err != nil {
		return err
	}
	return s.repo.InsertRun(ctx, tx, Run{
		MigrationID:  m.ID,
		ExecutedBy:   p.UserID,
		SQLSnapshot:  m.SQLText,
		Status:       StatusFailed,
		ErrorMessage: &message,
		StartedAt:    started,
		FinishedAt:   finished,
	})
}

func lockConnection(ctx context.Context, tx pgx.Tx, dbID int64) error {
	const q = `SELECT id FROM db_connections WHERE id = $1 FOR UPDATE`
	var id int64
	err := tx.QueryRow(ctx, q, dbID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("Database tidak ditemukan")
	}
	if err != nil {
		return err
	}
	return nil
}

func applyTarget(ctx context.Context, pool interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}, m Migration, p authz.Principal, version int32, appliedAt time.Time) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	const setTimeout = `SET LOCAL statement_timeout = '60s'`
	const setLock = `SET LOCAL lock_timeout = '5s'`
	const createSchema = `CREATE SCHEMA IF NOT EXISTS _dbmp`
	const createTable = `
		CREATE TABLE IF NOT EXISTS _dbmp.applied_migrations (
			version INT PRIMARY KEY,
			migration_id BIGINT NOT NULL,
			applied_by BIGINT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL
		)`
	for _, stmt := range []string{setTimeout, setLock, createSchema, createTable} {
		if _, err = tx.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	if err = execSimple(ctx, tx, m.SQLText); err != nil {
		return err
	}
	const insertApplied = `
		INSERT INTO _dbmp.applied_migrations (version, migration_id, applied_by, applied_at)
		VALUES ($1, $2, $3, $4)`
	if _, err = tx.Exec(ctx, insertApplied, version, m.ID, p.UserID, appliedAt); err != nil {
		return err
	}
	err = tx.Commit(ctx)
	return err
}

func execSimple(ctx context.Context, tx pgx.Tx, sql string) error {
	_, err := tx.Conn().PgConn().Exec(ctx, sql).ReadAll()
	return err
}

func safeSQLError(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Message != "" {
		return pgErr.Message
	}
	if errors.Is(err, dbconn.ErrUnavailable) {
		return "Database tidak dapat dihubungi"
	}
	return "Migration gagal dijalankan"
}

func durationMS(start, end time.Time) int32 {
	ms := end.Sub(start).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	if ms > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(ms)
}
