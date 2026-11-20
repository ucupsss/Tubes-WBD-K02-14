package explorer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explorer"
)

func TestHandler_ListDatabases(t *testing.T) {
	auth := &mockAuthorizer{
		dbs: []authz.DatabaseAccess{
			{DatabaseID: 1, Name: "DB Alpha", Level: authz.LevelView},
		},
	}
	svc := explorer.NewService(&mockManager{}, auth, &mockRepo{}, nil)
	h := explorer.NewHandler(svc)

	// Unauthenticated should return 401 JSON
	req := httptest.NewRequest(http.MethodGet, "/api/databases", nil)
	rec := httptest.NewRecorder()
	h.ListDatabases(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated user, got %d", rec.Code)
	}

	// Authenticated should return 200 JSON with items
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec = httptest.NewRecorder()
	h.ListDatabases(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 database item, got %v", out)
	}
}

func TestHandler_Schema(t *testing.T) {
	repo := &mockRepo{
		tables: []explorer.TableSummary{
			{Schema: "public", Name: "customers", Type: "BASE TABLE", ApproxRows: 120},
		},
	}
	svc := explorer.NewService(&mockManager{}, &mockAuthorizer{}, repo, nil)
	h := explorer.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/databases/1/schema", nil)
	req.SetPathValue("dbID", "1")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec := httptest.NewRecorder()

	h.Schema(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	tables, _ := out["tables"].([]any)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %v", out)
	}
}

func TestHandler_TableDetails(t *testing.T) {
	repo := &mockRepo{
		tableExists: true,
		columns: []explorer.ColumnInfo{
			{Name: "id", DataType: "bigint", IsPrimaryKey: true},
		},
	}
	svc := explorer.NewService(&mockManager{}, &mockAuthorizer{}, repo, nil)
	h := explorer.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/databases/1/tables/public/customers", nil)
	req.SetPathValue("dbID", "1")
	req.SetPathValue("schema", "public")
	req.SetPathValue("table", "customers")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec := httptest.NewRecorder()

	h.TableDetails(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
}

func TestHandler_TableData(t *testing.T) {
	repo := &mockRepo{
		tableExists: true,
		dataPage: &explorer.TableDataPage{
			Schema:    "public",
			Table:     "customers",
			Columns:   []string{"id"},
			Rows:      [][]string{{"1"}},
			TotalRows: 1,
			Page:      1,
			Size:      50,
		},
	}
	svc := explorer.NewService(&mockManager{}, &mockAuthorizer{}, repo, nil)
	h := explorer.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/databases/1/tables/public/customers/data?page=1&page_size=50", nil)
	req.SetPathValue("dbID", "1")
	req.SetPathValue("schema", "public")
	req.SetPathValue("table", "customers")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec := httptest.NewRecorder()

	h.TableData(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
}
