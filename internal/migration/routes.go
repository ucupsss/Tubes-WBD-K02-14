package migration

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

// AuthorizerMiddleware is the subset of authz.Middleware used to register routes.
type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
}

// RegisterRoutes mounts the migration pages. The EXPLAIN route belongs to package explain.
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	mux.Handle("GET /db/{dbID}/migrations", mw.Protect(authz.ActMigrationView, h.List))
	mux.Handle("GET /db/{dbID}/migrations/new", mw.Protect(authz.ActMigrationCreate, h.New))
	mux.Handle("POST /db/{dbID}/migrations", mw.Protect(authz.ActMigrationCreate, h.Create))
	mux.Handle("GET /db/{dbID}/migrations/{id}", mw.Protect(authz.ActMigrationView, h.Show))
	mux.Handle("POST /db/{dbID}/migrations/{id}/edit", mw.Protect(authz.ActMigrationEdit, h.Update))
	mux.Handle("POST /db/{dbID}/migrations/{id}/execute", mw.Protect(authz.ActMigrationExecute, h.Execute))
	mux.Handle("POST /db/{dbID}/migrations/{id}/delete", mw.Protect(authz.ActMigrationDelete, h.Delete))
}
