//go:build integration

package dataset_test

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dataset"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestIter6_EnsureRebuildsATableThatOnlyHasTheRightCount(t *testing.T) {
	ctx := context.Background()
	_, pool := testutil.NewTargetDB(t, "")
	if _, err := pool.Exec(ctx, `CREATE TABLE big_events (id bigint PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO big_events (id) SELECT g FROM generate_series(1, 1000) AS g`); err != nil {
		t.Fatal(err)
	}
	if err := dataset.Ensure(ctx, pool, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := dataset.TimeFullUpdate(ctx, pool); err != nil {
		t.Fatalf("ensure meninggalkan tabel tanpa kolom payload: %v", err)
	}
	var n int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM big_events WHERE payload = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1000 {
		t.Fatalf("baris terbarui = %d", n)
	}
}
