package auth

import (
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
)

// User adalah baris tabel users. PasswordHash JANGAN pernah dirender atau di-log.
type User struct {
	ID               int64
	Username         string
	PasswordHash     string
	IsAdmin          bool
	IsActive         bool
	PrivilegeVersion int32
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Principal mengubah User menjadi identitas yang dipakai authz.
func (u User) Principal() authz.Principal {
	return authz.Principal{UserID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin}
}

// SessionUser mengubah User menjadi data yang dibutuhkan package session.
func (u User) SessionUser() session.User {
	return session.User{
		ID:               u.ID,
		Username:         u.Username,
		IsAdmin:          u.IsAdmin,
		IsActive:         u.IsActive,
		PrivilegeVersion: u.PrivilegeVersion,
	}
}

var (
	ErrNotFound = apperr.NotFound("User tidak ditemukan")
	// ErrInvalidCredentials sengaja generik: tidak membedakan username salah, password salah, atau user nonaktif.
	ErrInvalidCredentials = apperr.Invalid("Username atau password salah")
	ErrUsernameTaken      = apperr.Conflict("Username sudah dipakai")
)
