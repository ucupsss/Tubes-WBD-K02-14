package session

import (
	"context"
	"errors"
	"time"
)

const (
	CookieName = "dbmp_session"
	// touchInterval membatasi update last_seen_at maksimal 1x per menit per session.
	touchInterval = time.Minute
)

// Config adalah parameter session (D3). Diisi main.go dari env SESSION_*.
type Config struct {
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
	Secure           bool // cookie Secure; true bila APP_ENV=production
}

// DefaultConfig: idle 30 menit, absolute 8 jam, rotasi 15 menit, grace 30 detik (D3).
func DefaultConfig() Config {
	return Config{
		IdleTimeout:      30 * time.Minute,
		AbsoluteTimeout:  8 * time.Hour,
		RotationInterval: 15 * time.Minute,
		GracePeriod:      30 * time.Second,
	}
}

// Session adalah baris tabel sessions. Token mentah tidak pernah disimpan, hanya SHA-256-nya (D4).
type Session struct {
	ID                int64
	UserID            int64
	TokenHash         []byte
	PrevTokenHash     []byte
	PrevValidUntil    *time.Time
	CSRFToken         string
	PrivilegeVersion  int32
	CreatedAt         time.Time
	LastSeenAt        time.Time
	RotatedAt         time.Time
	AbsoluteExpiresAt time.Time
	RevokedAt         *time.Time
}

// User adalah data user yang dibutuhkan session untuk validasi tiap request.
type User struct {
	ID               int64
	Username         string
	IsAdmin          bool
	IsActive         bool
	PrivilegeVersion int32
}

// Users memuat ulang status user. Diimplementasi auth.Service.
// Harus mengembalikan error ber-Kind NotFound (apperr) bila user tidak ada.
type Users interface {
	LookupUser(ctx context.Context, userID int64) (User, error)
}

// Clock adalah sumber waktu. Sama bentuknya dengan clock.Clock milik C, jadi bisa langsung dipakai.
type Clock interface {
	Now() time.Time
}

// ErrInvalid berarti cookie tidak menunjuk ke session yang masih berlaku
// (tidak ditemukan, revoked, expired, idle, atau user nonaktif). Tidak ditampilkan ke user.
var ErrInvalid = errors.New("session invalid")
