package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// Manager membuat, memvalidasi, merotasi, dan mencabut session.
type Manager struct {
	db    platformdb.DBTX
	repo  Repo
	users Users
	clock Clock
	cfg   Config
}

func NewManager(db platformdb.DBTX, repo Repo, users Users, clk Clock, cfg Config) *Manager {
	return &Manager{db: db, repo: repo, users: users, clock: clk, cfg: cfg}
}

// Result adalah hasil Resolve untuk satu request.
type Result struct {
	Session Session
	User    User
	// NewToken terisi bila session baru saja dirotasi; middleware wajib mengirim Set-Cookie.
	NewToken string
}

// Create membuat session baru untuk user yang baru login (login = rotasi). Mengembalikan token mentah
// untuk cookie; token ini tidak disimpan di mana pun selain cookie.
func (m *Manager) Create(ctx context.Context, u User) (string, Session, error) {
	token, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	csrfToken, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	now := m.clock.Now()
	s := Session{
		UserID:            u.ID,
		TokenHash:         hashToken(token),
		CSRFToken:         csrfToken,
		PrivilegeVersion:  u.PrivilegeVersion,
		CreatedAt:         now,
		LastSeenAt:        now,
		RotatedAt:         now,
		AbsoluteExpiresAt: now.Add(m.cfg.AbsoluteTimeout),
	}
	id, err := m.repo.Create(ctx, m.db, s)
	if err != nil {
		return "", Session{}, err
	}
	s.ID = id
	return token, s, nil
}

// Resolve memvalidasi token dari cookie. ErrInvalid bila session tidak berlaku.
func (m *Manager) Resolve(ctx context.Context, token string) (Result, error) {
	if token == "" {
		return Result{}, ErrInvalid
	}
	now := m.clock.Now()
	hash := hashToken(token)

	s, err := m.repo.FindByTokenHash(ctx, m.db, hash, now)
	if err != nil {
		return Result{}, err
	}
	if err := checkValid(s, m.cfg, now); err != nil {
		return Result{}, err
	}

	u, err := m.users.LookupUser(ctx, s.UserID)
	if apperr.KindOf(err) == apperr.KindNotFound {
		return Result{}, ErrInvalid
	}
	if err != nil {
		return Result{}, fmt.Errorf("session.Resolve: %w", err)
	}
	if !u.IsActive {
		return Result{}, ErrInvalid
	}

	res := Result{Session: s, User: u}
	usingGraceToken := !bytes.Equal(s.TokenHash, hash)

	if shouldRotate(s, usingGraceToken, u.PrivilegeVersion, m.cfg, now) {
		newTok, err := newToken()
		if err != nil {
			return Result{}, err
		}
		newHash := hashToken(newTok)
		rotated, err := m.repo.Rotate(ctx, m.db, RotateInput{
			ID:               s.ID,
			OldHash:          s.TokenHash,
			NewHash:          newHash,
			PrevValidUntil:   now.Add(m.cfg.GracePeriod),
			Now:              now,
			PrivilegeVersion: u.PrivilegeVersion,
		})
		if err != nil {
			return Result{}, err
		}
		// rotated=false: request paralel sudah merotasi duluan. Token ini kini berstatus grace,
		// jadi request tetap dilayani tanpa Set-Cookie (D5).
		if rotated {
			res.NewToken = newTok
			res.Session.PrevTokenHash = s.TokenHash
			res.Session.TokenHash = newHash
			res.Session.RotatedAt = now
			res.Session.LastSeenAt = now
			res.Session.PrivilegeVersion = u.PrivilegeVersion
		}
		return res, nil
	}

	if shouldTouch(s, now) {
		if err := m.repo.Touch(ctx, m.db, s.ID, now); err != nil {
			return Result{}, err
		}
		res.Session.LastSeenAt = now
	}
	return res, nil
}

// Revoke menonaktifkan satu session di backend (logout).
func (m *Manager) Revoke(ctx context.Context, sessionID int64) error {
	return m.repo.Revoke(ctx, m.db, sessionID, m.clock.Now())
}

// RevokeAllForUser mematikan semua session user (user dinonaktifkan / admin dicabut, D7).
func (m *Manager) RevokeAllForUser(ctx context.Context, userID int64) error {
	return m.repo.RevokeAllForUser(ctx, m.db, userID, m.clock.Now())
}

// checkValid menolak session yang revoked, melewati absolute expiration, atau idle terlalu lama.
func checkValid(s Session, cfg Config, now time.Time) error {
	switch {
	case s.RevokedAt != nil:
		return fmt.Errorf("%w: revoked", ErrInvalid)
	case !now.Before(s.AbsoluteExpiresAt):
		return fmt.Errorf("%w: absolute expired", ErrInvalid)
	case now.Sub(s.LastSeenAt) > cfg.IdleTimeout:
		return fmt.Errorf("%w: idle timeout", ErrInvalid)
	}
	return nil
}

// shouldRotate: rotasi hanya untuk token current (bukan grace, D5), saat interval lewat
// atau privilege user berubah sejak session dibuat/dirotasi (D6).
func shouldRotate(s Session, usingGraceToken bool, userPrivilegeVersion int32, cfg Config, now time.Time) bool {
	if usingGraceToken {
		return false
	}
	return now.Sub(s.RotatedAt) >= cfg.RotationInterval || s.PrivilegeVersion != userPrivilegeVersion
}

func shouldTouch(s Session, now time.Time) bool {
	return now.Sub(s.LastSeenAt) >= touchInterval
}

// IsInvalid bernilai true untuk semua error "session tidak berlaku".
func IsInvalid(err error) bool { return errors.Is(err, ErrInvalid) }
