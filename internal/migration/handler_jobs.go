package migration

import (
	"context"
	"net/http"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/telemetry"
)

// Enqueue menjawab POST /api/executions/{execID}/enqueue dengan 202 dan job_id.
func (h *Handler) Enqueue(w http.ResponseWriter, r *http.Request) {
	h.publish(w, r, false)
}

// Resume menjawab POST /api/executions/{execID}/resume tanpa membuka ronde approval baru.
func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	h.publish(w, r, true)
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request, resume bool) {
	execID, ok := h.executionID(w, r)
	if !ok {
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	if !h.allow(w, r, p, execID, authz.ActMigrationRun) {
		return
	}
	fn := h.svc.Enqueue
	if resume {
		fn = h.svc.Resume
	}
	jobID, err := fn(r.Context(), p, execID, traceCarrier(r.Context()))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"job_id": jobID, "execution_id": execID,
	})
}

// Cancel menjawab POST /api/executions/{execID}/cancel sebelum worker mengambil job.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	execID, ok := h.executionID(w, r)
	if !ok {
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	if !h.allow(w, r, p, execID, authz.ActMigrationCancel) {
		return
	}
	if err := h.svc.Cancel(r.Context(), p, execID); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"execution_id": execID, "status": StatusCancelled})
}

// ShowJob menjawab GET /api/jobs/{jobID} untuk polling status antrian.
func (h *Handler) ShowJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := httpx.PathInt64(r, "jobID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	job, exec, err := h.svc.Job(r.Context(), jobID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	if !h.allowDB(w, r, p, exec.DatabaseID, authz.ActMigrationView) {
		return
	}
	wait := time.Since(job.EnqueuedAt)
	if job.ClaimedAt != nil {
		wait = job.ClaimedAt.Sub(job.EnqueuedAt)
	}
	if wait < 0 {
		wait = 0
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"job_id": job.ID, "status": job.Status, "execution_id": job.ExecutionID,
		"execution_status": exec.Status, "batches_committed": exec.BatchesCommitted,
		"rows_committed": exec.RowsCommitted, "attempts": job.Attempts,
		"queue_wait_ms": wait.Milliseconds(),
	})
}

func (h *Handler) executionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httpx.PathInt64(r, "execID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return 0, false
	}
	return id, true
}

func (h *Handler) allow(w http.ResponseWriter, r *http.Request, p authz.Principal, execID int64, act authz.Action) bool {
	exec, _, err := h.svc.GetExecution(r.Context(), execID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return false
	}
	return h.allowDB(w, r, p, exec.DatabaseID, act)
}

func (h *Handler) allowDB(w http.ResponseWriter, r *http.Request, p authz.Principal, dbID int64, act authz.Action) bool {
	if h.authz == nil {
		return true
	}
	ok, err := h.authz.Can(r.Context(), p, act, dbID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return false
	}
	if !ok {
		httpx.WriteError(w, r, authz.ErrForbidden)
		return false
	}
	return true
}

func traceCarrier(ctx context.Context) map[string]string {
	return telemetry.Inject(ctx)
}
