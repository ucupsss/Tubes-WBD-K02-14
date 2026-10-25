package explain

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type AuthorizerMiddleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
}

// RegisterRoutes mendaftarkan analisis EXPLAIN. Butuh akses edit karena EXPLAIN ANALYZE mengeksekusi statement.
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw AuthorizerMiddleware) {
	mux.Handle("POST /db/{dbID}/migrations/{id}/explain", mw.Protect(authz.ActMigrationExplain, h.Explain))
}
