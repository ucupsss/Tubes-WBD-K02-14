package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// Repo is the platform-database store for migrations. The service owns transactions.
type Repo interface {
	Create(ctx context.Context, db platformdb.DBTX, m Migration) (int64, error)
	GetByID(ctx context.Context, db platformdb.DBTX, dbID, id int64) (Migration, error)
	GetByIDForUpdate(ctx context.Context, db platformdb.DBTX, dbID, id int64) (Migration, error)
	ListByDatabase(ctx context.Context, db platformdb.DBTX, dbID int64) ([]Migration, error)
	ListRuns(ctx context.Context, db platformdb.DBTX, dbID, migrationID int64) ([]Run, error)
	UpdateDraft(ctx context.Context, db platformdb.DBTX, m Migration) error
	DeleteDraft(ctx context.Context, db platformdb.DBTX, dbID, id int64) error
	MaxVersion(ctx context.Context, db platformdb.DBTX, dbID int64) (int32, error)
	SetRunning(ctx context.Context, db platformdb.DBTX, dbID, id int64) error
	MarkSuccess(ctx context.Context, db platformdb.DBTX, dbID, id int64, version int32, userID int64, started, finished time.Time, durationMS int32) error
	MarkFailed(ctx context.Context, db platformdb.DBTX, dbID, id int64, userID int64, started, finished time.Time, durationMS int32, message string) error
	InsertRun(ctx context.Context, db platformdb.DBTX, run Run) error
}

// PgRepo executes SQL against the platform database.
type PgRepo struct{}

// NewPgRepo returns a Postgres repository.
func NewPgRepo() *PgRepo { return &PgRepo{} }

func (r *PgRepo) Create(ctx context.Context, db platformdb.DBTX, m Migration) (int64, error) {
	const q = `
		INSERT INTO migrations (database_id, title, sql_text, status, created_by)
		VALUES ($1, $2, $3, 'draft', $4)
		RETURNING id`
	var id int64
	err := db.QueryRow(ctx, q, m.DatabaseID, m.Title, m.SQLText, m.CreatedBy).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("migration.Create: %w", err)
	}
	return id, nil
}

func (r *PgRepo) GetByID(ctx context.Context, db platformdb.DBTX, dbID, id int64) (Migration, error) {
	const q = `
		SELECT m.id, m.database_id, m.version, m.title, m.sql_text, m.status,
		       m.created_by, cu.username, m.created_at,
		       m.updated_by, m.updated_at,
		       m.executed_by, COALESCE(eu.username, ''), m.executed_at,
		       m.finished_at, m.duration_ms, m.error_message
		FROM migrations m
		JOIN users cu ON cu.id = m.created_by
		LEFT JOIN users eu ON eu.id = m.executed_by
		WHERE m.id = $1 AND m.database_id = $2`
	return scanMigration(db.QueryRow(ctx, q, id, dbID))
}

func (r *PgRepo) GetByIDForUpdate(ctx context.Context, db platformdb.DBTX, dbID, id int64) (Migration, error) {
	const q = `
		SELECT m.id, m.database_id, m.version, m.title, m.sql_text, m.status,
		       m.created_by, cu.username, m.created_at,
		       m.updated_by, m.updated_at,
		       m.executed_by, COALESCE(eu.username, ''), m.executed_at,
		       m.finished_at, m.duration_ms, m.error_message
		FROM migrations m
		JOIN users cu ON cu.id = m.created_by
		LEFT JOIN users eu ON eu.id = m.executed_by
		WHERE m.id = $1 AND m.database_id = $2
		FOR UPDATE OF m`
	m, err := scanMigration(db.QueryRow(ctx, q, id, dbID))
	if err != nil {
		return Migration{}, err
	}
	return m, nil
}

func (r *PgRepo) ListByDatabase(ctx context.Context, db platformdb.DBTX, dbID int64) ([]Migration, error) {
	const q = `
		SELECT m.id, m.database_id, m.version, m.title, m.sql_text, m.status,
		       m.created_by, cu.username, m.created_at,
		       m.updated_by, m.updated_at,
		       m.executed_by, COALESCE(eu.username, ''), m.executed_at,
		       m.finished_at, m.duration_ms, m.error_message
		FROM migrations m
		JOIN users cu ON cu.id = m.created_by
		LEFT JOIN users eu ON eu.id = m.executed_by
		WHERE m.database_id = $1
		ORDER BY CASE WHEN m.status = 'success' THEN 0 ELSE 1 END,
		         m.version ASC NULLS LAST,
		         m.id ASC`
	rows, err := db.Query(ctx, q, dbID)
	if err != nil {
		return nil, fmt.Errorf("migration.ListByDatabase: %w", err)
	}
	defer rows.Close()

	var list []Migration
	for rows.Next() {
		m, err := scanMigration(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migration.ListByDatabase: %w", err)
	}
	return list, nil
}

func (r *PgRepo) ListRuns(ctx context.Context, db platformdb.DBTX, dbID, migrationID int64) ([]Run, error) {
	const q = `
		SELECT r.id, r.migration_id, r.executed_by, u.username, r.sql_snapshot, r.status,
		       r.error_message, r.started_at, r.finished_at
		FROM migration_runs r
		JOIN migrations m ON m.id = r.migration_id
		JOIN users u ON u.id = r.executed_by
		WHERE r.migration_id = $1 AND m.database_id = $2
		ORDER BY r.started_at ASC, r.id ASC`
	rows, err := db.Query(ctx, q, migrationID, dbID)
	if err != nil {
		return nil, fmt.Errorf("migration.ListRuns: %w", err)
	}
	defer rows.Close()

	var list []Run
	for rows.Next() {
		var run Run
		if err := rows.Scan(
			&run.ID, &run.MigrationID, &run.ExecutedBy, &run.ExecutedByUsername,
			&run.SQLSnapshot, &run.Status, &run.ErrorMessage, &run.StartedAt, &run.FinishedAt,
		); err != nil {
			return nil, fmt.Errorf("migration.ListRuns: %w", err)
		}
		list = append(list, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migration.ListRuns: %w", err)
	}
	return list, nil
}

func (r *PgRepo) UpdateDraft(ctx context.Context, db platformdb.DBTX, m Migration) error {
	const q = `
		UPDATE migrations
		SET title = $1, sql_text = $2, updated_by = $3, updated_at = $4, error_message = NULL
		WHERE id = $5 AND database_id = $6 AND status IN ('draft', 'failed')`
	tag, err := db.Exec(ctx, q, m.Title, m.SQLText, m.UpdatedBy, m.UpdatedAt, m.ID, m.DatabaseID)
	if err != nil {
		return fmt.Errorf("migration.UpdateDraft: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PgRepo) DeleteDraft(ctx context.Context, db platformdb.DBTX, dbID, id int64) error {
	const q = `
		DELETE FROM migrations
		WHERE id = $1 AND database_id = $2 AND status = 'draft'`
	tag, err := db.Exec(ctx, q, id, dbID)
	if err != nil {
		return fmt.Errorf("migration.DeleteDraft: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PgRepo) MaxVersion(ctx context.Context, db platformdb.DBTX, dbID int64) (int32, error) {
	const q = `
		SELECT COALESCE(MAX(version), 0)
		FROM migrations
		WHERE database_id = $1`
	var max int32
	if err := db.QueryRow(ctx, q, dbID).Scan(&max); err != nil {
		return 0, fmt.Errorf("migration.MaxVersion: %w", err)
	}
	return max, nil
}

func (r *PgRepo) SetRunning(ctx context.Context, db platformdb.DBTX, dbID, id int64) error {
	const q = `
		UPDATE migrations
		SET status = 'running', updated_at = now()
		WHERE id = $1 AND database_id = $2 AND status IN ('draft', 'failed')`
	tag, err := db.Exec(ctx, q, id, dbID)
	if err != nil {
		return fmt.Errorf("migration.SetRunning: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotExecutable
	}
	return nil
}

func (r *PgRepo) MarkSuccess(ctx context.Context, db platformdb.DBTX, dbID, id int64, version int32, userID int64, started, finished time.Time, durationMS int32) error {
	const q = `
		UPDATE migrations
		SET version = $1, status = 'success', executed_by = $2, executed_at = $3,
		    finished_at = $4, duration_ms = $5, error_message = NULL,
		    updated_by = $2, updated_at = $4
		WHERE id = $6 AND database_id = $7 AND status = 'running'`
	tag, err := db.Exec(ctx, q, version, userID, started, finished, durationMS, id, dbID)
	if err != nil {
		return fmt.Errorf("migration.MarkSuccess: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotExecutable
	}
	return nil
}

func (r *PgRepo) MarkFailed(ctx context.Context, db platformdb.DBTX, dbID, id int64, userID int64, started, finished time.Time, durationMS int32, message string) error {
	const q = `
		UPDATE migrations
		SET status = 'failed', executed_by = $1, executed_at = $2, finished_at = $3,
		    duration_ms = $4, error_message = $5, updated_by = $1, updated_at = $3
		WHERE id = $6 AND database_id = $7 AND status = 'running'`
	tag, err := db.Exec(ctx, q, userID, started, finished, durationMS, message, id, dbID)
	if err != nil {
		return fmt.Errorf("migration.MarkFailed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotExecutable
	}
	return nil
}

func (r *PgRepo) InsertRun(ctx context.Context, db platformdb.DBTX, run Run) error {
	const q = `
		INSERT INTO migration_runs (
			migration_id, executed_by, sql_snapshot, status, error_message, started_at, finished_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := db.Exec(ctx, q,
		run.MigrationID, run.ExecutedBy, run.SQLSnapshot, run.Status,
		run.ErrorMessage, run.StartedAt, run.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("migration.InsertRun: %w", err)
	}
	return nil
}

func scanMigration(row pgx.Row) (Migration, error) {
	var m Migration
	err := row.Scan(
		&m.ID, &m.DatabaseID, &m.Version, &m.Title, &m.SQLText, &m.Status,
		&m.CreatedBy, &m.CreatedByUsername, &m.CreatedAt,
		&m.UpdatedBy, &m.UpdatedAt,
		&m.ExecutedBy, &m.ExecutedByUsername, &m.ExecutedAt,
		&m.FinishedAt, &m.DurationMS, &m.ErrorMessage,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, ErrNotFound
	}
	if err != nil {
		return Migration{}, fmt.Errorf("migration.scan: %w", err)
	}
	return m, nil
}
