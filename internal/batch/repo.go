package batch

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

const ensureTargetSchemaSQL = `
CREATE SCHEMA IF NOT EXISTS _dbmp;
CREATE TABLE IF NOT EXISTS _dbmp.batch_checkpoints (
  execution_id  BIGINT      NOT NULL,
  direction     TEXT        NOT NULL,
  batch_no      INT         NOT NULL,
  from_value    BIGINT      NOT NULL,
  to_value      BIGINT      NOT NULL,
  rows_affected BIGINT      NOT NULL,
  committed_at  TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (execution_id, direction, batch_no)
);
`

// EnsureTargetSchema memastikan skema _dbmp dan tabel _dbmp.batch_checkpoints tersedia di database target.
func EnsureTargetSchema(ctx context.Context, db platformdb.DBTX) error {
	if _, err := db.Exec(ctx, ensureTargetSchemaSQL); err != nil {
		return fmt.Errorf("batch.EnsureTargetSchema: %w", err)
	}
	return nil
}

// ListTargetCheckpoints membaca seluruh checkpoint untuk executionID dan direction dari _dbmp.batch_checkpoints
// di database target, diurutkan berdasarkan batch_no ASC.
func ListTargetCheckpoints(ctx context.Context, db platformdb.DBTX, executionID int64, direction string) ([]Checkpoint, error) {
	rows, err := db.Query(ctx, `
		SELECT execution_id, direction, batch_no, from_value, to_value, rows_affected, committed_at
		FROM _dbmp.batch_checkpoints
		WHERE execution_id = $1 AND direction = $2
		ORDER BY batch_no ASC`,
		executionID, direction,
	)
	if err != nil {
		return nil, fmt.Errorf("batch.ListTargetCheckpoints: %w", err)
	}
	defer rows.Close()

	var list []Checkpoint
	for rows.Next() {
		var cp Checkpoint
		if err := rows.Scan(
			&cp.ExecutionID, &cp.Direction, &cp.BatchNo,
			&cp.FromValue, &cp.ToValue, &cp.RowsAffected, &cp.CommittedAt,
		); err != nil {
			return nil, fmt.Errorf("batch.ListTargetCheckpoints scan: %w", err)
		}
		list = append(list, cp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("batch.ListTargetCheckpoints rows: %w", err)
	}
	return list, nil
}

// insertTargetCheckpoint menyimpan satu bukti batch di dalam transaksi database target yang sama.
func insertTargetCheckpoint(ctx context.Context, tx pgx.Tx, cp Checkpoint) error {
	if cp.CommittedAt.IsZero() {
		cp.CommittedAt = time.Now().UTC()
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO _dbmp.batch_checkpoints (
			execution_id, direction, batch_no, from_value, to_value, rows_affected, committed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		cp.ExecutionID, cp.Direction, cp.BatchNo, cp.FromValue, cp.ToValue, cp.RowsAffected, cp.CommittedAt,
	)
	if err != nil {
		return fmt.Errorf("batch.insertTargetCheckpoint: %w", err)
	}
	return nil
}

// MirrorRepo mengelola pencerminan checkpoint batch ke tabel execution_batches di database platform.
type MirrorRepo struct {
	db platformdb.DBTX
}

// NewMirrorRepo membuat MirrorRepo untuk database platform.
func NewMirrorRepo(db platformdb.DBTX) *MirrorRepo {
	return &MirrorRepo{db: db}
}

// RecordBatch mencatat (upsert) satu checkpoint batch ke tabel execution_batches di database platform.
func (r *MirrorRepo) RecordBatch(ctx context.Context, cp Checkpoint) error {
	if cp.CommittedAt.IsZero() {
		cp.CommittedAt = time.Now().UTC()
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO execution_batches (
			execution_id, direction, batch_no, from_value, to_value, rows_affected, committed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (execution_id, direction, batch_no) DO UPDATE SET
			from_value = EXCLUDED.from_value,
			to_value = EXCLUDED.to_value,
			rows_affected = EXCLUDED.rows_affected,
			committed_at = EXCLUDED.committed_at`,
		cp.ExecutionID, cp.Direction, cp.BatchNo, cp.FromValue, cp.ToValue, cp.RowsAffected, cp.CommittedAt,
	)
	if err != nil {
		return fmt.Errorf("batch.MirrorRepo.RecordBatch: %w", err)
	}
	return nil
}

// ListByExecution mengambil seluruh catatan batch dari tabel execution_batches di database platform
// untuk ditampilkan di halaman detail execution SPA.
func (r *MirrorRepo) ListByExecution(ctx context.Context, executionID int64) ([]Checkpoint, error) {
	rows, err := r.db.Query(ctx, `
		SELECT execution_id, direction, batch_no, from_value, to_value, rows_affected, committed_at
		FROM execution_batches
		WHERE execution_id = $1
		ORDER BY direction ASC, batch_no ASC`,
		executionID,
	)
	if err != nil {
		return nil, fmt.Errorf("batch.MirrorRepo.ListByExecution: %w", err)
	}
	defer rows.Close()

	var list []Checkpoint
	for rows.Next() {
		var cp Checkpoint
		if err := rows.Scan(
			&cp.ExecutionID, &cp.Direction, &cp.BatchNo,
			&cp.FromValue, &cp.ToValue, &cp.RowsAffected, &cp.CommittedAt,
		); err != nil {
			return nil, fmt.Errorf("batch.MirrorRepo.ListByExecution scan: %w", err)
		}
		list = append(list, cp)
	}
	return list, rows.Err()
}
