//go:build integration

package dataset_test

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dataset"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestIter3_BatchesCoverEveryRowOnce(t *testing.T) {
	ctx := context.Background()
	_, pool := testutil.NewTargetDB(t, "")
	if err := dataset.Load(ctx, pool, 1000); err != nil {
		t.Fatal(err)
	}
	total, batches, err := dataset.TimeBatchedUpdate(ctx, pool, 400)
	if err != nil {
		t.Fatal(err)
	}
	if batches != 3 || total <= 0 {
		t.Fatalf("batches=%d total=%s", batches, total)
	}
	var changed, maxPayload int64
	if err := pool.QueryRow(ctx, `SELECT count(*), max(payload) FROM big_events WHERE payload = 1`).Scan(&changed, &maxPayload); err != nil {
		t.Fatal(err)
	}
	if changed != 1000 || maxPayload != 1 {
		t.Fatalf("berubah=%d max=%d", changed, maxPayload)
	}
}
