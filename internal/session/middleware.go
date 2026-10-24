package session

import (
	"context"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type ctxKey struct{}

// WithSession menyisipkan Session ke context (dipakai Load, dan oleh test csrf/handler).
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext mengambil Session request saat ini (mis. untuk CSRF token dan logout).
func FromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(ctxKey{}).(Session)
	return s, ok
}

// SetCookie menulis cookie session: HttpOnly, SameSite=Lax, Path=/, Secure di production,
// tanpa Expires (cookie hilang saat browser ditutup; masa berlaku diatur backend).
func (m *Manager) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie menghapus cookie session di browser.
func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ErrorHandler menampilkan error ke user. Di main.go diisi httpx.HandleError.
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// Middleware memasang session ke setiap request.
type Middleware struct {
	mgr     *Manager
	onError ErrorHandler
}

func NewMiddleware(mgr *Manager, onError ErrorHandler) *Middleware {
	if onError == nil {
		onError = func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "Terjadi kesalahan pada server", http.StatusInternalServerError)
		}
	}
	return &Middleware{mgr: mgr, onError: onError}
}

// Load dipasang global. Session valid → Principal & Session masuk context (dan cookie dirotasi bila perlu).
// Session tidak valid → cookie dihapus dan request diteruskan sebagai anonim; route yang butuh login
// akan di-redirect oleh authz.RequireLogin.
func (mw *Middleware) Load(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		res, err := mw.mgr.Resolve(r.Context(), c.Value)
		if IsInvalid(err) {
			mw.mgr.ClearCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			mw.onError(w, r, err)
			return
		}

		if res.NewToken != "" {
			mw.mgr.SetCookie(w, res.NewToken)
		}
		ctx := WithSession(r.Context(), res.Session)
		ctx = authz.WithPrincipal(ctx, authz.Principal{
			UserID:   res.User.ID,
			Username: res.User.Username,
			IsAdmin:  res.User.IsAdmin,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
