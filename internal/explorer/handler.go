package explorer

import (
	"net/http"
	"strconv"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
)

// Handler menangani endpoint REST JSON untuk Dashboard & Database Explorer (Milestone 2, B3).
type Handler struct {
	svc *Service
}

// NewHandler membuat Handler JSON Explorer baru.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ListDatabases menangani GET /api/databases — Mengembalikan daftar seluruh database yang boleh diakses pengguna beserta level efektifnya.
func (h *Handler) ListDatabases(w http.ResponseWriter, r *http.Request) {
	p, ok := authz.PrincipalFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, apperr.Unauthenticated("Silakan login terlebih dahulu"))
		return
	}

	dbs, err := h.svc.GetDashboard(r.Context(), p.UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	type item struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Level string `json:"level"`
	}
	items := make([]item, len(dbs))
	for i, d := range dbs {
		items[i] = item{
			ID:    d.DatabaseID,
			Name:  d.Name,
			Level: d.Level.String(),
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Schema menangani GET /api/databases/{dbID}/schema — Mengembalikan info database, level akses, dan katalog tabel/view.
func (h *Handler) Schema(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	ctx := logx.WithDatabase(r.Context(), strconv.FormatInt(dbID, 10))

	dbInfo, _ := h.svc.GetDatabaseInfo(ctx, dbID)
	level, _ := h.svc.EffectiveLevel(ctx, p.UserID, dbID)

	tables, err := h.svc.ListTables(ctx, dbID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if tables == nil {
		tables = []TableSummary{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"database": map[string]any{
			"id":      dbInfo.ID,
			"name":    dbInfo.Name,
			"host":    dbInfo.Host,
			"port":    dbInfo.Port,
			"db_name": dbInfo.DBName,
			"level":   level.String(),
		},
		"tables": tables,
	})
}

// TableDetails menangani GET /api/databases/{dbID}/tables/{schema}/{table} — Mengembalikan metadata kolom, constraints, dan indeks.
func (h *Handler) TableDetails(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	schema := r.PathValue("schema")
	table := r.PathValue("table")
	if schema == "" || table == "" {
		httpx.WriteError(w, r, ErrTableNotFound)
		return
	}
	ctx := logx.WithDatabase(r.Context(), strconv.FormatInt(dbID, 10))

	dbInfo, _ := h.svc.GetDatabaseInfo(ctx, dbID)
	level, _ := h.svc.EffectiveLevel(ctx, p.UserID, dbID)
	meta, err := h.svc.GetTableMetadata(ctx, dbID, schema, table)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if meta.Columns == nil {
		meta.Columns = []ColumnInfo{}
	}
	if meta.Constraints == nil {
		meta.Constraints = []ConstraintInfo{}
	}
	if meta.Indexes == nil {
		meta.Indexes = []IndexInfo{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"database": map[string]any{
			"id":    dbInfo.ID,
			"name":  dbInfo.Name,
			"level": level.String(),
		},
		"metadata": meta,
	})
}

// TableData menangani GET /api/databases/{dbID}/tables/{schema}/{table}/data — Mengembalikan data baris berpaginasi.
func (h *Handler) TableData(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	schema := r.PathValue("schema")
	table := r.PathValue("table")
	if schema == "" || table == "" {
		httpx.WriteError(w, r, ErrTableNotFound)
		return
	}
	ctx := logx.WithDatabase(r.Context(), strconv.FormatInt(dbID, 10))

	sizeStr := r.URL.Query().Get("page_size")
	if sizeStr == "" {
		sizeStr = r.URL.Query().Get("size")
	}
	pg := ParsePagination(r.URL.Query().Get("page"), sizeStr)

	dbInfo, _ := h.svc.GetDatabaseInfo(ctx, dbID)
	level, _ := h.svc.EffectiveLevel(ctx, p.UserID, dbID)
	dataPage, err := h.svc.GetTableData(ctx, dbID, schema, table, pg.Page, pg.Size)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if dataPage.Columns == nil {
		dataPage.Columns = []string{}
	}
	if dataPage.Rows == nil {
		dataPage.Rows = [][]string{}
	}
	if dataPage.PKColumns == nil {
		dataPage.PKColumns = []string{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"database": map[string]any{
			"id":    dbInfo.ID,
			"name":  dbInfo.Name,
			"level": level.String(),
		},
		"data_page": dataPage,
		"items":     dataPage.Rows,
		"page":      dataPage.Page,
		"page_size": dataPage.Size,
		"total":     dataPage.TotalRows,
	})
}
