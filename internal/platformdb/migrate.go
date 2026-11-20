package platformdb

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/telemetry"
)

// Open creates a platform database pool and checks that it answers.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("platformdb.Open parse: %w", err)
	}
	cfg.ConnConfig.Tracer = telemetry.PgxTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("platformdb.Open: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("platformdb.Open ping: %w", err)
	}
	return pool, nil
}

// Migrate applies embedded SQL files in platform_migrations using golang-migrate.
// fsys must contain a platform_migrations directory (see package db).
// ErrNoChange is treated as success.
func Migrate(databaseURL string, fsys fs.FS) error {
	src, err := iofs.New(fsys, "platform_migrations")
	if err != nil {
		return fmt.Errorf("platformdb.Migrate source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("platformdb.Migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		_, _ = m.Close()
		return fmt.Errorf("platformdb.Migrate up: %w", err)
	}
	srcErr, dbErr := m.Close()
	if srcErr != nil {
		return fmt.Errorf("platformdb.Migrate close source: %w", srcErr)
	}
	if dbErr != nil {
		return fmt.Errorf("platformdb.Migrate close db: %w", dbErr)
	}
	return nil
}
