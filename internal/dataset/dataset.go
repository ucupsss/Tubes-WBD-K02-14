// Package dataset membuat tabel big_events untuk uji migration besar (D42).
//
// Kolom id unik dan tidak berubah, sehingga mode batch dapat memotong rentang
// dengan placeholder :from dan :to. Jumlah baris Rows dipilih supaya satu
// UPDATE seluruh tabel tanpa batch memakan waktu lebih dari satu detik.
//
// Pemilik: C.
package dataset

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Table adalah nama tabel data uji.
	Table = "big_events"
	// Rows adalah jumlah baris yang diukur pada mesin uji sehingga satu UPDATE penuh
	// melebihi satu detik. Lihat README.
	Rows = 400_000
	// BatchSize adalah ukuran batch untuk pengukuran pembanding.
	BatchSize = 50_000
)

// Load menghapus big_events lalu mengisinya dengan n baris (id 1..n, payload 0).
func Load(ctx context.Context, pool *pgxpool.Pool, n int) error {
	if n <= 0 {
		return fmt.Errorf("dataset: jumlah baris harus lebih dari 0")
	}
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS big_events CASCADE`); err != nil {
		return fmt.Errorf("dataset: drop: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE big_events (
			id bigint PRIMARY KEY,
			payload int NOT NULL
		)`); err != nil {
		return fmt.Errorf("dataset: create: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO big_events (id, payload)
		SELECT g, 0 FROM generate_series(1, $1::bigint) AS g`, n); err != nil {
		return fmt.Errorf("dataset: insert: %w", err)
	}
	return nil
}

// Ensure membuat tabel bila belum berisi tepat n baris.
func Ensure(ctx context.Context, pool *pgxpool.Pool, n int) error {
	var count int64
	err := pool.QueryRow(ctx, `SELECT count(*) FROM big_events`).Scan(&count)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return Load(ctx, pool, n)
		}
		return fmt.Errorf("dataset: count: %w", err)
	}
	if count == int64(n) {
		var shaped bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = 'big_events'
				  AND column_name = 'id' AND data_type = 'bigint')
			AND EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = 'big_events'
				  AND column_name = 'payload' AND data_type = 'integer')`).Scan(&shaped)
		if err != nil {
			return fmt.Errorf("dataset: bentuk tabel: %w", err)
		}
		if shaped {
			return nil
		}
	}
	return Load(ctx, pool, n)
}

// TimeFullUpdate mengukur satu UPDATE atas setiap baris, tanpa batch.
func TimeFullUpdate(ctx context.Context, pool *pgxpool.Pool) (time.Duration, error) {
	start := time.Now()
	if _, err := pool.Exec(ctx, `UPDATE big_events SET payload = payload + 1`); err != nil {
		return 0, fmt.Errorf("dataset: update penuh: %w", err)
	}
	return time.Since(start), nil
}

// TimeBatchedUpdate mengukur UPDATE yang sama dipotong per batchSize baris (D39).
// Mengembalikan total waktu dan jumlah batch.
func TimeBatchedUpdate(ctx context.Context, pool *pgxpool.Pool, batchSize int) (time.Duration, int, error) {
	if batchSize <= 0 {
		return 0, 0, fmt.Errorf("dataset: ukuran batch harus lebih dari 0")
	}
	var cursor int64
	var total time.Duration
	batches := 0
	for {
		var to *int64
		err := pool.QueryRow(ctx, `
			SELECT max(id) FROM (
				SELECT id FROM big_events WHERE id > $1 ORDER BY id LIMIT $2
			) AS page`, cursor, batchSize).Scan(&to)
		if err != nil {
			return 0, batches, fmt.Errorf("dataset: batas batch: %w", err)
		}
		if to == nil {
			return total, batches, nil
		}
		start := time.Now()
		if _, err := pool.Exec(ctx, `
			UPDATE big_events SET payload = payload + 1
			WHERE id > $1 AND id <= $2`, cursor, *to); err != nil {
			return 0, batches, fmt.Errorf("dataset: update batch: %w", err)
		}
		total += time.Since(start)
		batches++
		cursor = *to
	}
}
