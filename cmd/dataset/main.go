// Program dataset mengisi big_events. Flag -search mencari jumlah baris sampai satu UPDATE penuh melebihi satu detik.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dataset"
)

func main() {
	search := flag.Bool("search", false, "naikkan jumlah baris sampai satu UPDATE penuh melebihi satu detik")
	urlFlag := flag.String("url", "", "URL database target (default DATASET_DATABASE_URL atau target-db-1)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	url := *urlFlag
	if url == "" {
		url = os.Getenv("DATASET_DATABASE_URL")
	}
	if url == "" {
		url = "postgres://target:target@localhost:5433/target1?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		logger.Error("dataset connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `SET statement_timeout = 0`); err != nil {
		logger.Error("dataset timeout", "err", err)
		os.Exit(1)
	}

	if *search {
		if err := runSearch(ctx, logger, pool); err != nil {
			logger.Error("dataset search", "err", err)
			os.Exit(1)
		}
		return
	}
	if dataset.Rows <= 0 {
		logger.Error("dataset", "err", "Rows belum diukur; jalankan go run ./cmd/dataset -search")
		os.Exit(1)
	}
	if err := dataset.Ensure(ctx, pool, dataset.Rows); err != nil {
		logger.Error("dataset load", "err", err)
		os.Exit(1)
	}
	logger.Info("dataset ready", "table", dataset.Table, "rows", dataset.Rows)
}

func runSearch(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool) error {
	n := 200_000
	var full time.Duration
	for {
		logger.Info("mencoba", "rows", n)
		if err := dataset.Load(ctx, pool, n); err != nil {
			return err
		}
		d, err := dataset.TimeFullUpdate(ctx, pool)
		if err != nil {
			return err
		}
		logger.Info("update penuh", "rows", n, "duration", d.String())
		full = d
		if d >= time.Second || n >= 20_000_000 {
			break
		}
		n *= 2
	}
	batchDur, batches, err := dataset.TimeBatchedUpdate(ctx, pool, dataset.BatchSize)
	if err != nil {
		return err
	}
	fmt.Printf("rows=%d full=%s batches=%d batch_size=%d batch_total=%s\n",
		n, full.Round(time.Millisecond), batches, dataset.BatchSize, batchDur.Round(time.Millisecond))
	return nil
}
