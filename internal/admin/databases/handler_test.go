package databases_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

func TestHandler_List(t *testing.T) {
	repo := newMockRepo()
	mgr := &mockManager{}
	key := make([]byte, 32)
	svc := databases.NewService(nil, repo, mgr, key)
	h := databases.NewHandler(svc, view.New())

	req := httptest.NewRequest(http.MethodGet, "/admin/databases", nil)
	rec := httptest.NewRecorder()

	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestHandler_Create_ValidationFail(t *testing.T) {
	repo := newMockRepo()
	mgr := &mockManager{}
	key := make([]byte, 32)
	svc := databases.NewService(nil, repo, mgr, key)
	h := databases.NewHandler(svc, view.New())

	form := url.Values{}
	form.Set("name", "") // kosong -> gagal validasi

	req := httptest.NewRequest(http.MethodPost, "/admin/databases", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.Create(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422 for validation error, got %d", rec.Code)
	}
}

func TestHandler_Create_Success(t *testing.T) {
	repo := newMockRepo()
	mgr := &mockManager{}
	key := make([]byte, 32)
	svc := databases.NewService(nil, repo, mgr, key)
	h := databases.NewHandler(svc, view.New())

	form := url.Values{}
	form.Set("name", "Target Alpha")
	form.Set("host", "localhost")
	form.Set("port", "5432")
	form.Set("db_name", "target_db")
	form.Set("username", "postgres")
	form.Set("password", "secret")
	form.Set("sslmode", "disable")

	req := httptest.NewRequest(http.MethodPost, "/admin/databases", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "admin"}))
	rec := httptest.NewRecorder()

	h.Create(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/databases" {
		t.Fatalf("expected redirect to /admin/databases, got %q", loc)
	}
}
