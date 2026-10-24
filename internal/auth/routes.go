package auth

import "net/http"

type LoginMiddleware interface {
	RequireLogin(next http.Handler) http.Handler
}

// RegisterRoutes mendaftarkan login & logout. /login publik; /logout butuh session.
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw LoginMiddleware) {
	mux.HandleFunc("GET /login", h.ShowLogin)
	mux.HandleFunc("POST /login", h.Login)
	mux.Handle("POST /logout", mw.RequireLogin(http.HandlerFunc(h.Logout)))
}
