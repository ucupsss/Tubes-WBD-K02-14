package migration

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// ExecutionStatus mengembalikan status execution.
func (r *PgRepo) ExecutionStatus(ctx context.Context, execID int64) (string, error) {
	if err := r.needDB(); err != nil {
		return "", err
	}
	return r.executionStatus(ctx, r.db, execID)
}

func (r *PgRepo) needDB() error {
	if r.db == nil {
		return fmt.Errorf("migration: repo tidak terhubung ke database")
	}
	return nil
}

func (r *PgRepo) executionStatus(ctx context.Context, db platformdb.DBTX, execID int64) (string, error) {
	var status string
	err := db.QueryRow(ctx, `SELECT status FROM executions WHERE id = $1`, execID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrExecNotFound
	}
	if err != nil {
		return "", fmt.Errorf("migration.ExecutionStatus: %w", err)
	}
	return status, nil
}

// MarkRunning mengubah queued menjadi running. false bila status sudah berubah.
func (r *PgRepo) MarkRunning(ctx context.Context, execID int64) (bool, error) {
	if err := r.needDB(); err != nil {
		return false, err
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE executions
		SET status = 'running', started_at = COALESCE(started_at, now())
		WHERE id = $1 AND status = 'queued'`, execID)
	if err != nil {
		return false, fmt.Errorf("migration.MarkRunning: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkQueued mengembalikan execution ke antrian setelah retry.
func (r *PgRepo) MarkQueued(ctx context.Context, execID int64) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `UPDATE executions SET status = 'queued' WHERE id = $1`, execID)
	if err != nil {
		return fmt.Errorf("migration.MarkQueued: %w", err)
	}
	return nil
}

// MarkFailed menutup execution sebagai gagal.
func (r *PgRepo) MarkFailed(ctx context.Context, execID int64, message string) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		UPDATE executions
		SET status = 'failed', finished_at = now(), error_message = $2
		WHERE id = $1`, execID, message)
	if err != nil {
		return fmt.Errorf("migration.MarkFailed: %w", err)
	}
	return nil
}

// MarkSucceeded menutup execution sebagai sukses. Posisi rantai diisi worker eksekusi nanti.
func (r *PgRepo) MarkSucceeded(ctx context.Context, execID int64) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		UPDATE executions SET status = 'success', finished_at = now() WHERE id = $1`, execID)
	if err != nil {
		return fmt.Errorf("migration.MarkSucceeded: %w", err)
	}
	return nil
}

// InsertAttempt mencatat satu percobaan teknis. SQL target belum diisi pada paket antrian.
func (r *PgRepo) InsertAttempt(ctx context.Context, execID int64, attemptNo int, workerID, status, sqlstate, message string) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO execution_attempts (
			execution_id, attempt_no, worker_id, status, sql_executed, sqlstate, error_message, started_at, finished_at)
		VALUES ($1, $2, $3, $4, '', NULLIF($5, ''), NULLIF($6, ''), now(), now())`,
		execID, attemptNo, workerID, status, sqlstate, message)
	if err != nil {
		return fmt.Errorf("migration.InsertAttempt: %w", err)
	}
	return nil
}

// LockExecution mengunci satu execution dan mengembalikan database serta statusnya.
func (r *PgRepo) LockExecution(ctx context.Context, db platformdb.DBTX, execID int64) (databaseID int64, status string, requestedBy int64, err error) {
	err = db.QueryRow(ctx, `
		SELECT database_id, status, requested_by FROM executions WHERE id = $1 FOR UPDATE`, execID).
		Scan(&databaseID, &status, &requestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", 0, ErrExecNotFound
	}
	if err != nil {
		return 0, "", 0, fmt.Errorf("migration.LockExecution: %w", err)
	}
	return databaseID, status, requestedBy, nil
}

// SetQueued menandai execution masuk antrian.
func (r *PgRepo) SetQueued(ctx context.Context, db platformdb.DBTX, execID int64) error {
	tag, err := db.Exec(ctx, `UPDATE executions SET status = 'queued' WHERE id = $1`, execID)
	if err != nil {
		return fmt.Errorf("migration.SetQueued: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrExecNotFound
	}
	return nil
}

// SetCancelled menutup execution yang belum berjalan.
func (r *PgRepo) SetCancelled(ctx context.Context, db platformdb.DBTX, execID int64) error {
	tag, err := db.Exec(ctx, `
		UPDATE executions SET status = 'cancelled', finished_at = now() WHERE id = $1`, execID)
	if err != nil {
		return fmt.Errorf("migration.SetCancelled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrExecNotFound
	}
	return nil
}

// BlocksDatabase melaporkan ada execution lain yang menahan database (D38).
func (r *PgRepo) BlocksDatabase(ctx context.Context, db platformdb.DBTX, databaseID, exceptID int64) (bool, error) {
	var blocked bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM executions
			WHERE database_id = $1 AND id <> $2
			  AND status IN ('running', 'partial', 'partially_reverted', 'uncertain'))`,
		databaseID, exceptID).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("migration.BlocksDatabase: %w", err)
	}
	return blocked, nil
}
