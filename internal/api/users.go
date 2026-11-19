package api

import (
	"net/http"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
)

type userDTO struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	IsAdmin   bool      `json:"is_admin"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserDTO(u users.User) userDTO {
	return userDTO{ID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin, IsActive: u.IsActive, CreatedAt: u.CreatedAt}
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	list, err := h.d.Users.List(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]userDTO, len(list))
	for i, u := range list {
		items[i] = toUserDTO(u)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, r, users.ErrNotFound)
		return
	}
	u, err := h.d.Users.Get(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserDTO(u))
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := h.d.Users.Create(r.Context(), auth.CreateUserInput{Username: req.Username, Password: req.Password, IsAdmin: req.IsAdmin})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

type updateUserRequest struct {
	IsAdmin  bool `json:"is_admin"`
	IsActive bool `json:"is_active"`
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, r, users.ErrNotFound)
		return
	}
	var req updateUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	if err := h.d.Users.Update(r.Context(), p.UserID, id, users.UpdateInput{IsAdmin: req.IsAdmin, IsActive: req.IsActive}); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

type passwordRequest struct {
	Password string `json:"password"`
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, r, users.ErrNotFound)
		return
	}
	var req passwordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	if err := h.d.Users.ResetPassword(r.Context(), p.UserID, id, req.Password); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}
