package groups_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// Route admin A & B didaftarkan ke mux yang sama tanpa konflik pola, dan semuanya hanya untuk admin.
func TestRoutes_AdminOnly(t *testing.T) {
	mux := http.NewServeMux()
	mw := authz.NewMiddleware(authz.NewAuthorizer(nil), nil) // aksi admin tidak membaca grant
	gh, _ := newHandler(newStubRepo())
	groups.RegisterRoutes(mux, gh, mw)
	users.RegisterRoutes(mux, users.NewHandler(users.NewService(nil, nil, nil, nil), view.New()), mw)
	access.RegisterRoutes(mux, access.NewHandler(access.NewService(nil, nil, nil), view.New()), mw)

	paths := []struct{ method, path string }{
		{http.MethodGet, "/admin/groups"},
		{http.MethodGet, "/admin/groups/new"},
		{http.MethodPost, "/admin/groups"},
		{http.MethodGet, "/admin/groups/5"},
		{http.MethodPost, "/admin/groups/5/edit"},
		{http.MethodPost, "/admin/groups/5/delete"},
		{http.MethodPost, "/admin/groups/5/members"},
		{http.MethodPost, "/admin/groups/5/members/2/delete"},
		{http.MethodGet, "/admin/users"},
		{http.MethodGet, "/admin/users/new"},
		{http.MethodPost, "/admin/users"},
		{http.MethodGet, "/admin/users/2/edit"},
		{http.MethodPost, "/admin/users/2/edit"},
		{http.MethodPost, "/admin/users/2/password"},
	}
	for _, p := range paths {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			// Anonim → redirect login.
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(p.method, p.path, nil))
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
				t.Fatalf("anonim: status=%d loc=%q, want 303 /login", rec.Code, rec.Header().Get("Location"))
			}

			// Login tapi bukan admin → 403.
			req := httptest.NewRequest(p.method, p.path, nil)
			req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 2, Username: "bob"}))
			rec = httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("non-admin: status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestRoutes_AdminReachesHandler(t *testing.T) {
	mux := http.NewServeMux()
	gh, _ := newHandler(newStubRepo())
	groups.RegisterRoutes(mux, gh, authz.NewMiddleware(authz.NewAuthorizer(nil), nil))

	req := httptest.NewRequest(http.MethodGet, "/admin/groups/5", nil)
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: status = %d, want 200 (path value {id} harus terbaca)", rec.Code)
	}
}
