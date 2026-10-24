package users

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
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

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context())
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	h.view.Render(w, r, http.StatusOK, "admin/users/index.html", view.PageData{
		Title: "Kelola User",
		Data:  map[string]any{"Users": list, "CurrentUserID": p.UserID},
	})
}

func (h *Handler) New(w http.ResponseWriter, r *http.Request) {
	h.renderNew(w, r, http.StatusOK, auth.CreateUserInput{}, map[string]string{})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in := auth.CreateUserInput{
		Username: r.PostFormValue("username"),
		Password: r.PostFormValue("password"),
		IsAdmin:  r.PostFormValue("is_admin") != "",
	}

	_, err := h.svc.Create(r.Context(), in)
	in.Password = "" // password tidak pernah dikirim balik ke form
	if fe, ok := apperr.FieldErrors(err); ok {
		h.renderNew(w, r, http.StatusUnprocessableEntity, in, fe)
		return
	}
	if errors.Is(err, auth.ErrUsernameTaken) {
		h.renderNew(w, r, http.StatusConflict, in, map[string]string{"username": auth.ErrUsernameTaken.Msg})
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, "/admin/users", view.FlashSuccess("User berhasil dibuat"))
}

func (h *Handler) renderNew(w http.ResponseWriter, r *http.Request, status int, in auth.CreateUserInput, errs map[string]string) {
	h.view.Render(w, r, status, "admin/users/new.html", view.PageData{
		Title: "Tambah User",
		Data:  map[string]any{"Input": in, "Errors": errs},
	})
}

func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrNotFound)
		return
	}
	h.renderEdit(w, r, http.StatusOK, id, map[string]string{})
}

func (h *Handler) renderEdit(w http.ResponseWriter, r *http.Request, status int, id int64, errs map[string]string) {
	u, err := h.svc.Get(r.Context(), id)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	h.view.Render(w, r, status, "admin/users/edit.html", view.PageData{
		Title: "Edit User",
		Data:  map[string]any{"User": u, "IsSelf": p.UserID == u.ID, "Errors": errs},
	})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrNotFound)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	in := UpdateInput{
		IsAdmin:  r.PostFormValue("is_admin") != "",
		IsActive: r.PostFormValue("is_active") != "",
	}

	editURL := fmt.Sprintf("/admin/users/%d/edit", id)
	err = h.svc.Update(r.Context(), p.UserID, id, in)
	if errors.Is(err, ErrSelfLockout) {
		httpx.Redirect(w, r, editURL, view.FlashError(ErrSelfLockout.Msg))
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, editURL, view.FlashSuccess("Status user berhasil diperbarui"))
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrNotFound)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())

	err = h.svc.ResetPassword(r.Context(), p.UserID, id, r.PostFormValue("password"))
	if fe, ok := apperr.FieldErrors(err); ok {
		h.renderEdit(w, r, http.StatusUnprocessableEntity, id, fe)
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, fmt.Sprintf("/admin/users/%d/edit", id), view.FlashSuccess("Password berhasil direset"))
}
