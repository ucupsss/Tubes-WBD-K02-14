package access

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

type Handler struct {
	svc  *Service
	view *view.View
}

func NewHandler(svc *Service, v *view.View) *Handler {
	return &Handler{svc: svc, view: v}
}

// Show menampilkan matriks hak akses grup terhadap seluruh database target.
func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	groupID, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	grants, err := h.svc.GetAccessMatrix(r.Context(), groupID)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	h.view.Render(w, r, http.StatusOK, "admin/access/index.html", view.PageData{
		Title: "Kelola Akses Database Grup",
		Data: map[string]any{
			"GroupID": groupID,
			"Grants":  grants,
		},
	})
}

// Update memproses penyimpanan perubahan hak akses database grup.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	groupID, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	p, _ := authz.PrincipalFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	grants := make(map[int64]string)
	for key, values := range r.PostForm {
		if strings.HasPrefix(key, "grant_") && len(values) > 0 {
			dbIDStr := strings.TrimPrefix(key, "grant_")
			dbID, err := strconv.ParseInt(dbIDStr, 10, 64)
			if err == nil {
				grants[dbID] = values[0]
			}
		}
	}

	in := SaveAccessInput{
		GroupID:   groupID,
		Grants:    grants,
		GrantedBy: p.UserID,
	}

	if err := h.svc.SaveAccess(r.Context(), in); err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	redirectURL := fmt.Sprintf("/admin/groups/%d/access", groupID)
	httpx.Redirect(w, r, redirectURL, view.FlashSuccess("Hak akses database berhasil diperbarui"))
}
