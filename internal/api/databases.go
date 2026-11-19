package api

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
)

// connectionRequest dipakai create, update, dan test. Password kosong pada update = tidak diubah.
type connectionRequest struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	DBName   string `json:"db_name"`
	Username string `json:"username"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
}

func (h *Handler) connID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, r, databases.ErrNotFound)
		return 0, false
	}
	return id, true
}

// ListConnections: password terenkripsi tidak pernah ikut serta (Connection.PasswordEnc bertag json:"-").
func (h *Handler) ListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := h.d.Databases.List(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if conns == nil {
		conns = []databases.Connection{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": conns})
}

func (h *Handler) GetConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := h.connID(w, r)
	if !ok {
		return
	}
	c, err := h.d.Databases.GetByID(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) CreateConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	id, err := h.d.Databases.Create(r.Context(), databases.CreateInput{
		Name: req.Name, Host: req.Host, Port: req.Port, DBName: req.DBName,
		Username: req.Username, Password: req.Password, SSLMode: req.SSLMode, CreatedBy: p.UserID,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (h *Handler) UpdateConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := h.connID(w, r)
	if !ok {
		return
	}
	var req connectionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	err := h.d.Databases.Update(r.Context(), id, databases.UpdateInput{
		Name: req.Name, Host: req.Host, Port: req.Port, DBName: req.DBName,
		Username: req.Username, Password: req.Password, SSLMode: req.SSLMode,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) DeleteConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := h.connID(w, r)
	if !ok {
		return
	}
	if err := h.d.Databases.Delete(r.Context(), id); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// TestConnection menguji koneksi. Hasil uji selalu 200: {"ok": bool, "message": "..."}; error input tetap 400.
// id kosong (POST /api/admin/databases/test) dipakai form tambah yang belum tersimpan.
func (h *Handler) TestConnection(w http.ResponseWriter, r *http.Request) {
	id, _ := httpx.PathInt64(r, "id") // 0 bila rute tanpa {id}
	var req connectionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	err := h.d.Databases.TestConnection(r.Context(), id, databases.UpdateInput{
		Host: req.Host, Port: req.Port, DBName: req.DBName, Username: req.Username,
		Password: req.Password, SSLMode: req.SSLMode,
	})
	if err != nil {
		if _, fields := fieldErrors(err); fields {
			httpx.WriteError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Uji koneksi gagal: " + logx.RedactString(err.Error())})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Koneksi berhasil terhubung"})
}
