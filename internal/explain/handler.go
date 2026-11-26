package explain

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
)

// Migrations adalah bagian migration.Service yang dibutuhkan untuk analisis EXPLAIN.
type Migrations interface {
	ExplainSource(ctx context.Context, dbID, id int64) (title, sql string, err error)
}

// Handler menangani endpoint REST JSON untuk analisis EXPLAIN / EXPLAIN ANALYZE (Milestone 2, B3).
type Handler struct {
	migs     Migrations
	analyzer *Analyzer
}

// NewHandler membuat Handler JSON Explain baru.
func NewHandler(migs Migrations, analyzer *Analyzer) *Handler {
	return &Handler{migs: migs, analyzer: analyzer}
}

type explainRequest struct {
	Analyze bool `json:"analyze"`
}

// Explain menangani POST /api/databases/{dbID}/migrations/{id}/explain.
// Menerima JSON {"analyze": bool} (atau body kosong = analyze false), menjalankan EXPLAIN di dalam
// transaksi yang selalu di-rollback, lalu mengembalikan Report JSON.
func (h *Handler) Explain(w http.ResponseWriter, r *http.Request) {
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	ctx := logx.WithDatabase(r.Context(), strconv.FormatInt(dbID, 10))

	var req explainRequest
	if r.Body != nil && r.ContentLength != 0 && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	} else if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	} else if r.PostFormValue("analyze") != "" || r.URL.Query().Get("analyze") == "true" || r.URL.Query().Get("analyze") == "1" {
		req.Analyze = true
	}

	_, sqlText, err := h.migs.ExplainSource(ctx, dbID, id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	start := time.Now()
	report, err := h.analyzer.Analyze(ctx, dbID, sqlText, req.Analyze)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if report.Statements == nil {
		report.Statements = []StatementResult{}
	}

	p, _ := authz.PrincipalFrom(ctx)
	slog.InfoContext(ctx, "migration.explained",
		"user_id", p.UserID,
		"db_id", dbID,
		"migration_id", id,
		"analyze", req.Analyze,
		"statements", len(report.Statements),
		"failed", report.Failed,
		"duration_ms", time.Since(start).Milliseconds(),
		"status", "ok",
	)

	httpx.WriteJSON(w, http.StatusOK, report)
}
