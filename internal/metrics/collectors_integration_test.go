//go:build integration

package metrics_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/metrics"
	dbtest "github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestQueueCollector_ExportsEveryStatusFromDatabase(t *testing.T) {
	dbtest.RequireEnv(t)
	pool := dbtest.NewPlatformDB(t)

	reg := prometheus.NewRegistry()
	reg.MustRegister(metrics.QueueCollector{Pool: pool}, metrics.PoolCollector{Platform: pool})

	// Database baru: semua status tetap diekspor dengan nilai 0, supaya panel Grafana tidak kosong.
	if n, err := testutil.GatherAndCount(reg, "dbmp_jobs"); err != nil || n != 4 {
		t.Fatalf("dbmp_jobs series = %d, err = %v, want 4", n, err)
	}
	if n, err := testutil.GatherAndCount(reg, "dbmp_executions"); err != nil || n != 11 {
		t.Fatalf("dbmp_executions series = %d, err = %v, want 11", n, err)
	}
	if n, err := testutil.GatherAndCount(reg, "dbmp_db_pool_connections"); err != nil || n != 3 {
		t.Fatalf("dbmp_db_pool_connections series = %d, err = %v, want 3 (active, idle, total)", n, err)
	}
	problems, err := testutil.GatherAndLint(reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("lint %s: %s", p.Metric, p.Text)
	}
}
