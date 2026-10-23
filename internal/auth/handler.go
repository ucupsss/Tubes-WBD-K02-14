package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// Sessions adalah bagian session.Manager yang dipakai handler login/logout.
type Sessions interface {
	Create(ctx context.Context, u session.User) (string, session.Session, error)
	Revoke(ctx context.Context, sessionID int64) error
	SetCookie(w http.ResponseWriter, token string)
	ClearCookie(w http.ResponseWriter)
}

type Handler struct {
	svc      *Service
	sessions Sessions
	view     *view.View
	logger   *slog.Logger
}

func NewHandler(svc *Service, sessions Sessions, v *view.View, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, sessions: sessions, view: v, logger: logger}
}

func (h *Handler) ShowLogin(w http.ResponseWriter, r *http.Request) {
	if _, ok := authz.PrincipalFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.view.Render(w, r, http.StatusOK, "auth/login.html", view.PageData{
		Title: "Masuk",
		Data:  map[string]any{"Username": ""},
	})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")

	u, err := h.svc.Authenticate(r.Context(), username, password)
	if errors.Is(err, ErrInvalidCredentials) {
		h.logger.Warn("login failed", "username", username) // password TIDAK di-log
		h.view.Render(w, r, http.StatusUnauthorized, "auth/login.html", view.PageData{
			Title: "Masuk",
			Data:  map[string]any{"Username": username, "Error": ErrInvalidCredentials.Msg},
		})
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	// Session lama di browser yang sama (bila ada) dimatikan; login selalu membuat session baru.
	if old, ok := session.FromContext(r.Context()); ok {
		if err := h.sessions.Revoke(r.Context(), old.ID); err != nil {
			httpx.HandleError(w, r, h.view, err)
			return
		}
	}

	token, _, err := h.sessions.Create(r.Context(), u.SessionUser())
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	h.sessions.SetCookie(w, token)
	h.logger.Info("login success", "user_id", u.ID)
	httpx.Redirect(w, r, "/", nil)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if s, ok := session.FromContext(r.Context()); ok {
		if err := h.sessions.Revoke(r.Context(), s.ID); err != nil {
			httpx.HandleError(w, r, h.view, err)
			return
		}
		h.logger.Info("logout", "user_id", s.UserID)
	}
	h.sessions.ClearCookie(w)
	httpx.Redirect(w, r, "/login", view.FlashSuccess("Anda telah keluar"))
}
