package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
)

type groupDTO struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	MemberCount int       `json:"member_count"`
	LeaderID    *int64    `json:"leader_id"`
	LeaderName  string    `json:"leader_name"`
}

func toGroupDTO(g groups.Group) groupDTO {
	return groupDTO{ID: g.ID, Name: g.Name, Description: g.Description, CreatedAt: g.CreatedAt,
		MemberCount: g.MemberCount, LeaderID: g.LeaderID, LeaderName: g.LeaderName}
}

func (h *Handler) groupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, r, groups.ErrNotFound)
		return 0, false
	}
	return id, true
}

func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	list, err := h.d.Groups.List(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]groupDTO, len(list))
	for i, g := range list {
		items[i] = toGroupDTO(g)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type groupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := h.d.Groups.Create(r.Context(), groups.Input{Name: req.Name, Description: req.Description})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := h.groupID(w, r)
	if !ok {
		return
	}
	d, err := h.d.Groups.Detail(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type member struct {
		UserID   int64  `json:"user_id"`
		Username string `json:"username"`
		IsActive bool   `json:"is_active"`
		IsLeader bool   `json:"is_leader"`
	}
	type candidate struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	members := make([]member, len(d.Members))
	for i, m := range d.Members {
		members[i] = member{UserID: m.UserID, Username: m.Username, IsActive: m.IsActive, IsLeader: m.IsLeader}
	}
	cands := make([]candidate, len(d.Candidates))
	for i, c := range d.Candidates {
		cands[i] = candidate{ID: c.ID, Username: c.Username}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"group": toGroupDTO(d.Group), "members": members, "candidates": cands,
	})
}

func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := h.groupID(w, r)
	if !ok {
		return
	}
	var req groupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.d.Groups.Update(r.Context(), id, groups.Input{Name: req.Name, Description: req.Description}); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := h.groupID(w, r)
	if !ok {
		return
	}
	if err := h.d.Groups.Delete(r.Context(), id); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

type memberRequest struct {
	UserID int64 `json:"user_id"`
}

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	id, ok := h.groupID(w, r)
	if !ok {
		return
	}
	var req memberRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.UserID <= 0 {
		httpx.WriteError(w, r, apperr.InvalidFields("Input tidak valid", map[string]string{"user_id": "User wajib dipilih"}))
		return
	}
	if err := h.d.Groups.AddMember(r.Context(), id, req.UserID); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	id, ok := h.groupID(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.WriteError(w, r, groups.ErrUserNotFound)
		return
	}
	if err := h.d.Groups.RemoveMember(r.Context(), id, userID); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

type leaderRequest struct {
	UserID *int64 `json:"user_id"` // null = kosongkan leader
}

func (h *Handler) SetLeader(w http.ResponseWriter, r *http.Request) {
	id, ok := h.groupID(w, r)
	if !ok {
		return
	}
	var req leaderRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.d.Groups.SetLeader(r.Context(), id, req.UserID); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) GetAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := h.groupID(w, r)
	if !ok {
		return
	}
	if _, err := h.d.Groups.Get(r.Context(), id); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	grants, err := h.d.Access.GetAccessMatrix(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if grants == nil {
		grants = []access.DatabaseGrant{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": grants})
}

type accessRequest struct {
	Grants map[string]string `json:"grants"` // id database (string) → none|view|edit
}

func (h *Handler) SaveAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := h.groupID(w, r)
	if !ok {
		return
	}
	var req accessRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	grants := make(map[int64]string, len(req.Grants))
	for k, level := range req.Grants {
		dbID, err := strconv.ParseInt(k, 10, 64)
		if err != nil || dbID <= 0 {
			httpx.WriteError(w, r, apperr.Invalid("ID database tidak valid"))
			return
		}
		grants[dbID] = level
	}
	p, _ := authz.PrincipalFrom(r.Context())
	if err := h.d.Access.SaveAccess(r.Context(), access.SaveAccessInput{GroupID: id, Grants: grants, GrantedBy: p.UserID}); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.NoContent(w)
}
