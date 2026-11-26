package migration

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/telemetry"
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

// InsertAttempt mencatat satu percobaan teknis beserta trace_id aktif bila ada.
func (r *PgRepo) InsertAttempt(ctx context.Context, execID int64, attemptNo int, workerID, status, sqlstate, message, sqlText string) error {
	if err := r.needDB(); err != nil {
		return err
	}
	traceID, _ := telemetry.ExtractTraceIDs(ctx)
	_, err := r.db.Exec(ctx, `
		INSERT INTO execution_attempts (
			execution_id, attempt_no, worker_id, status, sql_executed, sqlstate, error_message, started_at, finished_at, trace_id)
		VALUES ($1, $2, $3, $4, $7, NULLIF($5, ''), NULLIF($6, ''), now(), now(), NULLIF($8, ''))`,
		execID, attemptNo, workerID, status, sqlstate, message, sqlText, traceID)
	if err != nil {
		return fmt.Errorf("migration.InsertAttempt: %w", err)
	}
	return nil
}

// UpdateBatchProgress memperbarui jumlah batch dan baris yang sudah di-commit pada executions.
func (r *PgRepo) UpdateBatchProgress(ctx context.Context, execID int64, batches int, rows int64) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		UPDATE executions
		SET batches_committed = $2, rows_committed = $3
		WHERE id = $1`, execID, batches, rows)
	if err != nil {
		return fmt.Errorf("migration.UpdateBatchProgress: %w", err)
	}
	return nil
}

// MarkPartial menandai execution berhenti sebagian (partial atau partially_reverted) beserta jumlah batch/baris terbaru.
func (r *PgRepo) MarkPartial(ctx context.Context, execID int64, status, code, message string, batches int, rows int64) error {
	if err := r.needDB(); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		UPDATE executions
		SET status = $2, batches_committed = $3, rows_committed = $4,
		    error_code = NULLIF($5, ''), error_message = $6
		WHERE id = $1`, execID, status, batches, rows, code, message)
	if err != nil {
		return fmt.Errorf("migration.MarkPartial: %w", err)
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

// BlocksDatabase melaporkan ada execution lain yang menahan database (D38). Execution partial yang
// sedang dibatalkan oleh exceptID sendiri tidak dihitung, begitu pula partial yang sudah di-revert sukses.
func (r *PgRepo) BlocksDatabase(ctx context.Context, db platformdb.DBTX, databaseID, exceptID int64) (bool, error) {
	var blocked bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM executions e
			WHERE e.database_id = $1 AND e.id <> $2
			  AND e.id <> COALESCE((SELECT rolls_back_execution_id FROM executions WHERE id = $2), 0)
			  AND e.status IN ('running', 'partial', 'partially_reverted', 'uncertain')
			  AND NOT `+revertedPartial+`)`,
		databaseID, exceptID).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("migration.BlocksDatabase: %w", err)
	}
	return blocked, nil
}

// RevertsPartial: rollback ini membatalkan batch committed dari execution yang berhenti partial,
// bukan migration aktif di rantai. Posisi rantai tidak berubah karena migration itu belum pernah aktif.
func (w Work) RevertsPartial() bool {
	return w.Kind == KindRollback && w.SourceStatus != nil && *w.SourceStatus == StatusPartial
}

// HasActiveRevert melaporkan ada rollback yang sudah atau sedang membatalkan execution partial ini.
// Rollback yang ditolak, dibatalkan, atau gagal tanpa mengubah apa pun tidak dihitung.
func (r *PgRepo) HasActiveRevert(ctx context.Context, db platformdb.DBTX, execID int64) (bool, error) {
	var found bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM executions
			WHERE rolls_back_execution_id = $1 AND status NOT IN ('rejected', 'cancelled', 'failed'))`, execID).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("migration.HasActiveRevert: %w", err)
	}
	return found, nil
}

// Work adalah execution beserta revisi yang benar-benar akan dijalankan.
type Work struct {
	ExecutionID          int64
	DatabaseID           int64
	MigrationID          int64
	RevisionID           int64
	Kind                 string
	Status               string
	ApprovalRound        int
	RequestedBy          int64
	RollsBackExecutionID *int64
	// SourceStatus adalah status execution yang dibatalkan (hanya untuk rollback).
	SourceStatus *string
	Version      *int32
	UpSQL        string
	DownSQL      *string
	SQLHash      string
	ExecMode     string
	Irreversible bool
	BatchTable   *string
	BatchColumn  *string
	BatchSize    *int
}

// LoadWork membaca execution dan revisi yang dikunci padanya.
func (r *PgRepo) LoadWork(ctx context.Context, execID int64) (Work, error) {
	if err := r.needDB(); err != nil {
		return Work{}, err
	}
	var w Work
	err := r.db.QueryRow(ctx, `
		SELECT e.id, e.database_id, e.migration_id, e.revision_id, e.kind, e.status, e.approval_round,
		       e.requested_by, e.rolls_back_execution_id, src.status, m.version, r.up_sql, r.down_sql, r.sql_hash, r.exec_mode, r.irreversible,
		       r.batch_table, r.batch_column, r.batch_size
		FROM executions e
		JOIN migration_revisions r ON r.id = e.revision_id
		JOIN migrations m ON m.id = e.migration_id
		LEFT JOIN executions src ON src.id = e.rolls_back_execution_id
		WHERE e.id = $1`, execID).Scan(
		&w.ExecutionID, &w.DatabaseID, &w.MigrationID, &w.RevisionID, &w.Kind, &w.Status, &w.ApprovalRound,
		&w.RequestedBy, &w.RollsBackExecutionID, &w.SourceStatus, &w.Version, &w.UpSQL, &w.DownSQL, &w.SQLHash, &w.ExecMode, &w.Irreversible,
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

// MarkApplied mencatat sukses di platform: posisi rantai dan status execution dalam satu pernyataan,
// supaya kegagalan di salah satunya tidak meninggalkan version terisi sementara execution gagal.
func (r *PgRepo) MarkApplied(ctx context.Context, w Work, version int32) error {
	if err := r.needDB(); err != nil {
		return err
	}
	sqlText := `
		WITH chain AS (
			UPDATE migrations SET version = $4
			WHERE id = $2 AND database_id = $3 AND (version IS NULL OR version = $4)
			RETURNING id
		)
		UPDATE executions
		SET status = 'success', finished_at = now(), version_applied = $4,
		    error_code = NULL, error_message = NULL
		WHERE id = $1 AND EXISTS (SELECT 1 FROM chain)`
	if w.Kind == KindRollback {
		sqlText = `
			WITH chain AS (
				UPDATE migrations SET version = NULL
				WHERE id = $2 AND database_id = $3
				RETURNING id
			)
			UPDATE executions
			SET status = 'success', finished_at = now(), version_applied = NULLIF($4, 0),
			    error_code = NULL, error_message = NULL
			WHERE id = $1 AND EXISTS (SELECT 1 FROM chain)`
	}
	tag, err := r.db.Exec(ctx, sqlText, w.ExecutionID, w.MigrationID, w.DatabaseID, version)
	if err != nil {
		return fmt.Errorf("migration.MarkApplied: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("migration.MarkApplied: posisi rantai tidak dapat dicatat")
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
