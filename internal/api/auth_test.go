package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/api"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
)

var errDB = errors.New("platform db down: password=hunter2")

// stubRepo adalah auth.Repo di memori.
type stubRepo struct {
	users map[string]auth.User
	err   error
}

func (r *stubRepo) GetByID(context.Context, platformdb.DBTX, int64) (auth.User, error) {
	return auth.User{}, auth.ErrNotFound
}
func (r *stubRepo) GetByUsername(_ context.Context, _ platformdb.DBTX, name string) (auth.User, error) {
	if r.err != nil {
		return auth.User{}, r.err
	}
	u, ok := r.users[name]
	if !ok {
		return auth.User{}, auth.ErrNotFound
	}
	return u, nil
}
func (r *stubRepo) Create(context.Context, platformdb.DBTX, auth.User) (int64, error) { return 0, nil }
func (r *stubRepo) HasAnyUser(context.Context, platformdb.DBTX) (bool, error)         { return true, nil }
func (r *stubRepo) CreateAdminIfNoUsers(context.Context, platformdb.DBTX, string, string) (bool, error) {
	return false, nil
}

type stubSessions struct {
	created   []session.User
	revoked   []int64
	setTokens []string
	cleared   int
	createErr error
}

func (s *stubSessions) Create(_ context.Context, u session.User) (string, session.Session, error) {
	if s.createErr != nil {
		return "", session.Session{}, s.createErr
	}
	s.created = append(s.created, u)
	return "new-token", session.Session{ID: 99, UserID: u.ID, CSRFToken: "csrf-for-new-session"}, nil
}
func (s *stubSessions) Revoke(_ context.Context, id int64) error {
	s.revoked = append(s.revoked, id)
	return nil
}
func (s *stubSessions) SetCookie(w http.ResponseWriter, token string) {
	s.setTokens = append(s.setTokens, token)
	http.SetCookie(w, &http.Cookie{Name: session.CookieName, Value: token})
}
func (s *stubSessions) ClearCookie(http.ResponseWriter) { s.cleared++ }

var hashOnce = sync.OnceValue(func() string {
	h, err := auth.HashPassword("password123")
	if err != nil {
		panic(err)
	}
	return h
})

func alice(active bool) auth.User {
	return auth.User{ID: 10, Username: "alice", PasswordHash: hashOnce(), IsActive: active, IsAdmin: true}
}

func newHandler(repo *stubRepo) (*api.Handler, *stubSessions) {
	sess := &stubSessions{}
	return api.New(api.Deps{
		Auth:     auth.NewService(nil, repo),
		Sessions: sess,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}), sess
}

func loginReq(username, password string) *http.Request {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestLogin_Success(t *testing.T) {
	h, sess := newHandler(&stubRepo{users: map[string]auth.User{"alice": alice(true)}})
	rec := httptest.NewRecorder()
	h.Login(rec, loginReq("alice", "password123"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(sess.created) != 1 || sess.created[0].ID != 10 || !sess.created[0].IsActive {
		t.Fatalf("session harus dibuat untuk alice, got %+v", sess.created)
	}
	if len(sess.setTokens) != 1 || sess.setTokens[0] != "new-token" {
		t.Fatalf("cookie harus di-set dengan token baru, got %v", sess.setTokens)
	}
	var out struct {
		User struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			IsAdmin  bool   `json:"is_admin"`
		} `json:"user"`
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.User.Username != "alice" || !out.User.IsAdmin || out.CSRF != "csrf-for-new-session" {
		t.Fatalf("body = %+v", out)
	}
	body := rec.Body.String()
	for _, secret := range []string{"password123", "new-token", "password_hash", "$2a$"} {
		if strings.Contains(body, secret) {
			t.Fatalf("body membocorkan %q: %s", secret, body)
		}
	}
}

func TestLogin_Failures(t *testing.T) {
	tests := []struct {
		name, user, pass string
		repo             *stubRepo
	}{
		{"password salah", "alice", "salah12345", &stubRepo{users: map[string]auth.User{"alice": alice(true)}}},
		{"user tidak ada", "mallory", "password123", &stubRepo{users: map[string]auth.User{"alice": alice(true)}}},
		{"user nonaktif", "alice", "password123", &stubRepo{users: map[string]auth.User{"alice": alice(false)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, sess := newHandler(tt.repo)
			rec := httptest.NewRecorder()
			h.Login(rec, loginReq(tt.user, tt.pass))

			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"invalid_credentials"`) {
				t.Fatalf("status=%d body=%s, want 401 invalid_credentials", rec.Code, rec.Body.String())
			}
			if len(sess.created) != 0 || len(sess.setTokens) != 0 {
				t.Fatal("login gagal tidak boleh membuat session / cookie")
			}
		})
	}
}

func TestLogin_BadBody(t *testing.T) {
	h, sess := newHandler(&stubRepo{})
	for name, body := range map[string]string{
		"kosong":        "",
		"bukan json":    "username=alice",
		"field asing":   `{"username":"a","password":"b","admin":true}`,
		"json berlebih": `{"username":"a","password":"b"}{"x":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
			rec := httptest.NewRecorder()
			h.Login(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
	if len(sess.created) != 0 {
		t.Fatal("tidak boleh ada session dari body buruk")
	}
}

func TestLogin_DBErrorIsGeneric500(t *testing.T) {
	h, sess := newHandler(&stubRepo{err: errDB})
	rec := httptest.NewRecorder()
	h.Login(rec, loginReq("alice", "password123"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "platform db") {
		t.Fatalf("detail error internal tidak boleh tampil: %s", rec.Body.String())
	}
	if len(sess.setTokens) != 0 {
		t.Fatal("tidak boleh ada cookie")
	}
}

func TestLogin_SessionCreateError(t *testing.T) {
	h, sess := newHandler(&stubRepo{users: map[string]auth.User{"alice": alice(true)}})
	sess.createErr = errDB
	rec := httptest.NewRecorder()
	h.Login(rec, loginReq("alice", "password123"))

	if rec.Code != http.StatusInternalServerError || len(sess.setTokens) != 0 {
		t.Fatalf("status=%d cookies=%v, want 500 tanpa cookie", rec.Code, sess.setTokens)
	}
}

func TestLogin_RevokesPreviousSession(t *testing.T) {
	h, sess := newHandler(&stubRepo{users: map[string]auth.User{"alice": alice(true)}})
	req := loginReq("alice", "password123")
	req = req.WithContext(session.WithSession(req.Context(), session.Session{ID: 7, UserID: 11}))

	h.Login(httptest.NewRecorder(), req)
	if len(sess.revoked) != 1 || sess.revoked[0] != 7 {
		t.Fatalf("session lama harus di-revoke, got %v", sess.revoked)
	}
}

func TestLogout_RevokesAndClears(t *testing.T) {
	h, sess := newHandler(&stubRepo{})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req = req.WithContext(session.WithSession(req.Context(), session.Session{ID: 42, UserID: 10}))
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(sess.revoked) != 1 || sess.revoked[0] != 42 || sess.cleared != 1 {
		t.Fatalf("revoked=%v cleared=%d", sess.revoked, sess.cleared)
	}
}
