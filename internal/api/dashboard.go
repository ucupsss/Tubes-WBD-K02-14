package api

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
)

// ListAccessibleDatabases mengembalikan database yang boleh diakses pemanggil beserta level efektifnya (D1).
//
// SEMENTARA milik A supaya dashboard SPA berfungsi. Endpoint ini bagian dari paket B3 (API explorer);
// saat B memindahkannya ke package explorer, hapus route ini dari RegisterRoutes agar tidak terdaftar ganda.
func (h *Handler) ListAccessibleDatabases(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	list, err := h.d.Authz.AccessibleDatabases(r.Context(), p.UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type item struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Level string `json:"level"`
	}
	items := make([]item, len(list))
	for i, d := range list {
		items[i] = item{ID: d.DatabaseID, Name: d.Name, Level: d.Level.String()}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
