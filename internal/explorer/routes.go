package explorer

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
	RequireLogin(next http.Handler) http.Handler
}

// RegisterRoutes mendaftarkan seluruh endpoint REST JSON untuk Dashboard dan Database Explorer (Milestone 2, B3).
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	mux.Handle("GET /api/databases", mw.RequireLogin(http.HandlerFunc(h.ListDatabases)))
	mux.Handle("GET /api/databases/{dbID}/schema", mw.Protect(authz.ActDBView, h.Schema))
	mux.Handle("GET /api/databases/{dbID}/tables/{schema}/{table}", mw.Protect(authz.ActDBView, h.TableDetails))
	mux.Handle("GET /api/databases/{dbID}/tables/{schema}/{table}/data", mw.Protect(authz.ActDBView, h.TableData))
}
