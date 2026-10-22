package access

import (
	"context"
	"fmt"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type Service struct {
	db     platformdb.DBTX
	repo   Repo
	bumper auth.PrivilegeBumper
}

func NewService(db platformdb.DBTX, repo Repo, bumper auth.PrivilegeBumper) *Service {
	return &Service{
		db:     db,
		repo:   repo,
		bumper: bumper,
	}
}

// GetAccessMatrix mengambil daftar seluruh database dan level akses grup saat ini.
func (s *Service) GetAccessMatrix(ctx context.Context, groupID int64) ([]DatabaseGrant, error) {
	return s.repo.GetGroupGrants(ctx, s.db, groupID)
}

// SaveAccess memperbarui hak akses grup ke sejumlah target database.
// Jika hak akses baru diberikan atau dinaikkan (view -> edit), versi privilege anggota grup dinaikkan (D6).
func (s *Service) SaveAccess(ctx context.Context, in SaveAccessInput) error {
	var escalated bool

	for dbID, newLevel := range in.Grants {
		if newLevel != "none" && newLevel != "view" && newLevel != "edit" {
			return ErrInvalidLevel
		}

		oldLevel, err := s.repo.GetGrant(ctx, s.db, in.GroupID, dbID)
		if err != nil {
			return err
		}

		if newLevel == "none" {
			if err := s.repo.RevokeGrant(ctx, s.db, in.GroupID, dbID); err != nil {
				return err
			}
		} else {
			if err := s.repo.SetGrant(ctx, s.db, in.GroupID, dbID, newLevel, in.GrantedBy); err != nil {
				return err
			}
		}

		// Deteksi kenaikan privilege:
		// 1. Dari none -> view atau edit (grant baru)
		// 2. Dari view -> edit (eskalasi wewenang)
		if (oldLevel == "none" && (newLevel == "view" || newLevel == "edit")) ||
			(oldLevel == "view" && newLevel == "edit") {
			escalated = true
		}
	}

	// Trigger privilege bumper untuk rotasi sesi anggota grup jika ada kenaikan akses (D6)
	if escalated && s.bumper != nil {
		if err := s.bumper.BumpGroupMembers(ctx, in.GroupID); err != nil {
			return fmt.Errorf("gagal menaikkan versi privilege anggota grup: %w", err)
		}
	}

	return nil
}
