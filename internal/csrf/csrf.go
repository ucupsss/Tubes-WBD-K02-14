package csrf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
)

const (
	// FormField adalah nama hidden field token di setiap form POST.
	FormField = "_csrf"
	// HeaderName dipakai bila token dikirim lewat header (mis. fetch dari JS).
	HeaderName = "X-CSRF-Token"
	// PreCookieName adalah cookie double-submit untuk form sebelum login (belum ada session).
	PreCookieName = "dbmp_csrf_pre"

	tokenBytes = 32
)

// tokenLen = panjang base64url tanpa padding dari tokenBytes.
var tokenLen = base64.RawURLEncoding.EncodedLen(tokenBytes)

type ctxKey struct{}

// Token mengembalikan token CSRF untuk request ini, dipakai view.Render untuk mengisi PageData.CSRFToken.
// Sudah login → token synchronizer milik session. Belum login → token pre-session yang disiapkan Verify.
func Token(r *http.Request) string {
	if s, ok := session.FromContext(r.Context()); ok {
		return s.CSRFToken
	}
	if t, ok := r.Context().Value(ctxKey{}).(string); ok {
		return t
	}
	return ""
}

func withPreToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, ctxKey{}, token)
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("csrf.newToken: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// equal membandingkan token secara constant-time. Token kosong selalu ditolak
// (ConstantTimeCompare("", "") bernilai 1).
func equal(expected, got string) bool {
	if expected == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(got)) == 1
}

// submittedToken mengambil token dari header, lalu dari field form.
func submittedToken(r *http.Request) string {
	if t := r.Header.Get(HeaderName); t != "" {
		return t
	}
	return r.PostFormValue(FormField)
}
