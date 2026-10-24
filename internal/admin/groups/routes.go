package groups

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
}

// RegisterRoutes mendaftarkan halaman admin group & anggota. Halaman akses DB per group
// (/admin/groups/{id}/access) didaftarkan oleh package admin/access (B).
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	mux.Handle("GET /admin/groups", mw.Protect(authz.ActAdminManageGroups, h.List))
	mux.Handle("GET /admin/groups/new", mw.Protect(authz.ActAdminManageGroups, h.New))
	mux.Handle("POST /admin/groups", mw.Protect(authz.ActAdminManageGroups, h.Create))
	mux.Handle("GET /admin/groups/{id}", mw.Protect(authz.ActAdminManageGroups, h.Show))
	mux.Handle("POST /admin/groups/{id}/edit", mw.Protect(authz.ActAdminManageGroups, h.Update))
	mux.Handle("POST /admin/groups/{id}/delete", mw.Protect(authz.ActAdminManageGroups, h.Delete))
	mux.Handle("POST /admin/groups/{id}/members", mw.Protect(authz.ActAdminManageGroups, h.AddMember))
	mux.Handle("POST /admin/groups/{id}/members/{userID}/delete", mw.Protect(authz.ActAdminManageGroups, h.RemoveMember))
}
