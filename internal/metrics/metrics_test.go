package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHandler_ExposesMetricsWithLowCardinalityLabels(t *testing.T) {
	JobsProcessed.WithLabelValues("success").Inc()
	JobRetries.WithLabelValues("55P03").Inc()
	MigrationDuration.WithLabelValues("apply", "normal", "success").Observe(0.2)
	BatchRows.WithLabelValues("apply").Add(500)

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	text := string(body)

	for _, want := range []string{
		`dbmp_jobs_processed_total{outcome="success"}`,
		`dbmp_job_retries_total{reason="55P03"}`,
		`dbmp_migration_duration_seconds_bucket{kind="apply",mode="normal",result="success"`,
		`dbmp_batch_rows_total{direction="apply"} 500`,
		`go_goroutines`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics tidak memuat %q", want)
		}
	}
	for _, banned := range []string{"job_id", "execution_id", "user_id", "username"} {
		if strings.Contains(text, banned+"=") {
			t.Errorf("/metrics memakai label berkardinalitas tinggi %q", banned)
		}
	}
}

func TestPoolCollector_TargetsWithoutPools(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(PoolCollector{Targets: func() []*pgxpool.Pool { return nil }})
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{"dbmp_db_pool_connections", "dbmp_db_pool_target_pools", "dbmp_db_pool_acquire_wait_seconds_total"} {
		if !names[want] {
			t.Errorf("metrik %s tidak dikumpulkan", want)
		}
	}
}
