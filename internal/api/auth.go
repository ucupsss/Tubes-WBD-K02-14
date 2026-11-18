package api

import (
	"errors"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/csrf"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
)

type userRef struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

type groupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// CSRF mengembalikan token CSRF untuk request ini: token session bila sudah login, token pre-login
// (double-submit cookie) bila belum. SPA memanggilnya sebelum login.
func (h *Handler) CSRF(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"csrf_token": csrf.Token(r)})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login memverifikasi kredensial lalu membuat session baru. Kegagalan selalu 401 generik.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	u, err := h.d.Auth.Authenticate(r.Context(), req.Username, req.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		h.d.Logger.WarnContext(r.Context(), "auth.login_failed", "username", req.Username) // password tidak pernah di-log
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorBody{Error: httpx.ErrorDetail{
			Code: "invalid_credentials", Message: auth.ErrInvalidCredentials.Msg, Details: map[string]any{},
		}})
		return
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	// Session lama di browser yang sama dimatikan; login selalu membuat session baru.
	if old, ok := session.FromContext(r.Context()); ok {
		if err := h.d.Sessions.Revoke(r.Context(), old.ID); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	}
	token, sess, err := h.d.Sessions.Create(r.Context(), u.SessionUser())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.d.Sessions.SetCookie(w, token)
	h.d.Logger.InfoContext(r.Context(), "auth.login", "user_id", u.ID, "status", "ok")

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":       userRef{ID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin},
		"csrf_token": sess.CSRFToken,
	})
}

// Logout mencabut session saat ini dan menghapus cookie.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if s, ok := session.FromContext(r.Context()); ok {
		if err := h.d.Sessions.Revoke(r.Context(), s.ID); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		h.d.Logger.InfoContext(r.Context(), "auth.logout", "user_id", s.UserID, "status", "ok")
	}
	h.d.Sessions.ClearCookie(w)
	httpx.NoContent(w)
}

// Me mengembalikan identitas pemanggil, token CSRF session, dan group yang ia pimpin (untuk inbox approval).
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	led, err := h.d.Groups.LedGroups(r.Context(), p.UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	refs := make([]groupRef, len(led))
	for i, g := range led {
		refs[i] = groupRef{ID: g.ID, Name: g.Name}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":       userRef{ID: p.UserID, Username: p.Username, IsAdmin: p.IsAdmin},
		"csrf_token": csrf.Token(r),
		"led_groups": refs,
	})
}
