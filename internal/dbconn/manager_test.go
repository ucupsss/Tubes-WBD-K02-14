package dbconn_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
)

type mockConnectionRepo struct {
	connections map[int64]dbconn.Connection
}

func (m *mockConnectionRepo) GetByID(ctx context.Context, id int64) (dbconn.Connection, error) {
	conn, ok := m.connections[id]
	if !ok {
		return dbconn.Connection{}, dbconn.ErrNotFound
	}
	return conn, nil
}

func TestManager_NotFound(t *testing.T) {
	repo := &mockConnectionRepo{connections: make(map[int64]dbconn.Connection)}
	key := make([]byte, 32)
	mgr := dbconn.NewManager(repo, key)

	_, err := mgr.Pool(context.Background(), 999)
	if !errors.Is(err, dbconn.ErrNotFound) {
		t.Fatalf("got error %v, want %v", err, dbconn.ErrNotFound)
	}
}

func TestManager_InvalidateAndCloseAll(t *testing.T) {
	repo := &mockConnectionRepo{connections: make(map[int64]dbconn.Connection)}
	key := make([]byte, 32)
	mgr := dbconn.NewManager(repo, key)

	// Invalidate non-existent pool should not panic
	mgr.Invalidate(1)
	mgr.CloseAll()
}

func TestManager_TestConnection_FailureSanitized(t *testing.T) {
	repo := &mockConnectionRepo{connections: make(map[int64]dbconn.Connection)}
	key := make([]byte, 32)
	mgr := dbconn.NewManager(repo, key)

	conn := dbconn.Connection{
		Host:     "127.0.0.1",
		Port:     54321, // Port mati
		DBName:   "testdb",
		Username: "postgres",
		SSLMode:  "disable",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	secretPassword := "SuperSecretPassword123"
	err := mgr.TestConnection(ctx, conn, secretPassword)
	if err == nil {
		t.Fatal("expected error connecting to inactive port, got nil")
	}

	if !errors.Is(err, dbconn.ErrUnavailable) {
		t.Errorf("error must wrap ErrUnavailable, got: %v", err)
	}

	// Pastikan password rahasia tidak bocor di string error
	if strings.Contains(err.Error(), secretPassword) {
		t.Errorf("password leaked in error message: %v", err)
	}
}
