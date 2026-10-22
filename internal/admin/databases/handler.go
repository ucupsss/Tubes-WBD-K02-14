package databases

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
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
	conns, err := h.svc.List(r.Context())
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	h.view.Render(w, r, http.StatusOK, "admin/databases/index.html", view.PageData{
		Title: "Kelola Target Database",
		Data:  map[string]any{"Connections": conns},
	})
}

func (h *Handler) New(w http.ResponseWriter, r *http.Request) {
	h.view.Render(w, r, http.StatusOK, "admin/databases/new.html", view.PageData{
		Title: "Tambah Target Database",
		Data: map[string]any{
			"Input":  CreateInput{Port: 5432, SSLMode: "disable"},
			"Errors": map[string]string{},
		},
	})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())

	port, _ := strconv.Atoi(r.PostFormValue("port"))
	in := CreateInput{
		Name:      r.PostFormValue("name"),
		Host:      r.PostFormValue("host"),
		Port:      port,
		DBName:    r.PostFormValue("db_name"),
		Username:  r.PostFormValue("username"),
		Password:  r.PostFormValue("password"),
		SSLMode:   r.PostFormValue("sslmode"),
		CreatedBy: p.UserID,
	}

	_, err := h.svc.Create(r.Context(), in)
	if fe, ok := apperr.FieldErrors(err); ok {
		h.view.Render(w, r, http.StatusUnprocessableEntity, "admin/databases/new.html", view.PageData{
			Title: "Tambah Target Database",
			Data:  map[string]any{"Input": in, "Errors": fe},
		})
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	httpx.Redirect(w, r, "/admin/databases", view.FlashSuccess("Koneksi database berhasil ditambahkan"))
}

func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	conn, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	h.view.Render(w, r, http.StatusOK, "admin/databases/edit.html", view.PageData{
		Title: "Edit Target Database",
		Data: map[string]any{
			"Connection": conn,
			"Input": UpdateInput{
				Name:     conn.Name,
				Host:     conn.Host,
				Port:     conn.Port,
				DBName:   conn.DBName,
				Username: conn.Username,
				SSLMode:  conn.SSLMode,
			},
			"Errors": map[string]string{},
		},
	})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	port, _ := strconv.Atoi(r.PostFormValue("port"))
	in := UpdateInput{
		Name:     r.PostFormValue("name"),
		Host:     r.PostFormValue("host"),
		Port:     port,
		DBName:   r.PostFormValue("db_name"),
		Username: r.PostFormValue("username"),
		Password: r.PostFormValue("password"),
		SSLMode:  r.PostFormValue("sslmode"),
	}

	err = h.svc.Update(r.Context(), id, in)
	if fe, ok := apperr.FieldErrors(err); ok {
		h.view.Render(w, r, http.StatusUnprocessableEntity, "admin/databases/edit.html", view.PageData{
			Title: "Edit Target Database",
			Data: map[string]any{
				"Connection": Connection{ID: id},
				"Input":      in,
				"Errors":     fe,
			},
		})
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	httpx.Redirect(w, r, "/admin/databases", view.FlashSuccess("Koneksi database berhasil diperbarui"))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	httpx.Redirect(w, r, "/admin/databases", view.FlashSuccess("Koneksi database berhasil dihapus"))
}

func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	id, _ := httpx.PathInt64(r, "id") // bisa 0 jika test dari form new

	port, _ := strconv.Atoi(r.PostFormValue("port"))
	in := UpdateInput{
		Host:     r.PostFormValue("host"),
		Port:     port,
		DBName:   r.PostFormValue("db_name"),
		Username: r.PostFormValue("username"),
		Password: r.PostFormValue("password"),
		SSLMode:  r.PostFormValue("sslmode"),
	}

	redirectURL := "/admin/databases"
	if id > 0 {
		redirectURL = fmt.Sprintf("/admin/databases/%d/edit", id)
	}

	if err := h.svc.TestConnection(r.Context(), id, in); err != nil {
		httpx.Redirect(w, r, redirectURL, view.FlashError(fmt.Sprintf("Uji koneksi gagal: %v", err)))
		return
	}

	httpx.Redirect(w, r, redirectURL, view.FlashSuccess("Koneksi berhasil terhubung!"))
}
