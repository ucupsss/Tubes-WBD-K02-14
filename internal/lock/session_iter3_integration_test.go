//go:build integration

package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/lock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestIter3_SameExecutionCannotBeStolenBeforeExpiry(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewPlatformDB(t)
	user := testutil.CreateUser(t, pool, "owner", false)
	dbID := testutil.CreateConnection(t, pool, "lock-same")
	execID := execution(t, pool, dbID, user)
	locks := lock.New(pool)
	lease, err := locks.Acquire(ctx, dbID, execID, "worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Acquire(ctx, dbID, execID, "worker-b", time.Minute); err != lock.ErrBusy {
		t.Fatalf("worker lain mengambil kunci yang masih hidup: %v", err)
	}
	again, err := locks.Acquire(ctx, dbID, execID, "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("pemegang yang sama harus bisa memperbarui lease: %v", err)
	}
	if err := again.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	_ = lease.Release(ctx)
}
