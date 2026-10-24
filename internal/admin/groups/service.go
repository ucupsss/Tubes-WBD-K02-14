package groups

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
	return &Service{db: db, repo: repo, bumper: bumper}
}

func (s *Service) List(ctx context.Context) ([]Group, error) {
	return s.repo.List(ctx, s.db)
}

func (s *Service) Get(ctx context.Context, id int64) (Group, error) {
	return s.repo.Get(ctx, s.db, id)
}

func (s *Service) Detail(ctx context.Context, id int64) (Detail, error) {
	g, err := s.repo.Get(ctx, s.db, id)
	if err != nil {
		return Detail{}, err
	}
	members, err := s.repo.ListMembers(ctx, s.db, id)
	if err != nil {
		return Detail{}, err
	}
	candidates, err := s.repo.ListCandidates(ctx, s.db, id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Group: g, Members: members, Candidates: candidates}, nil
}

func (s *Service) Create(ctx context.Context, in Input) (int64, error) {
	if err := in.Validate(); err != nil {
		return 0, err
	}
	return s.repo.Create(ctx, s.db, in.normalized())
}

func (s *Service) Update(ctx context.Context, id int64, in Input) error {
	if err := in.Validate(); err != nil {
		return err
	}
	return s.repo.Update(ctx, s.db, id, in.normalized())
}

// Delete menghapus group beserta keanggotaannya. Akses yang hilang langsung berlaku karena
// akses efektif dihitung ulang dari DB di setiap request; tidak perlu bump privilege.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, s.db, id)
}

// AddMember menambahkan user ke group. User yang baru masuk group mendapat privilege baru,
// jadi privilege_version-nya dinaikkan supaya session dirotasi (D6).
func (s *Service) AddMember(ctx context.Context, groupID, userID int64) error {
	if _, err := s.repo.Get(ctx, s.db, groupID); err != nil {
		return err
	}
	added, err := s.repo.AddMember(ctx, s.db, groupID, userID)
	if err != nil {
		return err
	}
	if !added {
		return nil
	}
	if err := s.bumper.BumpPrivilege(ctx, userID); err != nil {
		return fmt.Errorf("groups.AddMember: bump privilege: %w", err)
	}
	return nil
}

// RemoveMember mengeluarkan user dari group. Privilege turun, jadi tidak ada bump.
func (s *Service) RemoveMember(ctx context.Context, groupID, userID int64) error {
	if _, err := s.repo.Get(ctx, s.db, groupID); err != nil {
		return err
	}
	return s.repo.RemoveMember(ctx, s.db, groupID, userID)
}
