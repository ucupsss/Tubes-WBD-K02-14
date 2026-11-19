package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
)

func fieldErrors(err error) (map[string]string, bool) { return apperr.FieldErrors(err) }

// PendingApprovals adalah inbox leader: approval yang menunggu keputusan group yang ia pimpin.
func (h *Handler) PendingApprovals(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	list, err := h.d.Approval.Pending(r.Context(), p.UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type item struct {
		ExecutionID int64     `json:"execution_id"`
		Round       int       `json:"round"`
		GroupID     int64     `json:"group_id"`
		GroupName   string    `json:"group_name"`
		CreatedAt   time.Time `json:"created_at"`
	}
	items := make([]item, len(list))
	for i, p := range list {
		items[i] = item{ExecutionID: p.ExecutionID, Round: p.Round, GroupID: p.GroupID, GroupName: p.GroupName, CreatedAt: p.CreatedAt}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type decisionRequest struct {
	Comment string `json:"comment"`
}

func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) { h.decide(w, r, true) }
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request)  { h.decide(w, r, false) }

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, approve bool) {
	execID, err := strconv.ParseInt(r.PathValue("execID"), 10, 64)
	if err != nil || execID <= 0 {
		httpx.WriteError(w, r, apperr.NotFound("Execution tidak ditemukan"))
		return
	}
	var req decisionRequest
	if r.ContentLength != 0 { // body opsional
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	}
	p, _ := authz.PrincipalFrom(r.Context())
	out, err := h.d.Approval.Decide(r.Context(), p, execID, approve, req.Comment)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": out.Status})
}
