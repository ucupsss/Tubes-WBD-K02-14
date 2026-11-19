package explorer

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
	RequireLogin(next http.Handler) http.Handler
}

// RegisterRoutes mendaftarkan seluruh route untuk Dashboard dan Database Explorer.
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	// Dashboard user (daftar database yang berhak diakses)
	// Dashboard (GET /) kini halaman SPA; handler HTML Dashboard dihapus oleh B saat API explorer selesai.

	// Explorer target database (membutuhkan minimal hak level view)
	mux.Handle("GET /db/{dbID}", mw.Protect(authz.ActDBView, h.Overview))
	mux.Handle("GET /db/{dbID}/tables/{schema}/{table}", mw.Protect(authz.ActDBView, h.TableDetails))
	mux.Handle("GET /db/{dbID}/tables/{schema}/{table}/data", mw.Protect(authz.ActDBView, h.TableData))
}
