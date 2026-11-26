package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	poolConns = prometheus.NewDesc("dbmp_db_pool_connections",
		"Koneksi di pool database saat ini, per pool dan state (active, idle, total).",
		[]string{"pool", "state"}, nil)
	poolMax = prometheus.NewDesc("dbmp_db_pool_max_connections",
		"Batas koneksi pool database.", []string{"pool"}, nil)
	poolAcquires = prometheus.NewDesc("dbmp_db_pool_acquires_total",
		"Jumlah pengambilan koneksi dari pool.", []string{"pool"}, nil)
	poolEmptyAcquires = prometheus.NewDesc("dbmp_db_pool_empty_acquires_total",
		"Pengambilan koneksi yang harus menunggu karena pool kosong.", []string{"pool"}, nil)
	poolAcquireWait = prometheus.NewDesc("dbmp_db_pool_acquire_wait_seconds_total",
		"Total waktu menunggu koneksi dari pool.", []string{"pool"}, nil)
	poolTargets = prometheus.NewDesc("dbmp_db_pool_target_pools",
		"Jumlah pool database target yang sedang terbuka.", nil, nil)

	jobsByStatus = prometheus.NewDesc("dbmp_jobs",
		"Job di tabel migration_jobs per status (queued, running, done, dead).", []string{"status"}, nil)
	executionsByStatus = prometheus.NewDesc("dbmp_executions",
		"Execution per status.", []string{"status"}, nil)
)

// PoolCollector membaca statistik pgxpool saat scrape. Pool target dijumlahkan menjadi satu
// label pool="target" supaya kardinalitas tidak tumbuh dengan jumlah database terdaftar.
type PoolCollector struct {
	Platform *pgxpool.Pool
	Targets  func() []*pgxpool.Pool
}

// Describe memenuhi prometheus.Collector.
func (c PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{poolConns, poolMax, poolAcquires, poolEmptyAcquires, poolAcquireWait, poolTargets} {
		ch <- d
	}
}

// Collect memenuhi prometheus.Collector.
func (c PoolCollector) Collect(ch chan<- prometheus.Metric) {
	if c.Platform != nil {
		emitPool(ch, "platform", []*pgxpool.Pool{c.Platform})
	}
	if c.Targets != nil {
		targets := c.Targets()
		emitPool(ch, "target", targets)
		ch <- prometheus.MustNewConstMetric(poolTargets, prometheus.GaugeValue, float64(len(targets)))
	}
}

func emitPool(ch chan<- prometheus.Metric, name string, pools []*pgxpool.Pool) {
	var active, idle, total, maxConns, acquires, empty int64
	var wait time.Duration
	for _, p := range pools {
		s := p.Stat()
		active += int64(s.AcquiredConns())
		idle += int64(s.IdleConns())
		total += int64(s.TotalConns())
		maxConns += int64(s.MaxConns())
		acquires += s.AcquireCount()
		empty += s.EmptyAcquireCount()
		wait += s.AcquireDuration()
	}
	ch <- prometheus.MustNewConstMetric(poolConns, prometheus.GaugeValue, float64(active), name, "active")
	ch <- prometheus.MustNewConstMetric(poolConns, prometheus.GaugeValue, float64(idle), name, "idle")
	ch <- prometheus.MustNewConstMetric(poolConns, prometheus.GaugeValue, float64(total), name, "total")
	ch <- prometheus.MustNewConstMetric(poolMax, prometheus.GaugeValue, float64(maxConns), name)
	ch <- prometheus.MustNewConstMetric(poolAcquires, prometheus.CounterValue, float64(acquires), name)
	ch <- prometheus.MustNewConstMetric(poolEmptyAcquires, prometheus.CounterValue, float64(empty), name)
	ch <- prometheus.MustNewConstMetric(poolAcquireWait, prometheus.CounterValue, wait.Seconds(), name)
}

// jobStatuses dan executionStatuses selalu diekspor (nilai 0 bila kosong) supaya panel Grafana stabil.
var (
	jobStatuses       = []string{"queued", "running", "done", "dead"}
	executionStatuses = []string{
		"pending_review", "approved", "rejected", "queued", "running", "success", "failed",
		"partial", "partially_reverted", "uncertain", "cancelled",
	}
)

// QueueCollector menghitung job dan execution per status langsung dari database platform saat scrape,
// sehingga angkanya benar walau ada beberapa worker atau proses baru saja restart.
// Hanya didaftarkan di server agar nilai tidak terhitung ganda.
type QueueCollector struct {
	Pool *pgxpool.Pool
}

// Describe memenuhi prometheus.Collector.
func (c QueueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- jobsByStatus
	ch <- executionsByStatus
}

// Collect memenuhi prometheus.Collector.
func (c QueueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	emitCounts(ctx, ch, c.Pool, jobsByStatus, jobStatuses, `SELECT status, count(*) FROM migration_jobs GROUP BY status`)
	emitCounts(ctx, ch, c.Pool, executionsByStatus, executionStatuses, `SELECT status, count(*) FROM executions GROUP BY status`)
}

func emitCounts(ctx context.Context, ch chan<- prometheus.Metric, pool *pgxpool.Pool, desc *prometheus.Desc, known []string, query string) {
	counts := make(map[string]int64, len(known))
	for _, s := range known {
		counts[s] = 0
	}
	rows, err := pool.Query(ctx, query)
	if err != nil {
		slog.Warn("metrics.collect", "err", err)
		ch <- prometheus.NewInvalidMetric(desc, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			ch <- prometheus.NewInvalidMetric(desc, err)
			return
		}
		// Status di luar daftar tidak diekspor, supaya label tetap himpunan tetap.
		if _, ok := counts[status]; ok {
			counts[status] = n
		}
	}
	if err := rows.Err(); err != nil {
		ch <- prometheus.NewInvalidMetric(desc, err)
		return
	}
	for _, s := range known {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(counts[s]), s)
	}
}
