package migration

import (
	"context"
	"net/http"
	"strconv"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
)

// ApprovalLister memasok baris approval untuk detail execution.
type ApprovalLister interface {
	ListByExecution(ctx context.Context, execID int64) ([]approval.Approval, error)
}

// Handler menyajikan API JSON migration dan execution.
type Handler struct {
	svc       *Service
	authz     authz.Authorizer
	approvals ApprovalLister
}

// NewHandler merakit handler JSON. az nil berarti pemeriksaan akses di handler dilewati (untuk tes).
func NewHandler(svc *Service, az authz.Authorizer, approvals ApprovalLister) *Handler {
	return &Handler{svc: svc, authz: az, approvals: approvals}
}

type inputBody struct {
	Title              string `json:"title"`
	UpSQL              string `json:"up_sql"`
	DownSQL            string `json:"down_sql"`
	Irreversible       bool   `json:"irreversible"`
	IrreversibleReason string `json:"irreversible_reason"`
	ExecMode           string `json:"exec_mode"`
	BatchTable         string `json:"batch_table"`
	BatchColumn        string `json:"batch_column"`
	BatchSize          int    `json:"batch_size"`
}

func (b inputBody) input() Input {
	return Input{
		Title: b.Title, UpSQL: b.UpSQL, DownSQL: b.DownSQL, Irreversible: b.Irreversible,
		IrreversibleReason: b.IrreversibleReason, ExecMode: b.ExecMode,
		BatchTable: b.BatchTable, BatchColumn: b.BatchColumn, BatchSize: b.BatchSize,
	}
}

// List menjawab GET /api/databases/{dbID}/migrations.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	dbID, ok := pathDB(w, r)
	if !ok {
		return
	}
	page, size := queryPage(r)
	list, total, err := h.svc.List(r.Context(), dbID, page, size)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]any, len(list))
	for i := range list {
		items[i] = migrationJSON(list[i])
	}
	writePage(w, items, page, size, total)
}

// Create menjawab POST /api/databases/{dbID}/migrations dengan 201.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	dbID, ok := pathDB(w, r)
	if !ok {
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	var body inputBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := h.svc.Create(r.Context(), p, dbID, body.input())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, migrationJSON(m))
}

// Get menjawab GET /api/databases/{dbID}/migrations/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := pathIDs(w, r)
	if !ok {
		return
	}
	m, err := h.svc.Get(r.Context(), dbID, id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, migrationJSON(m))
}

// Update menjawab PUT /api/databases/{dbID}/migrations/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := pathIDs(w, r)
	if !ok {
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	var body inputBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := h.svc.Update(r.Context(), p, dbID, id, body.input())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, migrationJSON(m))
}

// Delete menjawab DELETE /api/databases/{dbID}/migrations/{id} dengan 204.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	dbID, id, ok := pathIDs(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), dbID, id); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// Validate memeriksa sintaks SQL pada body tanpa menyimpannya.
func (h *Handler) Validate(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := pathIDs(w, r); !ok {
		return
	}
	var body inputBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := body.input().Validate(); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"valid": true})
}

// Submit membuka execution apply dan ronde approval.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	h.open(w, r, h.svc.Submit)
}

// Forward membuka execution forward untuk revisi yang sudah dibekukan.
func (h *Handler) Forward(w http.ResponseWriter, r *http.Request) {
	h.open(w, r, h.svc.Forward)
}

// Rollback membuka execution rollback untuk migration teratas yang masih aktif.
func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	h.open(w, r, h.svc.Rollback)
}

func (h *Handler) open(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, p authz.Principal, dbID, id int64) (Execution, error)) {
	dbID, id, ok := pathIDs(w, r)
	if !ok {
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	e, err := fn(r.Context(), p, dbID, id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, executionJSON(e))
}

// ListExecutions menjawab GET /api/databases/{dbID}/executions. Query hide_rollback menyaring tampilan.
func (h *Handler) ListExecutions(w http.ResponseWriter, r *http.Request) {
	dbID, ok := pathDB(w, r)
	if !ok {
		return
	}
	page, size := queryPage(r)
	hide := r.URL.Query().Get("hide_rollback") == "true" || r.URL.Query().Get("hide_rollback") == "1"
	list, total, err := h.svc.ListExecutions(r.Context(), dbID, hide, page, size)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]any, len(list))
	for i := range list {
		items[i] = executionJSON(list[i])
	}
	writePage(w, items, page, size, total)
}

// ShowExecution menjawab GET /api/executions/{execID} bila pemanggil boleh melihat databasenya.
func (h *Handler) ShowExecution(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathInt64(r, "execID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	e, attempts, err := h.svc.GetExecution(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	if h.authz != nil {
		allowed, err := h.authz.Can(r.Context(), p, authz.ActMigrationView, e.DatabaseID)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if !allowed {
			httpx.WriteError(w, r, authz.ErrForbidden)
			return
		}
	}
	var approvals []approval.Approval
	if h.approvals != nil {
		approvals, err = h.approvals.ListByExecution(r.Context(), id)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	}
	batches, err := h.svc.ListBatches(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	canEdit := true
	if h.authz != nil {
		canEdit, _ = h.authz.Can(r.Context(), p, authz.ActMigrationRun, e.DatabaseID)
	}
	jobID, _ := h.svc.LatestJobID(r.Context(), id)
	execObj := executionJSON(e)
	body := executionJSON(e)
	body["execution"] = execObj
	body["approvals"] = approvalJSON(approvals)
	body["attempts"] = attemptJSON(attempts)
	body["batches"] = batches
	body["job_id"] = jobID
	body["can_edit"] = canEdit
	httpx.WriteJSON(w, http.StatusOK, body)
}

func pathDB(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return 0, false
	}
	return id, true
}

func pathIDs(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	dbID, ok := pathDB(w, r)
	if !ok {
		return 0, 0, false
	}
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return 0, 0, false
	}
	return dbID, id, true
}

func queryPage(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	return normalizePage(page, size)
}

func writePage(w http.ResponseWriter, items []any, page, size, total int) {
	if items == nil {
		items = []any{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "page": page, "page_size": size, "total": total,
	})
}

func migrationJSON(m Migration) map[string]any {
	return map[string]any{
		"id": m.ID, "database_id": m.DatabaseID, "title": m.Title, "version": m.Version,
		"created_by": m.CreatedBy, "created_by_username": m.CreatedByUsername,
		"created_at": m.CreatedAt, "revision": revisionJSON(m.Revision),
	}
}

func revisionJSON(r Revision) map[string]any {
	return map[string]any{
		"id": r.ID, "revision_no": r.RevisionNo, "up_sql": r.UpSQL, "down_sql": r.DownSQL,
		"irreversible": r.Irreversible, "irreversible_reason": r.IrreversibleReason,
		"exec_mode": r.ExecMode, "batch_table": r.BatchTable, "batch_column": r.BatchColumn,
		"batch_size": r.BatchSize, "sql_hash": r.SQLHash, "frozen_at": r.FrozenAt,
	}
}

func executionJSON(e Execution) map[string]any {
	return map[string]any{
		"id": e.ID, "database_id": e.DatabaseID, "migration_id": e.MigrationID,
		"migration_title": e.MigrationTitle, "revision_id": e.RevisionID,
		"revision_no": e.RevisionNo, "up_sql": e.UpSQL, "down_sql": e.DownSQL,
		"irreversible": e.Irreversible, "irreversible_reason": e.IrreversibleReason,
		"exec_mode": e.ExecMode, "is_top_of_chain": e.IsTopOfChain,
		"is_rolled_back": e.IsRolledBack, "has_active_revert": e.HasActiveRevert,
		"kind":   e.Kind,
		"status": e.Status, "requested_by": e.RequestedBy, "requested_by_username": e.RequestedByUsername,
		"rolls_back_execution_id": e.RollsBackExecutionID, "approval_round": e.ApprovalRound,
		"version_applied": e.VersionApplied, "batches_committed": e.BatchesCommitted,
		"rows_committed": e.RowsCommitted, "created_at": e.CreatedAt, "started_at": e.StartedAt,
		"finished_at": e.FinishedAt, "error_code": e.ErrorCode, "error_message": e.ErrorMessage,
	}
}

func approvalJSON(list []approval.Approval) []map[string]any {
	out := make([]map[string]any, len(list))
	for i, a := range list {
		out[i] = map[string]any{
			"execution_id": a.ExecutionID, "round": a.Round, "group_id": a.GroupID,
			"group_name": a.GroupName, "leader_user_id": a.LeaderID, "leader_id": a.LeaderID,
			"leader_name": a.LeaderName, "decision": a.Decision,
			"comment": a.Comment, "decided_by_name": a.DecidedByName, "decided_at": a.DecidedAt,
		}
	}
	return out
}

func attemptJSON(list []Attempt) []map[string]any {
	out := make([]map[string]any, len(list))
	for i, a := range list {
		out[i] = map[string]any{
			"id": a.ID, "attempt_no": a.AttemptNo, "worker_id": a.WorkerID, "status": a.Status,
			"sql_executed": a.SQLExecuted, "sqlstate": a.SQLState, "error_message": a.ErrorMessage,
			"started_at": a.StartedAt, "finished_at": a.FinishedAt, "trace_id": a.TraceID,
		}
	}
	return out
}
