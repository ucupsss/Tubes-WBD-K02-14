package users

import (
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

var (
	ErrNotFound    = apperr.NotFound("User tidak ditemukan")
	ErrSelfLockout = apperr.Conflict("Anda tidak bisa mencabut status admin atau menonaktifkan akun sendiri")
	// ErrIsLeader: user yang masih memimpin group tidak boleh dinonaktifkan (D28).
	ErrIsLeader = apperr.Conflict("User ini masih menjadi leader group. Ganti leader terlebih dahulu").WithReason("is_leader")
)

// User adalah tampilan user untuk halaman admin (tanpa password hash).
type User struct {
	ID        int64
	Username  string
	IsAdmin   bool
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpdateInput berisi status yang bisa diubah admin. Tidak ada hard delete (D8): user dinonaktifkan.
type UpdateInput struct {
	IsAdmin  bool
	IsActive bool
}
