package groups

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
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

func groupURL(id int64) string { return fmt.Sprintf("/admin/groups/%d", id) }

func inputFrom(r *http.Request) Input {
	return Input{Name: r.PostFormValue("name"), Description: r.PostFormValue("description")}
}

// formErrors memetakan error validasi & nama duplikat menjadi error per field form.
func formErrors(err error) (map[string]string, int, bool) {
	if fe, ok := apperr.FieldErrors(err); ok {
		return fe, http.StatusUnprocessableEntity, true
	}
	if errors.Is(err, ErrNameTaken) {
		return map[string]string{"name": ErrNameTaken.Msg}, http.StatusConflict, true
	}
	return nil, 0, false
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context())
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	h.view.Render(w, r, http.StatusOK, "admin/groups/index.html", view.PageData{
		Title: "Kelola Group",
		Data:  map[string]any{"Groups": list},
	})
}

func (h *Handler) New(w http.ResponseWriter, r *http.Request) {
	h.renderNew(w, r, http.StatusOK, Input{}, map[string]string{})
}

func (h *Handler) renderNew(w http.ResponseWriter, r *http.Request, status int, in Input, errs map[string]string) {
	h.view.Render(w, r, status, "admin/groups/new.html", view.PageData{
		Title: "Tambah Group",
		Data:  map[string]any{"Input": in, "Errors": errs},
	})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in := inputFrom(r)
	id, err := h.svc.Create(r.Context(), in)
	if fe, status, ok := formErrors(err); ok {
		h.renderNew(w, r, status, in, fe)
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, groupURL(id), view.FlashSuccess("Group berhasil dibuat"))
}

// Show menampilkan detail group: form edit, daftar anggota, dan form tambah anggota.
func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrNotFound)
		return
	}
	h.renderShow(w, r, http.StatusOK, id, nil, map[string]string{})
}

// renderShow: in != nil → isi form edit dengan input user (saat validasi gagal), bukan data DB.
func (h *Handler) renderShow(w http.ResponseWriter, r *http.Request, status int, id int64, in *Input, errs map[string]string) {
	d, err := h.svc.Detail(r.Context(), id)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	form := Input{Name: d.Group.Name, Description: d.Group.Description}
	if in != nil {
		form = *in
	}
	h.view.Render(w, r, status, "admin/groups/show.html", view.PageData{
		Title: "Group " + d.Group.Name,
		Data:  map[string]any{"Detail": d, "Input": form, "Errors": errs},
	})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrNotFound)
		return
	}
	in := inputFrom(r)
	err = h.svc.Update(r.Context(), id, in)
	if fe, status, ok := formErrors(err); ok {
		h.renderShow(w, r, status, id, &in, fe)
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, groupURL(id), view.FlashSuccess("Group berhasil diperbarui"))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrNotFound)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, "/admin/groups", view.FlashSuccess("Group berhasil dihapus"))
}

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrNotFound)
		return
	}
	userID, err := strconv.ParseInt(r.PostFormValue("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.Redirect(w, r, groupURL(id), view.FlashError("Pilih user yang akan ditambahkan"))
		return
	}
	if err := h.svc.AddMember(r.Context(), id, userID); err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, groupURL(id), view.FlashSuccess("Anggota berhasil ditambahkan"))
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrNotFound)
		return
	}
	userID, err := httpx.PathInt64(r, "userID")
	if err != nil {
		httpx.HandleError(w, r, h.view, ErrUserNotFound)
		return
	}
	if err := h.svc.RemoveMember(r.Context(), id, userID); err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, groupURL(id), view.FlashSuccess("Anggota berhasil dikeluarkan"))
}
