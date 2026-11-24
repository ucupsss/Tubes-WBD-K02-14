package migration

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

// AuthorizerMiddleware adalah bagian authz.Middleware yang dipakai untuk mendaftarkan route.
type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
	RequireLogin(next http.Handler) http.Handler
}

// RegisterRoutes memasang API JSON migration dan execution.
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	mux.Handle("GET /api/databases/{dbID}/migrations", mw.Protect(authz.ActMigrationView, h.List))
	mux.Handle("POST /api/databases/{dbID}/migrations", mw.Protect(authz.ActMigrationCreate, h.Create))
	mux.Handle("GET /api/databases/{dbID}/migrations/{id}", mw.Protect(authz.ActMigrationView, h.Get))
	mux.Handle("PUT /api/databases/{dbID}/migrations/{id}", mw.Protect(authz.ActMigrationEdit, h.Update))
	mux.Handle("DELETE /api/databases/{dbID}/migrations/{id}", mw.Protect(authz.ActMigrationDelete, h.Delete))
	mux.Handle("POST /api/databases/{dbID}/migrations/{id}/validate", mw.Protect(authz.ActMigrationEdit, h.Validate))
	mux.Handle("POST /api/databases/{dbID}/migrations/{id}/submit", mw.Protect(authz.ActMigrationSubmit, h.Submit))
	mux.Handle("POST /api/databases/{dbID}/migrations/{id}/forward", mw.Protect(authz.ActMigrationSubmit, h.Forward))
	mux.Handle("POST /api/databases/{dbID}/migrations/{id}/rollback", mw.Protect(authz.ActMigrationRollback, h.Rollback))
	mux.Handle("GET /api/databases/{dbID}/executions", mw.Protect(authz.ActMigrationView, h.ListExecutions))
	mux.Handle("GET /api/executions/{execID}", mw.RequireLogin(http.HandlerFunc(h.ShowExecution)))
}
