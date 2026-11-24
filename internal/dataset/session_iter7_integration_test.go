//go:build integration

package dataset_test

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dataset"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestIter7_ReloadReplacesTheTableEvenWhenAViewDependsOnIt(t *testing.T) {
	ctx := context.Background()
	_, pool := testutil.NewTargetDB(t, "")
	if err := dataset.Load(ctx, pool, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE VIEW big_events_open AS SELECT id, payload FROM big_events`); err != nil {
		t.Fatal(err)
	}
	if err := dataset.Load(ctx, pool, 25); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM big_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 25 {
		t.Fatalf("baris = %d", n)
	}
	if err := dataset.Ensure(ctx, pool, 25); err != nil {
		t.Fatal(err)
	}
}
