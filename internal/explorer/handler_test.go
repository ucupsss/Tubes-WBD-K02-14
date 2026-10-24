package explorer_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explorer"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

func TestHandler_Dashboard(t *testing.T) {
	auth := &mockAuthorizer{
		dbs: []authz.DatabaseAccess{
			{DatabaseID: 1, Name: "DB Alpha", Level: authz.LevelView},
		},
	}
	svc := explorer.NewService(&mockManager{}, auth, &mockRepo{}, nil)
	h := explorer.NewHandler(svc, view.New())

	// Unauthenticated should redirect
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.Dashboard(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect for unauthenticated user, got %d", rec.Code)
	}

	// Authenticated should return 200
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec = httptest.NewRecorder()
	h.Dashboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
}

func TestHandler_Overview(t *testing.T) {
	repo := &mockRepo{
		tables: []explorer.TableSummary{
			{Schema: "public", Name: "customers", Type: "BASE TABLE", ApproxRows: 120},
		},
	}
	svc := explorer.NewService(&mockManager{}, &mockAuthorizer{}, repo, nil)
	h := explorer.NewHandler(svc, view.New())

	req := httptest.NewRequest(http.MethodGet, "/db/1", nil)
	req.SetPathValue("dbID", "1")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec := httptest.NewRecorder()

	h.Overview(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
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
	h := explorer.NewHandler(svc, view.New())

	req := httptest.NewRequest(http.MethodGet, "/db/1/tables/public/customers", nil)
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
	h := explorer.NewHandler(svc, view.New())

	req := httptest.NewRequest(http.MethodGet, "/db/1/tables/public/customers/data?page=1&size=50", nil)
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
