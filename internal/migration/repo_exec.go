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
	return r.MarkFailedCode(ctx, execID, "", message)
}

// MarkFailedCode menutup execution sebagai gagal dan menyimpan kode mesin.
func (r *PgRepo) MarkFailedCode(ctx context.Context, execID int64, code, message string) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		UPDATE executions
		SET status = 'failed', finished_at = now(), error_code = NULLIF($2, ''), error_message = $3
		WHERE id = $1`, execID, code, message)
	if err != nil {
		return fmt.Errorf("migration.MarkFailed: %w", err)
	}
	return nil
}

// MarkUncertain menandai hasil commit yang belum bisa dipastikan. Kunci database tidak dilepas.
func (r *PgRepo) MarkUncertain(ctx context.Context, execID int64, message string) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		UPDATE executions SET status = 'uncertain', error_code = 'uncertain', error_message = $2
		WHERE id = $1`, execID, message)
	if err != nil {
		return fmt.Errorf("migration.MarkUncertain: %w", err)
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
func (r *PgRepo) InsertAttempt(ctx context.Context, execID int64, attemptNo int, workerID, status, sqlstate, message, sqlText string) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO execution_attempts (
			execution_id, attempt_no, worker_id, status, sql_executed, sqlstate, error_message, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $7, NULLIF($5, ''), NULLIF($6, ''), now(), now())`,
		execID, attemptNo, workerID, status, sqlstate, message, sqlText)
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

// Work adalah execution beserta revisi yang benar-benar akan dijalankan.
type Work struct {
	ExecutionID   int64
	DatabaseID    int64
	MigrationID   int64
	RevisionID    int64
	Kind          string
	Status        string
	ApprovalRound int
	RequestedBy   int64
	Version       *int32
	UpSQL         string
	DownSQL       *string
	SQLHash       string
	ExecMode      string
	Irreversible  bool
	BatchTable    *string
	BatchColumn   *string
	BatchSize     *int
}

// LoadWork membaca execution dan revisi yang dikunci padanya.
func (r *PgRepo) LoadWork(ctx context.Context, execID int64) (Work, error) {
	if err := r.needDB(); err != nil {
		return Work{}, err
	}
	var w Work
	err := r.db.QueryRow(ctx, `
		SELECT e.id, e.database_id, e.migration_id, e.revision_id, e.kind, e.status, e.approval_round,
		       e.requested_by, m.version, r.up_sql, r.down_sql, r.sql_hash, r.exec_mode, r.irreversible,
		       r.batch_table, r.batch_column, r.batch_size
		FROM executions e
		JOIN migration_revisions r ON r.id = e.revision_id
		JOIN migrations m ON m.id = e.migration_id
		WHERE e.id = $1`, execID).Scan(
		&w.ExecutionID, &w.DatabaseID, &w.MigrationID, &w.RevisionID, &w.Kind, &w.Status, &w.ApprovalRound,
		&w.RequestedBy, &w.Version, &w.UpSQL, &w.DownSQL, &w.SQLHash, &w.ExecMode, &w.Irreversible,
		&w.BatchTable, &w.BatchColumn, &w.BatchSize,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Work{}, ErrExecNotFound
	}
	if err != nil {
		return Work{}, fmt.Errorf("migration.LoadWork: %w", err)
	}
	return w, nil
}

// MarkApplied mencatat sukses di platform: posisi rantai untuk apply/forward, atau pengosongan untuk rollback.
func (r *PgRepo) MarkApplied(ctx context.Context, w Work, version int32) error {
	if err := r.needDB(); err != nil {
		return err
	}
	if w.Kind == KindRollback {
		if _, err := r.db.Exec(ctx, `UPDATE migrations SET version = NULL WHERE id = $1 AND database_id = $2`, w.MigrationID, w.DatabaseID); err != nil {
			return fmt.Errorf("migration.MarkApplied clear: %w", err)
		}
	} else if _, err := r.db.Exec(ctx, `
		UPDATE migrations SET version = $1 WHERE id = $2 AND database_id = $3 AND version IS NULL`,
		version, w.MigrationID, w.DatabaseID); err != nil {
		return fmt.Errorf("migration.MarkApplied version: %w", err)
	}
	_, err := r.db.Exec(ctx, `
		UPDATE executions
		SET status = 'success', finished_at = now(), version_applied = $2, error_code = NULL, error_message = NULL
		WHERE id = $1`, w.ExecutionID, version)
	if err != nil {
		return fmt.Errorf("migration.MarkApplied: %w", err)
	}
	return nil
}

// NextChainVersion adalah posisi berikutnya pada rantai aktif database.
func (r *PgRepo) NextChainVersion(ctx context.Context, databaseID int64) (int32, error) {
	if err := r.needDB(); err != nil {
		return 0, err
	}
	var max int32
	if err := r.db.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM migrations WHERE database_id = $1`, databaseID).Scan(&max); err != nil {
		return 0, fmt.Errorf("migration.NextChainVersion: %w", err)
	}
	return max + 1, nil
}

// TopVersion adalah versi teratas yang masih aktif. Nol bila rantai kosong.
func (r *PgRepo) TopVersion(ctx context.Context, databaseID int64) (int32, error) {
	if err := r.needDB(); err != nil {
		return 0, err
	}
	var top int32
	if err := r.db.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM migrations WHERE database_id = $1`, databaseID).Scan(&top); err != nil {
		return 0, fmt.Errorf("migration.TopVersion: %w", err)
	}
	return top, nil
}
