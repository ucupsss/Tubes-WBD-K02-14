package users

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
}

// RegisterRoutes mendaftarkan halaman admin user. Tidak ada route hapus (D8).
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	mux.Handle("GET /admin/users", mw.Protect(authz.ActAdminManageUsers, h.List))
	mux.Handle("GET /admin/users/new", mw.Protect(authz.ActAdminManageUsers, h.New))
	mux.Handle("POST /admin/users", mw.Protect(authz.ActAdminManageUsers, h.Create))
	mux.Handle("GET /admin/users/{id}/edit", mw.Protect(authz.ActAdminManageUsers, h.Edit))
	mux.Handle("POST /admin/users/{id}/edit", mw.Protect(authz.ActAdminManageUsers, h.Update))
	mux.Handle("POST /admin/users/{id}/password", mw.Protect(authz.ActAdminManageUsers, h.ResetPassword))
}
