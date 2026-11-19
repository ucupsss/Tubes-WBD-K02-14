package auth

import "net/http"

type LoginMiddleware interface {
	RequireLogin(next http.Handler) http.Handler
}

// RegisterRoutes mendaftarkan logout bentuk form untuk halaman HTML lama (explorer/migration) yang belum
// dipindah ke SPA. Login dan logout SPA ada di internal/api (POST /api/auth/login, /api/auth/logout).
// Hapus package ini dari wiring setelah seluruh halaman HTML dipindah.
func RegisterRoutes(mux *http.ServeMux, h *Handler, mw LoginMiddleware) {
	mux.Handle("POST /logout", mw.RequireLogin(http.HandlerFunc(h.Logout)))
}
