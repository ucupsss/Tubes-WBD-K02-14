package explain

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// Migrations adalah bagian migration.Service yang dibutuhkan halaman analisis.
type Migrations interface {
	Get(ctx context.Context, dbID, id int64) (migration.Migration, []migration.Run, error)
}

type Handler struct {
	migs     Migrations
	analyzer *Analyzer
	view     *view.View
}

func NewHandler(migs Migrations, analyzer *Analyzer, v *view.View) *Handler {
	return &Handler{migs: migs, analyzer: analyzer, view: v}
}

// Explain menganalisis SQL migration draft/gagal lalu menampilkan plan di halaman detail migration.
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
	m, runs, err := h.migs.Get(r.Context(), dbID, id)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	if m.Status != migration.StatusDraft && m.Status != migration.StatusFailed {
		httpx.HandleError(w, r, h.view, ErrNotAnalyzable)
		return
	}

	analyze := r.PostFormValue("analyze") != ""
	start := time.Now()
	report, err := h.analyzer.Analyze(r.Context(), dbID, m.SQLText, analyze)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	p, _ := authz.PrincipalFrom(r.Context())
	slog.Info("migration explained", // SQL tidak di-log
		"user_id", p.UserID, "db_id", dbID, "migration_id", id,
		"analyze", analyze, "statements", len(report.Statements), "failed", report.Failed,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	h.view.Render(w, r, http.StatusOK, "migration/detail.html", view.PageData{
		Title: "Analisis: " + m.Title,
		Data: migration.DetailPage{
			Migration: m,
			Runs:      runs,
			CanEdit:   true,
			ShowPlan:  true,
			Plans:     report,
		},
	})
}
