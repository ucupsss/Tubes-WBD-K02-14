package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/db"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// RequireEnv skips the test when TEST_DATABASE_URL is not configured.
func RequireEnv(t *testing.T) {
	t.Helper()
	config.LoadDotEnv()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL kosong: jalankan make up dan isi .env untuk integration test")
	}
}

// NewPlatformDB creates a throwaway database, applies every platform migration, and drops it on cleanup.
func NewPlatformDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin, adminURL := adminPool(t)
	name := createDB(t, admin)
	dsn := databaseURL(t, adminURL, name)
	if err := platformdb.Migrate(dsn, db.PlatformMigrations); err != nil {
		t.Fatalf("migrate platform db: %v", err)
	}
	return openAppPool(t, dsn)
}

// NewTargetDB creates an empty database on the test server. seedSQL is optional and may contain multiple statements.
func NewTargetDB(t *testing.T, seedSQL string) (string, *pgxpool.Pool) {
	t.Helper()
	admin, adminURL := adminPool(t)
	name := createDB(t, admin)
	pool := openAppPool(t, databaseURL(t, adminURL, name))
	if strings.TrimSpace(seedSQL) != "" {
		if err := execSimple(context.Background(), pool, seedSQL); err != nil {
			t.Fatalf("seed target db: %v", err)
		}
	}
	return name, pool
}

func adminPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	RequireEnv(t)
	adminURL := os.Getenv("TEST_DATABASE_URL")
	pool, err := pgxpool.New(context.Background(), adminURL)
	if err != nil {
		t.Fatalf("connect test server: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, adminURL
}

func createDB(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	name := "test_" + randHex(t)
	stmt := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
	if err := execSimple(context.Background(), admin, stmt); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { dropDB(admin, name) })
	return name
}

func openAppPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := platformdb.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func databaseURL(t *testing.T, raw, name string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse database url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func dropDB(admin *pgxpool.Pool, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = admin.Exec(ctx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
	_ = execSimple(ctx, admin, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize())
}

func execSimple(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Conn().PgConn().Exec(ctx, sql).ReadAll()
	return err
}

func randHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buf)
}
