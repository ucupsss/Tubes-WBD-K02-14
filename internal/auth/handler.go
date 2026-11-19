package auth

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// Sessions adalah bagian session.Manager yang dipakai handler login/logout (HTML dan API).
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
