package explorer

import (
	"fmt"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

type Handler struct {
	svc  *Service
	view *view.View
}

func NewHandler(svc *Service, v *view.View) *Handler {
	return &Handler{svc: svc, view: v}
}

// Dashboard menangani GET / — Menampilkan seluruh database yang dapat diakses pengguna.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	p, ok := authz.PrincipalFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	dbs, err := h.svc.GetDashboard(r.Context(), p.UserID)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	h.view.Render(w, r, http.StatusOK, "dashboard/index.html", view.PageData{
		Title:     "Workspace Database",
		Principal: &p,
		Data: map[string]any{
			"Databases": dbs,
		},
	})
}

// Overview menangani GET /db/{dbID} — Menampilkan daftar skema dan tabel pengguna.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	dbInfo, _ := h.svc.GetDatabaseInfo(r.Context(), dbID)
	tables, err := h.svc.ListTables(r.Context(), dbID)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	h.view.Render(w, r, http.StatusOK, "explorer/overview.html", view.PageData{
		Title:     fmt.Sprintf("%s · Katalog Tabel", dbInfo.Name),
		Principal: &p,
		Data: map[string]any{
			"Database": dbInfo,
			"Tables":   tables,
		},
	})
}

// TableDetails menangani GET /db/{dbID}/tables/{schema}/{table} — Menampilkan metadata kolom, constraints, dan indeks.
func (h *Handler) TableDetails(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	schema := r.PathValue("schema")
	table := r.PathValue("table")
	if schema == "" || table == "" {
		httpx.HandleError(w, r, h.view, ErrTableNotFound)
		return
	}

	dbInfo, _ := h.svc.GetDatabaseInfo(r.Context(), dbID)
	meta, err := h.svc.GetTableMetadata(r.Context(), dbID, schema, table)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	h.view.Render(w, r, http.StatusOK, "explorer/table.html", view.PageData{
		Title:     fmt.Sprintf("%s.%s · Metadata Skema", schema, table),
		Principal: &p,
		Data: map[string]any{
			"Database": dbInfo,
			"Metadata": meta,
		},
	})
}

// TableData menangani GET /db/{dbID}/tables/{schema}/{table}/data — Menampilkan isi data baris berpaginasi.
func (h *Handler) TableData(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	schema := r.PathValue("schema")
	table := r.PathValue("table")
	if schema == "" || table == "" {
		httpx.HandleError(w, r, h.view, ErrTableNotFound)
		return
	}

	pg := ParsePagination(r.URL.Query().Get("page"), r.URL.Query().Get("size"))
	dbInfo, _ := h.svc.GetDatabaseInfo(r.Context(), dbID)

	dataPage, err := h.svc.GetTableData(r.Context(), dbID, schema, table, pg.Page, pg.Size)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}

	h.view.Render(w, r, http.StatusOK, "explorer/data.html", view.PageData{
		Title:     fmt.Sprintf("%s.%s · Penampil Data", schema, table),
		Principal: &p,
		Data: map[string]any{
			"Database": dbInfo,
			"DataPage": dataPage,
		},
	})
}
