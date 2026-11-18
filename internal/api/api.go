// Package api berisi endpoint REST JSON di bawah /api untuk SPA (Milestone 2).
//
// Handler di sini hanya menerjemahkan HTTP/JSON ↔ service; aturan bisnis tetap di service tiap modul.
// Format error seragam ada di httpx.WriteError. Semua endpoint selain /api/csrf dan /api/auth/login
// butuh login; endpoint admin butuh Platform Admin (lewat authz.Protect).
//
// Pemilik: A (auth, me, csrf, admin/*, approval). Endpoint explorer dan migration milik B dan C
// didaftarkan di package masing-masing.
package api

import (
	"log/slog"
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

// Middleware adalah bagian authz.Middleware yang dipakai untuk mendaftarkan route.
type Middleware interface {
	Protect(action authz.Action, h http.HandlerFunc) http.Handler
	RequireLogin(next http.Handler) http.Handler
}

// Deps adalah semua service yang dibutuhkan handler.
type Deps struct {
	Auth      *auth.Service
	Sessions  auth.Sessions
	Users     *users.Service
	Groups    *groups.Service
	Access    *access.Service
	Databases *databases.Service
	Approval  *approval.Service
	Authz     authz.Authorizer
	Logger    *slog.Logger
}

// Handler memegang dependensi.
type Handler struct{ d Deps }

// New membuat Handler.
func New(d Deps) *Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Handler{d: d}
}

// RegisterRoutes mendaftarkan seluruh endpoint milik A.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, mw Middleware) {
	// Auth dan sesi.
	mux.HandleFunc("GET /api/csrf", h.CSRF)
	mux.HandleFunc("POST /api/auth/login", h.Login)
	mux.Handle("POST /api/auth/logout", mw.RequireLogin(http.HandlerFunc(h.Logout)))
	mux.Handle("GET /api/me", mw.RequireLogin(http.HandlerFunc(h.Me)))
	mux.Handle("GET /api/databases", mw.RequireLogin(http.HandlerFunc(h.ListAccessibleDatabases))) // sementara, lihat dashboard.go

	// Admin: user.
	mux.Handle("GET /api/admin/users", mw.Protect(authz.ActAdminManageUsers, h.ListUsers))
	mux.Handle("POST /api/admin/users", mw.Protect(authz.ActAdminManageUsers, h.CreateUser))
	mux.Handle("GET /api/admin/users/{id}", mw.Protect(authz.ActAdminManageUsers, h.GetUser))
	mux.Handle("PUT /api/admin/users/{id}", mw.Protect(authz.ActAdminManageUsers, h.UpdateUser))
	mux.Handle("POST /api/admin/users/{id}/password", mw.Protect(authz.ActAdminManageUsers, h.ResetPassword))

	// Admin: group, anggota, leader, akses database.
	mux.Handle("GET /api/admin/groups", mw.Protect(authz.ActAdminManageGroups, h.ListGroups))
	mux.Handle("POST /api/admin/groups", mw.Protect(authz.ActAdminManageGroups, h.CreateGroup))
	mux.Handle("GET /api/admin/groups/{id}", mw.Protect(authz.ActAdminManageGroups, h.GetGroup))
	mux.Handle("PUT /api/admin/groups/{id}", mw.Protect(authz.ActAdminManageGroups, h.UpdateGroup))
	mux.Handle("DELETE /api/admin/groups/{id}", mw.Protect(authz.ActAdminManageGroups, h.DeleteGroup))
	mux.Handle("POST /api/admin/groups/{id}/members", mw.Protect(authz.ActAdminManageGroups, h.AddMember))
	mux.Handle("DELETE /api/admin/groups/{id}/members/{userID}", mw.Protect(authz.ActAdminManageGroups, h.RemoveMember))
	mux.Handle("PUT /api/admin/groups/{id}/leader", mw.Protect(authz.ActAdminManageGroups, h.SetLeader))
	mux.Handle("GET /api/admin/groups/{id}/access", mw.Protect(authz.ActAdminManageGrants, h.GetAccess))
	mux.Handle("PUT /api/admin/groups/{id}/access", mw.Protect(authz.ActAdminManageGrants, h.SaveAccess))

	// Admin: koneksi database target.
	mux.Handle("GET /api/admin/databases", mw.Protect(authz.ActAdminManageConnections, h.ListConnections))
	mux.Handle("POST /api/admin/databases", mw.Protect(authz.ActAdminManageConnections, h.CreateConnection))
	mux.Handle("POST /api/admin/databases/test", mw.Protect(authz.ActAdminManageConnections, h.TestConnection))
	mux.Handle("GET /api/admin/databases/{id}", mw.Protect(authz.ActAdminManageConnections, h.GetConnection))
	mux.Handle("PUT /api/admin/databases/{id}", mw.Protect(authz.ActAdminManageConnections, h.UpdateConnection))
	mux.Handle("DELETE /api/admin/databases/{id}", mw.Protect(authz.ActAdminManageConnections, h.DeleteConnection))
	mux.Handle("POST /api/admin/databases/{id}/test", mw.Protect(authz.ActAdminManageConnections, h.TestConnection))

	// Approval oleh leader group.
	mux.Handle("GET /api/approvals", mw.Protect(authz.ActApprovalView, h.PendingApprovals))
	mux.Handle("POST /api/executions/{execID}/approve", mw.Protect(authz.ActApprovalDecide, h.Approve))
	mux.Handle("POST /api/executions/{execID}/reject", mw.Protect(authz.ActApprovalDecide, h.Reject))
}
