package session

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type captured struct {
	called    bool
	principal authz.Principal
	hasP      bool
	session   Session
	hasS      bool
}

func capture(c *captured) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.called = true
		c.principal, c.hasP = authz.PrincipalFrom(r.Context())
		c.session, c.hasS = FromContext(r.Context())
	})
}

func requestWithCookie(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	}
	return req
}

func findCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	return nil
}

func TestMiddleware_Load_NoCookie(t *testing.T) {
	f := newFixture()
	var c captured
	rec := httptest.NewRecorder()
	NewMiddleware(f.mgr, nil).Load(capture(&c)).ServeHTTP(rec, requestWithCookie(""))

	if !c.called || c.hasP || c.hasS {
		t.Fatalf("tanpa cookie: harus diteruskan sebagai anonim, got %+v", c)
	}
	if findCookie(rec) != nil {
		t.Fatal("tidak boleh ada Set-Cookie")
	}
}

func TestMiddleware_Load_ValidSession(t *testing.T) {
	f := newFixture()
	token, s := f.login(t, aliceID)

	var c captured
	rec := httptest.NewRecorder()
	NewMiddleware(f.mgr, nil).Load(capture(&c)).ServeHTTP(rec, requestWithCookie(token))

	if !c.called || !c.hasP || !c.hasS {
		t.Fatalf("session valid harus menaruh Principal & Session di context, got %+v", c)
	}
	if c.principal.UserID != aliceID || c.principal.Username != "alice" || c.principal.IsAdmin {
		t.Fatalf("principal salah: %+v", c.principal)
	}
	if c.session.ID != s.ID || c.session.CSRFToken != s.CSRFToken {
		t.Fatal("session di context salah")
	}
	if findCookie(rec) != nil {
		t.Fatal("tanpa rotasi tidak boleh ada Set-Cookie")
	}
}

func TestMiddleware_Load_InvalidSessionClearsCookie(t *testing.T) {
	f := newFixture()
	var c captured
	rec := httptest.NewRecorder()
	NewMiddleware(f.mgr, nil).Load(capture(&c)).ServeHTTP(rec, requestWithCookie("token-palsu"))

	if !c.called || c.hasP {
		t.Fatal("session invalid harus diteruskan sebagai anonim")
	}
	ck := findCookie(rec)
	if ck == nil || ck.MaxAge >= 0 || ck.Value != "" {
		t.Fatalf("cookie harus dihapus, got %+v", ck)
	}
}

func TestMiddleware_Load_RotationSetsCookie(t *testing.T) {
	f := newFixture()
	token, _ := f.login(t, aliceID)
	f.clk.Advance(15 * time.Minute)

	var c captured
	rec := httptest.NewRecorder()
	NewMiddleware(f.mgr, nil).Load(capture(&c)).ServeHTTP(rec, requestWithCookie(token))

	ck := findCookie(rec)
	if ck == nil || ck.Value == "" || ck.Value == token {
		t.Fatalf("rotasi harus mengirim cookie dengan nilai baru, got %+v", ck)
	}
	if !c.called || !c.hasP {
		t.Fatal("request harus tetap dilayani")
	}
}

func TestMiddleware_Load_DBError(t *testing.T) {
	f := newFixture()
	token, _ := f.login(t, aliceID)
	f.repo.err = errDB

	var gotErr error
	onError := func(w http.ResponseWriter, r *http.Request, err error) {
		gotErr = err
		w.WriteHeader(http.StatusInternalServerError)
	}
	var c captured
	rec := httptest.NewRecorder()
	NewMiddleware(f.mgr, onError).Load(capture(&c)).ServeHTTP(rec, requestWithCookie(token))

	if c.called {
		t.Fatal("DB error tidak boleh diteruskan sebagai anonim")
	}
	if gotErr == nil || rec.Code != http.StatusInternalServerError {
		t.Fatalf("ErrorHandler harus dipanggil, got err=%v status=%d", gotErr, rec.Code)
	}
}

func TestManager_CookieAttributes(t *testing.T) {
	for _, secure := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.Secure = secure
		mgr := NewManager(nil, newMemRepo(), &stubUsers{}, &fakeClock{t: t0}, cfg)

		rec := httptest.NewRecorder()
		mgr.SetCookie(rec, "tok")
		ck := findCookie(rec)
		if ck == nil {
			t.Fatal("cookie tidak ter-set")
		}
		if ck.Value != "tok" || ck.Path != "/" || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Secure != secure {
			t.Fatalf("secure=%v: atribut cookie salah: %+v", secure, ck)
		}
		if !ck.Expires.IsZero() || ck.MaxAge != 0 {
			t.Fatalf("cookie session tidak boleh punya Expires/MaxAge, got %+v", ck)
		}
	}
}
