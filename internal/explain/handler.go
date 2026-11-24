package explain

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// Migrations adalah bagian migration.Service yang dibutuhkan analisis.
type Migrations interface {
	ExplainSource(ctx context.Context, dbID, id int64) (title, sql string, err error)
}

type Handler struct {
	migs     Migrations
	analyzer *Analyzer
	view     *view.View
}

func NewHandler(migs Migrations, analyzer *Analyzer, v *view.View) *Handler {
	return &Handler{migs: migs, analyzer: analyzer, view: v}
}

// Explain menganalisis SQL revisi yang belum diajukan dan mengembalikan plan sebagai JSON.
// Route ini sudah dijaga Protect(ActMigrationExplain), yang mensyaratkan akses edit.
func (h *Handler) Explain(w http.ResponseWriter, r *http.Request) {
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	_, sql, err := h.migs.ExplainSource(r.Context(), dbID, id)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	analyze := r.PostFormValue("analyze") != ""
	start := time.Now()
	report, err := h.analyzer.Analyze(r.Context(), dbID, sql, analyze)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	slog.Info("migration explained",
		"user_id", p.UserID, "db_id", dbID, "migration_id", id,
		"analyze", analyze, "statements", len(report.Statements), "failed", report.Failed,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	httpx.WriteJSON(w, http.StatusOK, report)
}
