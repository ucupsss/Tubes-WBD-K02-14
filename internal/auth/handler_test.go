package auth_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
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
