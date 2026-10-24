package platformdb

import (
	"context"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/db"
)

func TestOpen_ClosedPort(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := Open(ctx, "postgres://test:test@127.0.0.1:1/postgres?sslmode=disable")
	if err == nil {
		t.Fatal("expected a connection error")
	}
}

func TestMigrate_ClosedPort(t *testing.T) {
	t.Parallel()
	err := Migrate("postgres://test:test@127.0.0.1:1/postgres?sslmode=disable", db.PlatformMigrations)
	if err == nil {
		t.Fatal("expected a migration error")
	}
}
