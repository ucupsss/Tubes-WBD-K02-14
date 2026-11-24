package lock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

const (
	// DefaultLease adalah masa berlaku kunci bila worker berhenti memperpanjang.
	DefaultLease = 30 * time.Second
	// HeartbeatEvery adalah jarak perpanjangan lease oleh pemegang kunci.
	HeartbeatEvery = 10 * time.Second
)

// ErrBusy berarti database masih dikunci execution lain yang lease-nya hidup.
var ErrBusy = apperr.Conflict("Database sedang menjalankan migration lain").WithReason("lock_busy")

// Locker mengambil dan melepas kunci per database.
type Locker interface {
	Acquire(ctx context.Context, dbID, execID int64, workerID string, lease time.Duration) (Lease, error)
}

// Lease adalah kunci yang sedang dipegang. Heartbeat memperpanjangnya. Release melepasnya.
type Lease interface {
	Heartbeat(ctx context.Context) error
	Release(ctx context.Context) error
	DatabaseID() int64
	ExecutionID() int64
}

// PgLocker menyimpan lease di tabel database_locks.
type PgLocker struct {
	pool *pgxpool.Pool
}

// New membuat locker di atas tabel database_locks.
func New(pool *pgxpool.Pool) *PgLocker { return &PgLocker{pool: pool} }

type pgLease struct {
	pool     *pgxpool.Pool
	dbID     int64
	execID   int64
	workerID string
	lease    time.Duration
}

// DatabaseID mengembalikan database yang dikunci.
func (l *pgLease) DatabaseID() int64 { return l.dbID }

// ExecutionID mengembalikan execution pemegang kunci.
func (l *pgLease) ExecutionID() int64 { return l.execID }

// Acquire mengambil kunci. Execution yang sama boleh memperbarui lease-nya.
// Worker lain hanya bisa mengambil alih setelah lease kedaluwarsa.
func (l *PgLocker) Acquire(ctx context.Context, dbID, execID int64, workerID string, lease time.Duration) (Lease, error) {
	if lease <= 0 {
		lease = DefaultLease
	}
	var got int64
	err := platformdb.WithTx(ctx, l.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO database_locks (database_id, execution_id, owner_worker, lease_expires_at)
			VALUES ($1, $2, $3, now() + make_interval(secs => $4))
			ON CONFLICT (database_id) DO UPDATE
			SET execution_id = EXCLUDED.execution_id,
			    owner_worker = EXCLUDED.owner_worker,
			    lease_expires_at = EXCLUDED.lease_expires_at,
			    acquired_at = now()
			WHERE database_locks.lease_expires_at <= now()
			   OR (database_locks.execution_id = EXCLUDED.execution_id
			       AND database_locks.owner_worker = EXCLUDED.owner_worker)
			RETURNING database_id`, dbID, execID, workerID, lease.Seconds()).Scan(&got)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBusy
		}
		return err
	})
	if err != nil {
		if errors.Is(err, ErrBusy) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("lock.Acquire: %w", err)
	}
	return &pgLease{pool: l.pool, dbID: dbID, execID: execID, workerID: workerID, lease: lease}, nil
}

// Heartbeat memperpanjang lease kunci ini. ErrBusy bila pemegangnya sudah berganti.
func (l *pgLease) Heartbeat(ctx context.Context) error {
	tag, err := l.pool.Exec(ctx, `
		UPDATE database_locks
		SET lease_expires_at = now() + make_interval(secs => $4)
		WHERE database_id = $1 AND execution_id = $2 AND owner_worker = $3`,
		l.dbID, l.execID, l.workerID, l.lease.Seconds())
	if err != nil {
		return fmt.Errorf("lock.Heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBusy
	}
	return nil
}

// Release melepas kunci bila masih dipegang worker yang sama.
func (l *pgLease) Release(ctx context.Context) error {
	_, err := l.pool.Exec(ctx, `
		DELETE FROM database_locks
		WHERE database_id = $1 AND execution_id = $2 AND owner_worker = $3`,
		l.dbID, l.execID, l.workerID)
	if err != nil {
		return fmt.Errorf("lock.Release: %w", err)
	}
	return nil
}

// Held melaporkan apakah execution masih memegang kunci yang belum kedaluwarsa.
func (l *PgLocker) Held(ctx context.Context, dbID, execID int64) (bool, error) {
	var held bool
	err := l.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM database_locks
			WHERE database_id = $1 AND execution_id = $2 AND lease_expires_at > now())`,
		dbID, execID).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("lock.Held: %w", err)
	}
	return held, nil
}
