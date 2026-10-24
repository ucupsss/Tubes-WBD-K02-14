package users

import (
	"context"
	"fmt"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// Accounts adalah bagian auth.Service untuk membuat user (validasi + hash password ada di sana).
type Accounts interface {
	CreateUser(ctx context.Context, in auth.CreateUserInput) (int64, error)
}

// SessionRevoker diimplementasi session.Manager.
type SessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID int64) error
}

type Service struct {
	db       platformdb.DBTX
	repo     Repo
	accounts Accounts
	sessions SessionRevoker
}

func NewService(db platformdb.DBTX, repo Repo, accounts Accounts, sessions SessionRevoker) *Service {
	return &Service{db: db, repo: repo, accounts: accounts, sessions: sessions}
}

func (s *Service) List(ctx context.Context) ([]User, error) {
	return s.repo.List(ctx, s.db)
}

func (s *Service) Get(ctx context.Context, id int64) (User, error) {
	return s.repo.Get(ctx, s.db, id)
}

func (s *Service) Create(ctx context.Context, in auth.CreateUserInput) (int64, error) {
	return s.accounts.CreateUser(ctx, in)
}

// Update mengubah status admin/aktif user.
//   - Dijadikan admin → privilege_version++ sehingga session dirotasi (D6).
//   - Admin dicabut atau dinonaktifkan → semua session user dicabut (D7).
//
// Admin tidak bisa mencabut admin / menonaktifkan dirinya sendiri, sehingga selalu tersisa minimal satu admin aktif.
func (s *Service) Update(ctx context.Context, actorID, id int64, in UpdateInput) error {
	if actorID == id && (!in.IsAdmin || !in.IsActive) {
		return ErrSelfLockout
	}
	cur, err := s.repo.Get(ctx, s.db, id)
	if err != nil {
		return err
	}

	promoted := !cur.IsAdmin && in.IsAdmin
	demoted := cur.IsAdmin && !in.IsAdmin
	deactivated := cur.IsActive && !in.IsActive

	if err := s.repo.UpdateFlags(ctx, s.db, id, in, promoted); err != nil {
		return err
	}
	// Status admin/aktif tetap dibaca ulang dari DB di setiap request (session.Resolve), jadi revoke di sini
	// adalah lapisan tambahan; bila gagal, error tetap dilaporkan.
	if demoted || deactivated {
		if err := s.sessions.RevokeAllForUser(ctx, id); err != nil {
			return fmt.Errorf("users.Update: revoke sessions: %w", err)
		}
	}
	return nil
}

// ResetPassword mengganti password user. Session user lain yang di-reset ikut dicabut supaya
// sesi lama (mungkin milik penyerang) tidak bisa dipakai lagi; session admin sendiri dibiarkan.
func (s *Service) ResetPassword(ctx context.Context, actorID, id int64, password string) error {
	if msg := auth.ValidatePassword(password); msg != "" {
		return apperr.InvalidFields("Input tidak valid", map[string]string{"password": msg})
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.repo.SetPasswordHash(ctx, s.db, id, hash); err != nil {
		return err
	}
	if actorID != id {
		if err := s.sessions.RevokeAllForUser(ctx, id); err != nil {
			return fmt.Errorf("users.ResetPassword: revoke sessions: %w", err)
		}
	}
	return nil
}
