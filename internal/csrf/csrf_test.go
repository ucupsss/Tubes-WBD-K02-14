package csrf

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
)

const (
	host         = "example.test"
	sessionToken = "session-csrf-token"
)

// preToken berformat valid (43 karakter base64url).
var preToken = strings.Repeat("a", tokenLen)

type result struct {
	called bool
	token  string // csrf.Token(r) yang dilihat handler
}

func run(t *testing.T, cfg Config, req *http.Request) (*httptest.ResponseRecorder, result) {
	t.Helper()
	var res result
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res.called = true
		res.token = Token(r)
	})
	rec := httptest.NewRecorder()
	NewMiddleware(cfg, nil).Verify(next).ServeHTTP(rec, req)
	return rec, res
}

func newRequest(method, field string) *http.Request {
	var body *strings.Reader
	if field != "" {
		body = strings.NewReader(url.Values{FormField: {field}}.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "http://"+host+"/something", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func loggedIn(req *http.Request) *http.Request {
	return req.WithContext(session.WithSession(req.Context(), session.Session{ID: 1, UserID: 10, CSRFToken: sessionToken}))
}

func preCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == PreCookieName {
			return c
		}
	}
	return nil
}

func TestVerify_LoggedIn(t *testing.T) {
	tests := []struct {
		name   string
		method string
		field  string
		header string
		want   bool
	}{
		{"POST field benar", http.MethodPost, sessionToken, "", true},
		{"POST header benar", http.MethodPost, "", sessionToken, true},
		{"POST tanpa token", http.MethodPost, "", "", false},
		{"POST token salah", http.MethodPost, "salah", "", false},
		{"POST token pre-session ditolak", http.MethodPost, preToken, "", false},
		{"header salah walau field benar", http.MethodPost, sessionToken, "salah", false},
		{"PUT tanpa token", http.MethodPut, "", "", false},
		{"PATCH tanpa token", http.MethodPatch, "", "", false},
		{"DELETE tanpa token", http.MethodDelete, "", "", false},
		{"DELETE header benar", http.MethodDelete, "", sessionToken, true},
		{"GET tanpa token", http.MethodGet, "", "", true},
		{"HEAD tanpa token", http.MethodHead, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := loggedIn(newRequest(tt.method, tt.field))
			if tt.header != "" {
				req.Header.Set(HeaderName, tt.header)
			}
			rec, res := run(t, Config{}, req)

			if res.called != tt.want {
				t.Fatalf("diteruskan = %v, want %v (status %d)", res.called, tt.want, rec.Code)
			}
			if !tt.want && rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			if tt.want && res.token != sessionToken {
				t.Fatalf("Token(r) = %q, want token session", res.token)
			}
			if preCookie(rec) != nil {
				t.Fatal("sudah login: tidak perlu cookie pre-session")
			}
		})
	}
}

func TestVerify_LoggedIn_EmptySessionToken(t *testing.T) {
	req := newRequest(http.MethodPost, "")
	req = req.WithContext(session.WithSession(req.Context(), session.Session{ID: 1}))
	rec, res := run(t, Config{}, req)
	if res.called || rec.Code != http.StatusForbidden {
		t.Fatal("token kosong vs token kosong tidak boleh dianggap cocok")
	}
}

func TestVerify_PreSession_GetIssuesCookie(t *testing.T) {
	for _, secure := range []bool{false, true} {
		rec, res := run(t, Config{Secure: secure}, newRequest(http.MethodGet, ""))

		ck := preCookie(rec)
		if ck == nil {
			t.Fatal("GET tanpa session harus menerbitkan cookie pre-session")
		}
		if len(ck.Value) != tokenLen || !validFormat(ck.Value) {
			t.Fatalf("format token salah: %q", ck.Value)
		}
		if !ck.HttpOnly || ck.Path != "/" || ck.SameSite != http.SameSiteStrictMode || ck.Secure != secure {
			t.Fatalf("secure=%v: atribut cookie salah: %+v", secure, ck)
		}
		if !res.called || res.token != ck.Value {
			t.Fatalf("Token(r) = %q, want sama dengan cookie %q", res.token, ck.Value)
		}
	}
}

func TestVerify_PreSession_TokenUnique(t *testing.T) {
	rec1, _ := run(t, Config{}, newRequest(http.MethodGet, ""))
	rec2, _ := run(t, Config{}, newRequest(http.MethodGet, ""))
	if preCookie(rec1).Value == preCookie(rec2).Value {
		t.Fatal("token pre-session harus acak")
	}
}

func TestVerify_PreSession_ReusesValidCookie(t *testing.T) {
	req := newRequest(http.MethodGet, "")
	req.AddCookie(&http.Cookie{Name: PreCookieName, Value: preToken})
	rec, res := run(t, Config{}, req)

	if preCookie(rec) != nil {
		t.Fatal("cookie valid tidak perlu diterbitkan ulang")
	}
	if res.token != preToken {
		t.Fatalf("Token(r) = %q, want token dari cookie", res.token)
	}
}

func TestVerify_PreSession_ReplacesMalformedCookie(t *testing.T) {
	req := newRequest(http.MethodGet, "")
	req.AddCookie(&http.Cookie{Name: PreCookieName, Value: "pendek"})
	rec, res := run(t, Config{}, req)

	ck := preCookie(rec)
	if ck == nil || ck.Value == "pendek" || res.token != ck.Value {
		t.Fatalf("cookie rusak harus diganti, got %+v token=%q", ck, res.token)
	}
}

func TestVerify_PreSession_Post(t *testing.T) {
	origin := "http://" + host
	tests := []struct {
		name    string
		cookie  string
		field   string
		origin  string
		referer string
		want    bool
	}{
		{"origin + cookie cocok", preToken, preToken, origin, "", true},
		{"referer + cookie cocok", preToken, preToken, "", origin + "/login", true},
		{"origin beda host", preToken, preToken, "http://evil.test", "", false},
		{"referer beda host", preToken, preToken, "", "http://evil.test/login", false},
		{"origin beda port", preToken, preToken, "http://" + host + ":8080", "", false},
		{"origin null", preToken, preToken, "null", "", false},
		{"tanpa origin & referer", preToken, preToken, "", "", false},
		{"origin menang atas referer", preToken, preToken, "http://evil.test", origin + "/login", false},
		{"tanpa cookie", "", preToken, origin, "", false},
		{"tanpa field", preToken, "", origin, "", false},
		{"field beda dengan cookie", preToken, strings.Repeat("b", tokenLen), origin, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newRequest(http.MethodPost, tt.field)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: PreCookieName, Value: tt.cookie})
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}
			rec, res := run(t, Config{}, req)

			if res.called != tt.want {
				t.Fatalf("diteruskan = %v, want %v (status %d)", res.called, tt.want, rec.Code)
			}
			if !tt.want && rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestVerify_FormStillReadableByHandler(t *testing.T) {
	form := url.Values{FormField: {sessionToken}, "name": {"budi"}}
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = loggedIn(req)

	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.FormValue("name") })
	NewMiddleware(Config{}, nil).Verify(next).ServeHTTP(httptest.NewRecorder(), req)
	if got != "budi" {
		t.Fatalf("handler harus tetap bisa membaca form, got %q", got)
	}
}

func TestVerify_UsesErrorHandler(t *testing.T) {
	var gotErr error
	onError := func(w http.ResponseWriter, r *http.Request, err error) {
		gotErr = err
		w.WriteHeader(http.StatusTeapot)
	}
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("tidak boleh diteruskan") })
	NewMiddleware(Config{}, onError).Verify(next).ServeHTTP(rec, loggedIn(newRequest(http.MethodPost, "")))

	if gotErr != ErrInvalidToken || rec.Code != http.StatusTeapot {
		t.Fatalf("ErrorHandler harus dipanggil dengan ErrInvalidToken, got %v / %d", gotErr, rec.Code)
	}
}

func TestToken_NoContext(t *testing.T) {
	if got := Token(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Fatalf("tanpa session & tanpa Verify: Token = %q, want kosong", got)
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "ab", false},
		{"", "", false},
		{"abc", "", false},
		{"", "abc", false},
	}
	for _, tt := range tests {
		if got := equal(tt.a, tt.b); got != tt.want {
			t.Errorf("equal(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
