package databases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type mockRepo struct {
	connections   map[int64]databases.Connection
	nextID        int64
	hasMigrations bool
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		connections: make(map[int64]databases.Connection),
		nextID:      1,
	}
}

func (m *mockRepo) List(ctx context.Context, db platformdb.DBTX) ([]databases.Connection, error) {
	var list []databases.Connection
	for _, c := range m.connections {
		list = append(list, c)
	}
	return list, nil
}

func (m *mockRepo) GetByID(ctx context.Context, db platformdb.DBTX, id int64) (databases.Connection, error) {
	c, ok := m.connections[id]
	if !ok {
		return databases.Connection{}, databases.ErrNotFound
	}
	return c, nil
}

func (m *mockRepo) Create(ctx context.Context, db platformdb.DBTX, conn databases.Connection) (int64, error) {
	conn.ID = m.nextID
	m.nextID++
	m.connections[conn.ID] = conn
	return conn.ID, nil
}

func (m *mockRepo) Update(ctx context.Context, db platformdb.DBTX, conn databases.Connection) error {
	if _, ok := m.connections[conn.ID]; !ok {
		return databases.ErrNotFound
	}
	m.connections[conn.ID] = conn
	return nil
}

func (m *mockRepo) Delete(ctx context.Context, db platformdb.DBTX, id int64) error {
	if _, ok := m.connections[id]; !ok {
		return databases.ErrNotFound
	}
	delete(m.connections, id)
	return nil
}

func (m *mockRepo) HasMigrations(ctx context.Context, db platformdb.DBTX, id int64) (bool, error) {
	return m.hasMigrations, nil
}

type mockManager struct {
	invalidatedIDs []int64
}

func (m *mockManager) Pool(ctx context.Context, dbID int64) (*pgxpool.Pool, error) {
	return nil, nil
}

func (m *mockManager) Invalidate(dbID int64) {
	m.invalidatedIDs = append(m.invalidatedIDs, dbID)
}

func (m *mockManager) CloseAll() {}

func (m *mockManager) TestConnection(ctx context.Context, conn dbconn.Connection, plainPassword string) error {
	if conn.Host == "fail" {
		return dbconn.ErrUnavailable
	}
	return nil
}

func TestService_Create_Validation(t *testing.T) {
	repo := newMockRepo()
	mgr := &mockManager{}
	key := make([]byte, 32)
	svc := databases.NewService(nil, repo, mgr, key)

	// Missing required fields
	_, err := svc.Create(context.Background(), databases.CreateInput{})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	fe, ok := apperr.FieldErrors(err)
	if !ok || len(fe) == 0 {
		t.Fatalf("expected field errors, got: %v", err)
	}

	// Valid input
	id, err := svc.Create(context.Background(), databases.CreateInput{
		Name:     "Target 1",
		Host:     "localhost",
		Port:     5432,
		DBName:   "mydb",
		Username: "postgres",
		Password: "plainPassword",
		SSLMode:  "disable",
	})
	if err != nil {
		t.Fatalf("unexpected error creating connection: %v", err)
	}
	if id != 1 {
		t.Fatalf("got id %d, want 1", id)
	}
}

func TestService_Update_KeepExistingPassword(t *testing.T) {
	repo := newMockRepo()
	mgr := &mockManager{}
	key := make([]byte, 32)
	svc := databases.NewService(nil, repo, mgr, key)

	id, err := svc.Create(context.Background(), databases.CreateInput{
		Name:     "Old Name",
		Host:     "localhost",
		Port:     5432,
		DBName:   "old_db",
		Username: "postgres",
		Password: "InitialPassword",
	})
	if err != nil {
		t.Fatal(err)
	}

	originalConn, _ := repo.GetByID(context.Background(), nil, id)

	// Update with empty password -> must retain original password_enc
	err = svc.Update(context.Background(), id, databases.UpdateInput{
		Name:     "New Name",
		Host:     "192.168.1.1",
		Port:     5433,
		DBName:   "new_db",
		Username: "new_user",
		Password: "", // empty!
	})
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}

	updatedConn, _ := repo.GetByID(context.Background(), nil, id)
	if string(updatedConn.PasswordEnc) != string(originalConn.PasswordEnc) {
		t.Fatal("password_enc should not change when empty password provided")
	}
	if updatedConn.Name != "New Name" {
		t.Fatalf("got name %q, want 'New Name'", updatedConn.Name)
	}

	// Must have invalidated manager pool
	if len(mgr.invalidatedIDs) != 1 || mgr.invalidatedIDs[0] != id {
		t.Fatalf("expected pool invalidation for ID %d", id)
	}
}

func TestService_Delete_BlockedIfHasMigrations(t *testing.T) {
	repo := newMockRepo()
	repo.hasMigrations = true
	mgr := &mockManager{}
	key := make([]byte, 32)
	svc := databases.NewService(nil, repo, mgr, key)

	err := svc.Delete(context.Background(), 1)
	if !errors.Is(err, databases.ErrHasMigrations) {
		t.Fatalf("expected ErrHasMigrations, got: %v", err)
	}
}
