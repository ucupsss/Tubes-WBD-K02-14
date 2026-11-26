// Package metrics menyediakan metrik Prometheus untuk server dan worker.
//
// Semua label berkardinalitas rendah: nilainya berasal dari himpunan tetap (status, outcome, kind,
// mode, direction, SQLSTATE yang bisa di-retry). ID user, job, execution, dan teks SQL tidak pernah
// dipakai sebagai label.
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry menampung semua metrik proses ini. Registry sendiri (bukan default global) supaya
// isi /metrics hanya metrik yang didefinisikan di sini plus metrik runtime Go dan proses.
var Registry = prometheus.NewRegistry()

var (
	// JobsProcessed menghitung job yang selesai diproses worker per outcome
	// (success, done, retry, uncertain, failed).
	JobsProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dbmp_jobs_processed_total",
		Help: "Job yang selesai diproses worker, per outcome.",
	}, []string{"outcome"})

	// JobDuration adalah lama satu job diproses worker (runner + pencatatan), per outcome.
	JobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dbmp_job_duration_seconds",
		Help:    "Lama satu job diproses worker, per outcome.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"outcome"})

	// JobRetries menghitung job yang dikembalikan ke antrian, per alasan (SQLSTATE, lock_busy, db_busy, platform_record, atau deferred).
	JobRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dbmp_job_retries_total",
		Help: "Job yang dikembalikan ke antrian untuk dicoba lagi, per alasan.",
	}, []string{"reason"})

	// QueueWait adalah jeda antara job siap diambil (run_after) dan saat worker mengklaimnya.
	QueueWait = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "dbmp_queue_wait_seconds",
		Help:    "Waktu job menunggu di antrian sejak siap diambil sampai diklaim worker.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 300},
	})

	// LockAcquire adalah lama percobaan mengambil kunci database target, per hasil (acquired, busy, error).
	LockAcquire = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dbmp_lock_acquire_seconds",
		Help:    "Lama percobaan mengambil kunci database target, per hasil.",
		Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}, []string{"result"})

	// LockWait adalah waktu sejak job pertama kali masuk antrian sampai kunci database target didapat.
	// Kunci tidak memblokir; job yang menemukan kunci sibuk diantre ulang, jadi nilai ini mencakup
	// semua putaran tunggu karena kunci dipegang execution lain.
	LockWait = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "dbmp_lock_wait_seconds",
		Help:    "Waktu sejak job masuk antrian sampai kunci database target didapat.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 300, 900},
	})

	// MigrationDuration adalah lama SQL migration dijalankan di database target.
	MigrationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dbmp_migration_duration_seconds",
		Help:    "Lama SQL migration dijalankan di database target, per kind, mode, dan hasil.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
	}, []string{"kind", "mode", "result"})

	// BatchesCommitted menghitung batch yang sudah di-commit, per arah (apply atau revert).
	BatchesCommitted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dbmp_batches_committed_total",
		Help: "Batch migration yang sudah di-commit di database target, per arah.",
	}, []string{"direction"})

	// BatchRows menghitung baris yang diproses batch yang sudah di-commit, per arah.
	BatchRows = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dbmp_batch_rows_total",
		Help: "Baris yang diproses batch migration yang sudah di-commit, per arah.",
	}, []string{"direction"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		JobsProcessed, JobDuration, JobRetries, QueueWait,
		LockAcquire, LockWait, MigrationDuration, BatchesCommitted, BatchRows,
	)
}

// Since mengembalikan detik sejak t, minimal 0 (jam proses dan jam database bisa sedikit berbeda).
func Since(t time.Time) float64 {
	return max(time.Since(t).Seconds(), 0)
}

// Handler menyajikan isi Registry dalam format teks Prometheus.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// Serve membuka listener /metrics terpisah dari API di addr sampai ctx selesai.
// addr kosong atau "off" mematikan endpoint. Kegagalan listener hanya dicatat, tidak menghentikan proses.
func Serve(ctx context.Context, addr string) {
	if addr == "" || addr == "off" {
		return
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	go func() {
		slog.Info("metrics listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics stopped", "err", err, "addr", addr)
		}
	}()
}
