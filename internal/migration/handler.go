package migration

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// ListPage is the history screen.
type ListPage struct {
	DatabaseID int64
	Migrations []Migration
	CanEdit    bool
}

// FormPage is the new/edit screen.
type FormPage struct {
	DatabaseID  int64
	MigrationID int64
	Status      Status
	Input       CreateInput
	Errors      map[string]string
	CanEdit     bool
	ShowPlan    bool
}

// DetailPage is one migration and its attempts.
// ShowPlan stays false here. The EXPLAIN route (package explain) sets it when
// web/templates/explain/plan.html defines "explain_plan".
type DetailPage struct {
	Migration Migration
	Runs      []Run
	CanEdit   bool
	ShowPlan  bool
	Plans     any
}

// Handler serves migration pages.
type Handler struct {
	svc   *Service
	view  *view.View
	authz authz.Authorizer
}

// NewHandler builds a handler. az may be nil in tests that do not render edit controls.
func NewHandler(svc *Service, v *view.View, az authz.Authorizer) *Handler {
	return &Handler{svc: svc, view: v, authz: az}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	canEdit, err := h.canEdit(r.Context(), p.UserID, dbID)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	list, err := h.svc.List(r.Context(), dbID)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	h.view.Render(w, r, http.StatusOK, "migration/list.html", view.PageData{
		Title: "Riwayat Migration",
		Data: ListPage{
			DatabaseID: dbID,
			Migrations: list,
			CanEdit:    canEdit,
		},
	})
}

func (h *Handler) New(w http.ResponseWriter, r *http.Request) {
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	h.view.Render(w, r, http.StatusOK, "migration/new.html", view.PageData{
		Title: "Migration Baru",
		Data: FormPage{
			DatabaseID: dbID,
			Input:      CreateInput{DatabaseID: dbID},
			Errors:     map[string]string{},
			CanEdit:    true,
		},
	})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, err := httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpx.HandleError(w, r, h.view, apperr.Invalid("Ukuran atau format permintaan tidak valid"))
		return
	}
	in := CreateInput{
		DatabaseID: dbID,
		Title:      r.PostFormValue("title"),
		SQLText:    r.PostFormValue("sql_text"),
	}
	id, err := h.svc.CreateDraft(r.Context(), p, in)
	if fe, ok := apperr.FieldErrors(err); ok {
		h.view.Render(w, r, http.StatusUnprocessableEntity, "migration/new.html", view.PageData{
			Title: "Migration Baru",
			Data: FormPage{
				DatabaseID: dbID,
				Input:      in,
				Errors:     fe,
				CanEdit:    true,
			},
		})
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, detailPath(dbID, id), view.FlashSuccess("Draft migration dibuat"))
}

func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, id, ok := h.ids(w, r)
	if !ok {
		return
	}
	canEdit, err := h.canEdit(r.Context(), p.UserID, dbID)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	m, runs, err := h.svc.Get(r.Context(), dbID, id)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	h.view.Render(w, r, http.StatusOK, "migration/detail.html", view.PageData{
		Title: m.Title,
		Data: DetailPage{
			Migration: m,
			Runs:      runs,
			CanEdit:   canEdit,
			ShowPlan:  false,
		},
	})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, id, ok := h.ids(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpx.HandleError(w, r, h.view, apperr.Invalid("Ukuran atau format permintaan tidak valid"))
		return
	}
	in := CreateInput{
		DatabaseID: dbID,
		Title:      r.PostFormValue("title"),
		SQLText:    r.PostFormValue("sql_text"),
	}
	err := h.svc.UpdateDraft(r.Context(), p, dbID, id, in)
	if fe, ok := apperr.FieldErrors(err); ok {
		h.view.Render(w, r, http.StatusUnprocessableEntity, "migration/edit.html", view.PageData{
			Title: "Ubah Migration",
			Data: FormPage{
				DatabaseID:  dbID,
				MigrationID: id,
				Status:      StatusDraft,
				Input:       in,
				Errors:      fe,
				CanEdit:     true,
			},
		})
		return
	}
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, detailPath(dbID, id), view.FlashSuccess("Migration diperbarui"))
}

func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, id, ok := h.ids(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Execute(r.Context(), p, dbID, id)
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	if res.Failed {
		msg := res.Message
		if msg == "" {
			msg = "Migration gagal dijalankan"
		}
		httpx.Redirect(w, r, detailPath(dbID, id), view.FlashError(msg))
		return
	}
	httpx.Redirect(w, r, detailPath(dbID, id), view.FlashSuccess("Migration berhasil dijalankan"))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p, _ := authz.PrincipalFrom(r.Context())
	dbID, id, ok := h.ids(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteDraft(r.Context(), p, dbID, id); err != nil {
		httpx.HandleError(w, r, h.view, err)
		return
	}
	httpx.Redirect(w, r, listPath(dbID), view.FlashSuccess("Draft migration dihapus"))
}

func (h *Handler) ids(w http.ResponseWriter, r *http.Request) (dbID, id int64, ok bool) {
	var err error
	dbID, err = httpx.PathInt64(r, "dbID")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return 0, 0, false
	}
	id, err = httpx.PathInt64(r, "id")
	if err != nil {
		httpx.HandleError(w, r, h.view, err)
		return 0, 0, false
	}
	return dbID, id, true
}

func (h *Handler) canEdit(ctx context.Context, userID, dbID int64) (bool, error) {
	if h.authz == nil {
		return false, nil
	}
	level, err := h.authz.EffectiveLevel(ctx, userID, dbID)
	if err != nil {
		return false, err
	}
	return level >= authz.LevelEdit, nil
}

func listPath(dbID int64) string {
	return fmt.Sprintf("/db/%d/migrations", dbID)
}

func detailPath(dbID, id int64) string {
	return fmt.Sprintf("/db/%d/migrations/%d", dbID, id)
}
