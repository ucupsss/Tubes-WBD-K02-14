package databases

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
}

// RegisterRoutes mendaftarkan seluruh endpoint admin pengelolaan target database.
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	mux.Handle("GET /admin/databases", mw.Protect(authz.ActAdminManageConnections, h.List))
	mux.Handle("GET /admin/databases/new", mw.Protect(authz.ActAdminManageConnections, h.New))
	mux.Handle("POST /admin/databases", mw.Protect(authz.ActAdminManageConnections, h.Create))
	mux.Handle("GET /admin/databases/{id}/edit", mw.Protect(authz.ActAdminManageConnections, h.Edit))
	mux.Handle("POST /admin/databases/{id}/edit", mw.Protect(authz.ActAdminManageConnections, h.Update))
	mux.Handle("POST /admin/databases/{id}/delete", mw.Protect(authz.ActAdminManageConnections, h.Delete))
	mux.Handle("POST /admin/databases/{id}/test", mw.Protect(authz.ActAdminManageConnections, h.Test))
}
