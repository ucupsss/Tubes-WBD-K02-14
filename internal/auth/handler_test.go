package auth_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// stubSessions mencatat panggilan handler ke session manager.
type stubSessions struct {
	created   []session.User
	revoked   []int64
	setTokens []string
	cleared   int
	createErr error
}

func (s *stubSessions) Create(ctx context.Context, u session.User) (string, session.Session, error) {
	if s.createErr != nil {
		return "", session.Session{}, s.createErr
	}
	s.created = append(s.created, u)
	return "new-token", session.Session{ID: 99, UserID: u.ID}, nil
}

func (s *stubSessions) Revoke(ctx context.Context, id int64) error {
	s.revoked = append(s.revoked, id)
	return nil
}

func (s *stubSessions) SetCookie(w http.ResponseWriter, token string) {
	s.setTokens = append(s.setTokens, token)
	http.SetCookie(w, &http.Cookie{Name: session.CookieName, Value: token})
}

func (s *stubSessions) ClearCookie(w http.ResponseWriter) { s.cleared++ }

func newHandler(repo *stubRepo) (*auth.Handler, *stubSessions) {
	sess := &stubSessions{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return auth.NewHandler(auth.NewService(nil, repo), sess, view.New(), logger), sess
}

func postLogin(username, password string) *http.Request {
	form := url.Values{"username": {username}, "password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestHandler_ShowLogin(t *testing.T) {
	h, _ := newHandler(newStubRepo())

	rec := httptest.NewRecorder()
	h.ShowLogin(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("anonim: status = %d, want 200", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 10}))
	rec = httptest.NewRecorder()
	h.ShowLogin(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("sudah login: status=%d loc=%q, want 303 /", rec.Code, rec.Header().Get("Location"))
	}
}

func TestHandler_Login_Success(t *testing.T) {
	h, sess := newHandler(newStubRepo(alice(true)))

	rec := httptest.NewRecorder()
	h.Login(rec, postLogin("alice", "password123"))

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("status=%d loc=%q, want 303 /", rec.Code, rec.Header().Get("Location"))
	}
	if len(sess.created) != 1 || sess.created[0].ID != 10 || !sess.created[0].IsActive {
		t.Fatalf("session harus dibuat untuk alice, got %+v", sess.created)
	}
	if len(sess.setTokens) != 1 || sess.setTokens[0] != "new-token" {
		t.Fatalf("cookie harus di-set dengan token baru, got %v", sess.setTokens)
	}
	if loc := rec.Header().Get("Location"); strings.Contains(loc, "token") || strings.Contains(loc, "password") {
		t.Fatalf("token/password tidak boleh ada di URL: %q", loc)
	}
	body := rec.Body.String()
	if strings.Contains(body, "password123") || strings.Contains(body, "new-token") {
		t.Fatal("password/token tidak boleh ada di body")
	}
}

func TestHandler_Login_Failures(t *testing.T) {
	tests := []struct {
		name     string
		repo     *stubRepo
		username string
		password string
	}{
		{"wrong password", newStubRepo(alice(true)), "alice", "salah12345"},
		{"unknown user", newStubRepo(alice(true)), "mallory", "password123"},
		{"inactive user", newStubRepo(alice(false)), "alice", "password123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, sess := newHandler(tt.repo)
			rec := httptest.NewRecorder()
			h.Login(rec, postLogin(tt.username, tt.password))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if len(sess.created) != 0 || len(sess.setTokens) != 0 {
				t.Fatal("login gagal tidak boleh membuat session / cookie")
			}
		})
	}
}

func TestHandler_Login_DBError(t *testing.T) {
	h, sess := newHandler(&stubRepo{err: errDB})
	rec := httptest.NewRecorder()
	h.Login(rec, postLogin("alice", "password123"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), errDB.Error()) {
		t.Fatal("detail error DB tidak boleh tampil ke user")
	}
	if len(sess.setTokens) != 0 {
		t.Fatal("tidak boleh ada cookie")
	}
}

func TestHandler_Login_SessionCreateError(t *testing.T) {
	h, sess := newHandler(newStubRepo(alice(true)))
	sess.createErr = errDB
	rec := httptest.NewRecorder()
	h.Login(rec, postLogin("alice", "password123"))

	if rec.Code != http.StatusInternalServerError || len(sess.setTokens) != 0 {
		t.Fatalf("status=%d cookies=%v, want 500 tanpa cookie", rec.Code, sess.setTokens)
	}
}

func TestHandler_Login_RevokesPreviousSession(t *testing.T) {
	h, sess := newHandler(newStubRepo(alice(true)))
	req := postLogin("alice", "password123")
	req = req.WithContext(session.WithSession(req.Context(), session.Session{ID: 7, UserID: 11}))

	h.Login(httptest.NewRecorder(), req)
	if len(sess.revoked) != 1 || sess.revoked[0] != 7 {
		t.Fatalf("session lama harus di-revoke, got %v", sess.revoked)
	}
}

func TestHandler_Logout(t *testing.T) {
	h, sess := newHandler(newStubRepo())
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req = req.WithContext(session.WithSession(req.Context(), session.Session{ID: 42, UserID: 10}))

	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("status=%d loc=%q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
	if len(sess.revoked) != 1 || sess.revoked[0] != 42 {
		t.Fatalf("session harus di-revoke di backend, got %v", sess.revoked)
	}
	if sess.cleared != 1 {
		t.Fatal("cookie harus dihapus")
	}
}

func TestService_LookupUser(t *testing.T) {
	svc := auth.NewService(nil, newStubRepo(alice(true)))
	u, err := svc.LookupUser(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 10 || u.Username != "alice" || !u.IsActive || u.PrivilegeVersion != 1 {
		t.Fatalf("session user salah: %+v", u)
	}
	if _, err := svc.LookupUser(context.Background(), 999); err != auth.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

var _ auth.Sessions = (*session.Manager)(nil)
