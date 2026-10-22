package access

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
}

// RegisterRoutes mendaftarkan endpoint admin pengelolaan hak akses grup ke database target.
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	mux.Handle("GET /admin/groups/{id}/access", mw.Protect(authz.ActAdminManageGrants, h.Show))
	mux.Handle("POST /admin/groups/{id}/access", mw.Protect(authz.ActAdminManageGrants, h.Update))
}
