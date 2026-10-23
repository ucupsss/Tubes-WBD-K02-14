package authz

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

var (
	ErrUnauthenticated = &apperr.Error{Kind: apperr.KindUnauthenticated, Msg: "Silakan login terlebih dahulu"}
	ErrForbidden       = apperr.Forbidden("Anda tidak memiliki akses")
	ErrInvalidDBID     = apperr.NotFound("Database tidak ditemukan")
)

// ErrorHandler menampilkan error ke user. Di main.go diisi httpx.HandleError.
// Dibuat sebagai fungsi karena authz tidak boleh meng-import httpx/view (import cycle).
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// Middleware memasang pengecekan login & policy ke route.
type Middleware struct {
	authz   Authorizer
	onError ErrorHandler
}

// NewMiddleware membuat Middleware. onError nil → fallback teks polos (cukup untuk test).
func NewMiddleware(a Authorizer, onError ErrorHandler) *Middleware {
	if onError == nil {
		onError = plainError
	}
	return &Middleware{authz: a, onError: onError}
}

// RequireLogin menolak request tanpa Principal (belum login) → redirect /login lewat ErrorHandler.
func (m *Middleware) RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFrom(r.Context()); !ok {
			m.onError(w, r, ErrUnauthenticated)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAction memeriksa aksi terhadap policy. Untuk aksi db.*, dbID dibaca dari r.PathValue("dbID").
func (m *Middleware) RequireAction(act Action, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			m.onError(w, r, ErrUnauthenticated)
			return
		}

		var dbID int64
		if req, known := RequirementFor(act); known && req.NeedsDatabase() {
			id, err := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
			if err != nil || id <= 0 {
				m.onError(w, r, ErrInvalidDBID)
				return
			}
			dbID = id
		}

		allowed, err := m.authz.Can(r.Context(), p, act, dbID)
		if err != nil {
			m.onError(w, r, fmt.Errorf("authz.RequireAction %s: %w", act, err))
			return
		}
		if !allowed {
			m.onError(w, r, ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Protect = RequireLogin + RequireAction. Setiap route non-publik WAJIB lewat sini.
func (m *Middleware) Protect(act Action, h http.HandlerFunc) http.Handler {
	return m.RequireLogin(m.RequireAction(act, h))
}

// plainError adalah fallback bila ErrorHandler tidak diberikan.
func plainError(w http.ResponseWriter, r *http.Request, err error) {
	switch apperr.KindOf(err) {
	case apperr.KindUnauthenticated:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	case apperr.KindForbidden:
		http.Error(w, ErrForbidden.Msg, http.StatusForbidden)
	case apperr.KindNotFound:
		http.Error(w, ErrInvalidDBID.Msg, http.StatusNotFound)
	default:
		http.Error(w, "Terjadi kesalahan pada server", http.StatusInternalServerError)
	}
}
