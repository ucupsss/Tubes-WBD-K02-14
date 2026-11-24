//go:build integration

package dataset_test

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dataset"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestLoad_BatchRangeUpdatesOnlyThatSlice(t *testing.T) {
	ctx := context.Background()
	_, pool := testutil.NewTargetDB(t, "")
	if err := dataset.Load(ctx, pool, 1000); err != nil {
		t.Fatal(err)
	}
	tag, err := pool.Exec(ctx, `
		UPDATE big_events SET payload = payload + 1
		WHERE id > $1 AND id <= $2`, 100, 250)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 150 {
		t.Fatalf("rows = %d, want 150", tag.RowsAffected())
	}
	var untouched int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM big_events WHERE payload = 0`).Scan(&untouched); err != nil {
		t.Fatal(err)
	}
	if untouched != 850 {
		t.Fatalf("yang tidak berubah = %d", untouched)
	}
	if err := dataset.Ensure(ctx, pool, 1000); err != nil {
		t.Fatal(err)
	}
}
