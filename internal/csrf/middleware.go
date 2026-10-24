package csrf

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
)

var ErrInvalidToken = apperr.Forbidden("Permintaan ditolak: token keamanan tidak valid. Muat ulang halaman lalu coba lagi.")

// ErrorHandler menampilkan error ke user. Di main.go diisi httpx.HandleError.
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// Config: Secure mengikuti cookie session (true di production).
type Config struct {
	Secure bool
}

// Middleware memverifikasi token CSRF untuk semua request yang mengubah state.
type Middleware struct {
	cfg     Config
	onError ErrorHandler
}

// NewMiddleware membuat Middleware. onError nil → fallback 403/500 teks polos (cukup untuk test).
func NewMiddleware(cfg Config, onError ErrorHandler) *Middleware {
	if onError == nil {
		onError = func(w http.ResponseWriter, r *http.Request, err error) {
			if apperr.KindOf(err) == apperr.KindForbidden {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			http.Error(w, "Terjadi kesalahan pada server", http.StatusInternalServerError)
		}
	}
	return &Middleware{cfg: cfg, onError: onError}
}

// Verify dipasang global setelah session.Load.
//   - GET/HEAD/OPTIONS/TRACE: tidak dicek. Bila belum login, siapkan token pre-session (cookie dbmp_csrf_pre)
//     supaya form login bisa mengisi _csrf.
//   - Method lain, sudah login: token (header X-CSRF-Token / field _csrf) harus sama dengan sessions.csrf_token.
//   - Method lain, belum login: Origin/Referer harus satu host, dan token harus sama dengan cookie dbmp_csrf_pre.
func (m *Middleware) Verify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, loggedIn := session.FromContext(r.Context())

		if isSafeMethod(r.Method) {
			if loggedIn {
				next.ServeHTTP(w, r)
				return
			}
			token, err := m.ensurePreToken(w, r)
			if err != nil {
				m.onError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(withPreToken(r.Context(), token)))
			return
		}

		if !m.validUnsafe(r, loggedIn) {
			m.onError(w, r, ErrInvalidToken)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) validUnsafe(r *http.Request, loggedIn bool) bool {
	if loggedIn {
		s, _ := session.FromContext(r.Context())
		return equal(s.CSRFToken, submittedToken(r))
	}
	if !sameOrigin(r) {
		return false
	}
	c, err := r.Cookie(PreCookieName)
	if err != nil {
		return false
	}
	return equal(c.Value, submittedToken(r))
}

// ensurePreToken memakai ulang cookie pre-session yang formatnya valid, atau membuat yang baru.
func (m *Middleware) ensurePreToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if c, err := r.Cookie(PreCookieName); err == nil && validFormat(c.Value) {
		return c.Value, nil
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     PreCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func validFormat(token string) bool {
	if len(token) != tokenLen {
		return false
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// sameOrigin: host di header Origin (atau Referer bila Origin tidak ada) harus sama dengan host request.
// Tanpa keduanya → ditolak.
func sameOrigin(r *http.Request) bool {
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	if src == "" || src == "null" {
		return false
	}
	u, err := url.Parse(src)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
