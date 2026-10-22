package access_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

func TestHandler_Show(t *testing.T) {
	repo := newMockAccessRepo()
	bumper := &mockBumper{}
	svc := access.NewService(nil, repo, bumper)
	h := access.NewHandler(svc, view.New())

	req := httptest.NewRequest(http.MethodGet, "/admin/groups/1/access", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	h.Show(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestHandler_Update(t *testing.T) {
	repo := newMockAccessRepo()
	bumper := &mockBumper{}
	svc := access.NewService(nil, repo, bumper)
	h := access.NewHandler(svc, view.New())

	form := url.Values{}
	form.Set("grant_10", "edit")
	form.Set("grant_20", "view")

	req := httptest.NewRequest(http.MethodPost, "/admin/groups/1/access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "1")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "admin"}))
	rec := httptest.NewRecorder()

	h.Update(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/groups/1/access" {
		t.Fatalf("expected redirect to /admin/groups/1/access, got %q", loc)
	}
}
