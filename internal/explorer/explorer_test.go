package explorer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explorer"
)

type mockRepo struct {
	tables      []explorer.TableSummary
	columns     []explorer.ColumnInfo
	constraints []explorer.ConstraintInfo
	indexes     []explorer.IndexInfo
	dataPage    *explorer.TableDataPage
	tableExists bool
	err         error
}

func (m *mockRepo) ListTables(ctx context.Context, pool *pgxpool.Pool) ([]explorer.TableSummary, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tables, nil
}

func (m *mockRepo) TableExists(ctx context.Context, pool *pgxpool.Pool, schema, table string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.tableExists, nil
}

func (m *mockRepo) GetColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]explorer.ColumnInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.columns, nil
}

func (m *mockRepo) GetConstraints(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]explorer.ConstraintInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.constraints, nil
}

func (m *mockRepo) GetIndexes(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]explorer.IndexInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.indexes, nil
}

func (m *mockRepo) GetPrimaryKeyColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]string, error) {
	return []string{"id"}, nil
}

func (m *mockRepo) GetTableData(ctx context.Context, pool *pgxpool.Pool, schema, table string, page, size int) (*explorer.TableDataPage, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.dataPage, nil
}

type mockManager struct {
	poolErr error
}

func (m *mockManager) Pool(ctx context.Context, dbID int64) (*pgxpool.Pool, error) {
	if m.poolErr != nil {
		return nil, m.poolErr
	}
	// return non-nil pointer placeholder for unit testing (repo is mocked)
	return &pgxpool.Pool{}, nil
}

func (m *mockManager) Invalidate(dbID int64) {}
func (m *mockManager) CloseAll()             {}
func (m *mockManager) TestConnection(ctx context.Context, conn dbconn.Connection, p string) error {
	return nil
}

type mockAuthorizer struct {
	dbs []authz.DatabaseAccess
}

func (m *mockAuthorizer) EffectiveLevel(ctx context.Context, userID, dbID int64) (authz.Level, error) {
	return authz.LevelView, nil
}

func (m *mockAuthorizer) AccessibleDatabases(ctx context.Context, userID int64) ([]authz.DatabaseAccess, error) {
	return m.dbs, nil
}

func (m *mockAuthorizer) Can(ctx context.Context, p authz.Principal, a authz.Action, dbID int64) (bool, error) {
	return true, nil
}

func TestService_GetDashboard(t *testing.T) {
	auth := &mockAuthorizer{
		dbs: []authz.DatabaseAccess{
			{DatabaseID: 1, Name: "DB Alpha", Level: authz.LevelEdit},
			{DatabaseID: 2, Name: "DB Beta", Level: authz.LevelView},
		},
	}
	svc := explorer.NewService(&mockManager{}, auth, &mockRepo{}, nil)

	dbs, err := svc.GetDashboard(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dbs) != 2 {
		t.Fatalf("expected 2 databases, got %d", len(dbs))
	}
	if dbs[0].Name != "DB Alpha" || dbs[0].Level != authz.LevelEdit {
		t.Errorf("unexpected db[0]: %+v", dbs[0])
	}
}

func TestService_ListTables_OfflineTargetDB(t *testing.T) {
	mgr := &mockManager{poolErr: errors.New("connection refused")}
	svc := explorer.NewService(mgr, &mockAuthorizer{}, &mockRepo{}, nil)

	_, err := svc.ListTables(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error for offline target DB, got nil")
	}
	if apperr.KindOf(err) != apperr.KindUnavailable {
		t.Errorf("expected KindUnavailable error, got: %v", err)
	}
}

func TestService_GetTableMetadata_NotFound(t *testing.T) {
	repo := &mockRepo{tableExists: false}
	svc := explorer.NewService(&mockManager{}, &mockAuthorizer{}, repo, nil)

	_, err := svc.GetTableMetadata(context.Background(), 1, "public", "non_existent")
	if !errors.Is(err, explorer.ErrTableNotFound) {
		t.Fatalf("expected ErrTableNotFound, got: %v", err)
	}
}

func TestService_GetTableData_Success(t *testing.T) {
	repo := &mockRepo{
		tableExists: true,
		dataPage: &explorer.TableDataPage{
			Schema:    "public",
			Table:     "users",
			Columns:   []string{"id", "email"},
			Rows:      [][]string{{"1", "alice@example.com"}},
			TotalRows: 1,
			Page:      1,
			Size:      50,
		},
	}
	svc := explorer.NewService(&mockManager{}, &mockAuthorizer{}, repo, nil)

	res, err := svc.GetTableData(context.Background(), 1, "public", "users", 1, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TotalRows != 1 || len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", res)
	}
}
