//go:build integration

package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/lock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestLock_OneDatabaseAtATimeAndExpiredTakeover(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewPlatformDB(t)
	user := testutil.CreateUser(t, pool, "locker", false)
	db1 := testutil.CreateConnection(t, pool, "lock-a")
	db2 := testutil.CreateConnection(t, pool, "lock-b")
	e1 := execution(t, pool, db1, user)
	e2 := execution(t, pool, db1, user)
	e3 := execution(t, pool, db2, user)
	locks := lock.New(pool)

	if _, err := locks.Acquire(ctx, db1, e1, "worker-a", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Acquire(ctx, db1, e2, "worker-b", time.Minute); err != lock.ErrBusy {
		t.Fatalf("kunci kedua = %v", err)
	}
	other, err := locks.Acquire(ctx, db2, e3, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("database lain harus jalan: %v", err)
	}
	if err := other.Release(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `UPDATE database_locks SET lease_expires_at = now() - interval '1 minute' WHERE database_id = $1`, db1); err != nil {
		t.Fatal(err)
	}
	taken, err := locks.Acquire(ctx, db1, e2, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("lease kedaluwarsa harus bisa diambil: %v", err)
	}
	if err := taken.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	held, err := locks.Held(ctx, db1, e2)
	if err != nil || !held {
		t.Fatalf("held = %v %v", held, err)
	}
	if err := taken.Release(ctx); err != nil {
		t.Fatal(err)
	}
	held, err = locks.Held(ctx, db1, e2)
	if err != nil || held {
		t.Fatalf("setelah release held = %v", held)
	}
}

func execution(t *testing.T, pool *pgxpool.Pool, dbID, userID int64) int64 {
	t.Helper()
	mig := testutil.CreateDraft(t, pool, dbID, userID, "SELECT 1")
	var rev int64
	if err := pool.QueryRow(context.Background(), `SELECT current_revision_id FROM migrations WHERE id = $1`, mig).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO executions (database_id, migration_id, revision_id, kind, status, requested_by)
		VALUES ($1, $2, $3, 'apply', 'queued', $4)
		RETURNING id`, dbID, mig, rev, userID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
