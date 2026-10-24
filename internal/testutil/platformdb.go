package testutil

// Implementasi sementara oleh A supaya integration test auth/session/admin bisa jalan sebelum
// runner migrasi C (internal/platformdb) selesai. Signature mengikuti panduan-pengerjaan.md 9.4;
// C boleh mengganti isinya (mis. memakai runner golang-migrate) asal signature tetap.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/db"
)

// EnvDatabaseURL menunjuk ke server Postgres untuk test. User-nya harus boleh CREATE DATABASE.
const EnvDatabaseURL = "TEST_DATABASE_URL"

// RequireEnv men-skip test bila TEST_DATABASE_URL kosong.
func RequireEnv(t *testing.T) {
	t.Helper()
	if os.Getenv(EnvDatabaseURL) == "" {
		t.Skipf("%s kosong: integration test di-skip (jalankan `make up`, lalu set %s)", EnvDatabaseURL, EnvDatabaseURL)
	}
}

// NewPlatformDB membuat database baru test_<random>, menjalankan semua migrasi platform (*.up.sql
// urut nama file), dan men-drop database itu saat test selesai.
func NewPlatformDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	RequireEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminURL := os.Getenv(EnvDatabaseURL)
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("testutil: connect %s: %v", EnvDatabaseURL, err)
	}

	name := "test_" + randomHex(t, 8)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatalf("testutil: create database: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		t.Fatalf("testutil: parse %s: %v", EnvDatabaseURL, err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("testutil: open pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("testutil: drop database %s: %v", name, err)
		}
		admin.Close(ctx)
	})

	applyMigrations(t, ctx, pool)
	return pool
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	files, err := fs.Glob(db.PlatformMigrations, "platform_migrations/*.up.sql")
	if err != nil {
		t.Fatalf("testutil: list migrations: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := fs.ReadFile(db.PlatformMigrations, f)
		if err != nil {
			t.Fatalf("testutil: read %s: %v", f, err)
		}
		// Tanpa argumen, pgx memakai simple protocol sehingga satu file boleh berisi banyak statement.
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("testutil: migrate %s: %v", f, err)
		}
	}
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("testutil: random: %v", err)
	}
	return hex.EncodeToString(b)
}
