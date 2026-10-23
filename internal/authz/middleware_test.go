package authz_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

// recorder mencatat error yang diteruskan middleware ke ErrorHandler.
type recorder struct {
	err error
}

func (rc *recorder) handle(w http.ResponseWriter, r *http.Request, err error) {
	rc.err = err
	w.WriteHeader(http.StatusTeapot) // penanda bahwa ErrorHandler yang menulis respons
}

func okHandler(called *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	}
}

func newRequest(p *authz.Principal, dbID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if p != nil {
		req = req.WithContext(authz.WithPrincipal(req.Context(), *p))
	}
	if dbID != "" {
		req.SetPathValue("dbID", dbID)
	}
	return req
}

func TestMiddleware_Protect(t *testing.T) {
	admin := &authz.Principal{UserID: 1, Username: "admin", IsAdmin: true}
	alice := &authz.Principal{UserID: 2, Username: "alice"}

	tests := []struct {
		name     string
		p        *authz.Principal
		action   authz.Action
		dbID     string
		levels   []authz.Level
		grantErr error
		wantNext bool
		wantKind apperr.Kind // dicek hanya bila wantNext=false
	}{
		{"not logged in", nil, authz.ActDBView, "1", []authz.Level{authz.LevelEdit}, nil, false, apperr.KindUnauthenticated},
		{"admin opens admin page", admin, authz.ActAdminManageUsers, "", nil, nil, true, 0},
		{"non-admin opens admin page", alice, authz.ActAdminManageUsers, "", nil, nil, false, apperr.KindForbidden},
		{"view user views db", alice, authz.ActDBView, "1", []authz.Level{authz.LevelView}, nil, true, 0},
		{"view user executes migration", alice, authz.ActMigrationExecute, "1", []authz.Level{authz.LevelView}, nil, false, apperr.KindForbidden},
		{"edit user executes migration", alice, authz.ActMigrationExecute, "1", []authz.Level{authz.LevelEdit}, nil, true, 0},
		{"user without grant", alice, authz.ActDBView, "1", nil, nil, false, apperr.KindForbidden},
		{"admin without grant views db (D2)", admin, authz.ActDBView, "1", nil, nil, false, apperr.KindForbidden},
		{"dbID not a number", alice, authz.ActDBView, "abc", []authz.Level{authz.LevelEdit}, nil, false, apperr.KindNotFound},
		{"dbID missing", alice, authz.ActDBView, "", []authz.Level{authz.LevelEdit}, nil, false, apperr.KindNotFound},
		{"dbID zero", alice, authz.ActDBView, "0", []authz.Level{authz.LevelEdit}, nil, false, apperr.KindNotFound},
		{"dbID negative", alice, authz.ActDBView, "-1", []authz.Level{authz.LevelEdit}, nil, false, apperr.KindNotFound},
		{"grant source error", alice, authz.ActDBView, "1", nil, errGrantDB, false, apperr.KindInternal},
		{"unknown action", admin, "db.unknown", "1", []authz.Level{authz.LevelEdit}, nil, false, apperr.KindForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &recorder{}
			mw := authz.NewMiddleware(authz.NewAuthorizer(&stubGrants{levels: tt.levels, err: tt.grantErr}), rc.handle)

			called := false
			rec := httptest.NewRecorder()
			mw.Protect(tt.action, okHandler(&called)).ServeHTTP(rec, newRequest(tt.p, tt.dbID))

			if called != tt.wantNext {
				t.Fatalf("next called = %v, want %v (err: %v)", called, tt.wantNext, rc.err)
			}
			if tt.wantNext {
				if rc.err != nil {
					t.Fatalf("ErrorHandler tidak boleh dipanggil, got %v", rc.err)
				}
				return
			}
			if rc.err == nil {
				t.Fatal("ErrorHandler harus dipanggil")
			}
			if got := apperr.KindOf(rc.err); got != tt.wantKind {
				t.Fatalf("kind = %v, want %v (err: %v)", got, tt.wantKind, rc.err)
			}
			if rec.Code != http.StatusTeapot {
				t.Fatalf("status = %d, want respons dari ErrorHandler", rec.Code)
			}
		})
	}
}

func TestMiddleware_RequireAction_DBIDPassedToGrantSource(t *testing.T) {
	g := &stubGrants{levels: []authz.Level{authz.LevelView}}
	mw := authz.NewMiddleware(authz.NewAuthorizer(g), nil)

	called := false
	mw.Protect(authz.ActDBView, okHandler(&called)).ServeHTTP(httptest.NewRecorder(), newRequest(&authz.Principal{UserID: 42}, "17"))

	if !called {
		t.Fatal("next harus dipanggil")
	}
	if g.gotUserID != 42 || g.gotDBID != 17 {
		t.Fatalf("GrantSource dipanggil dengan user=%d db=%d, want 42/17", g.gotUserID, g.gotDBID)
	}
}

func TestMiddleware_GrantErrorIsWrapped(t *testing.T) {
	rc := &recorder{}
	mw := authz.NewMiddleware(authz.NewAuthorizer(&stubGrants{err: errGrantDB}), rc.handle)
	mw.Protect(authz.ActDBView, okHandler(new(bool))).ServeHTTP(httptest.NewRecorder(), newRequest(&authz.Principal{UserID: 2}, "1"))

	if !errors.Is(rc.err, errGrantDB) {
		t.Fatalf("err = %v, want wrap %v", rc.err, errGrantDB)
	}
}

func TestMiddleware_RequireLogin(t *testing.T) {
	mw := authz.NewMiddleware(authz.NewAuthorizer(&stubGrants{}), nil)

	called := false
	rec := httptest.NewRecorder()
	mw.RequireLogin(okHandler(&called)).ServeHTTP(rec, newRequest(&authz.Principal{UserID: 1}, ""))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("logged in: called=%v status=%d, want true/200", called, rec.Code)
	}
}

func TestMiddleware_DefaultErrorHandler(t *testing.T) {
	mw := authz.NewMiddleware(authz.NewAuthorizer(&stubGrants{}), nil)

	tests := []struct {
		name       string
		p          *authz.Principal
		action     authz.Action
		dbID       string
		wantStatus int
	}{
		{"not logged in → redirect /login", nil, authz.ActDBView, "1", http.StatusSeeOther},
		{"forbidden → 403", &authz.Principal{UserID: 2}, authz.ActAdminManageUsers, "", http.StatusForbidden},
		{"bad dbID → 404", &authz.Principal{UserID: 2}, authz.ActDBView, "abc", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			rec := httptest.NewRecorder()
			mw.Protect(tt.action, okHandler(&called)).ServeHTTP(rec, newRequest(tt.p, tt.dbID))

			if called {
				t.Fatal("next tidak boleh dipanggil")
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusSeeOther {
				if loc := rec.Header().Get("Location"); loc != "/login" {
					t.Fatalf("Location = %q, want /login", loc)
				}
			}
		})
	}
}
