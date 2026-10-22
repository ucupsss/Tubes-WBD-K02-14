package access_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type mockAccessRepo struct {
	grants map[int64]map[int64]string // groupID -> dbID -> level
}

func newMockAccessRepo() *mockAccessRepo {
	return &mockAccessRepo{
		grants: make(map[int64]map[int64]string),
	}
}

func (m *mockAccessRepo) GetGroupGrants(ctx context.Context, db platformdb.DBTX, groupID int64) ([]access.DatabaseGrant, error) {
	groupGrants, ok := m.grants[groupID]
	if !ok {
		return nil, nil
	}
	var res []access.DatabaseGrant
	for dbID, level := range groupGrants {
		res = append(res, access.DatabaseGrant{
			DatabaseID:   dbID,
			DatabaseName: "db",
			Level:        level,
		})
	}
	return res, nil
}

func (m *mockAccessRepo) GetGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64) (string, error) {
	if groupGrants, ok := m.grants[groupID]; ok {
		if lvl, ok := groupGrants[dbID]; ok {
			return lvl, nil
		}
	}
	return "none", nil
}

func (m *mockAccessRepo) SetGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64, level string, grantedBy int64) error {
	if _, ok := m.grants[groupID]; !ok {
		m.grants[groupID] = make(map[int64]string)
	}
	m.grants[groupID][dbID] = level
	return nil
}

func (m *mockAccessRepo) RevokeGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64) error {
	if groupGrants, ok := m.grants[groupID]; ok {
		delete(groupGrants, dbID)
	}
	return nil
}

type mockBumper struct {
	bumpedGroupIDs []int64
}

func (m *mockBumper) BumpPrivilege(ctx context.Context, userIDs ...int64) error {
	return nil
}

func (m *mockBumper) BumpGroupMembers(ctx context.Context, groupID int64) error {
	m.bumpedGroupIDs = append(m.bumpedGroupIDs, groupID)
	return nil
}

func TestService_SaveAccess_Validation(t *testing.T) {
	repo := newMockAccessRepo()
	bumper := &mockBumper{}
	svc := access.NewService(nil, repo, bumper)

	// Invalid level
	err := svc.SaveAccess(context.Background(), access.SaveAccessInput{
		GroupID: 1,
		Grants:  map[int64]string{10: "invalid_level"},
	})
	if !errors.Is(err, access.ErrInvalidLevel) {
		t.Fatalf("expected ErrInvalidLevel, got %v", err)
	}
}

func TestService_SaveAccess_EscalationBumpsPrivilege(t *testing.T) {
	repo := newMockAccessRepo()
	bumper := &mockBumper{}
	svc := access.NewService(nil, repo, bumper)

	// 1. None -> View (New grant) -> must trigger bump
	err := svc.SaveAccess(context.Background(), access.SaveAccessInput{
		GroupID: 1,
		Grants:  map[int64]string{10: "view"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bumper.bumpedGroupIDs) != 1 || bumper.bumpedGroupIDs[0] != 1 {
		t.Fatalf("expected bump for group 1 on none->view, got: %v", bumper.bumpedGroupIDs)
	}

	// 2. View -> Edit (Escalation) -> must trigger bump
	bumper.bumpedGroupIDs = nil
	err = svc.SaveAccess(context.Background(), access.SaveAccessInput{
		GroupID: 1,
		Grants:  map[int64]string{10: "edit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bumper.bumpedGroupIDs) != 1 {
		t.Fatalf("expected bump on view->edit, got: %v", bumper.bumpedGroupIDs)
	}

	// 3. Edit -> View (Downgrade) -> must NOT trigger bump
	bumper.bumpedGroupIDs = nil
	err = svc.SaveAccess(context.Background(), access.SaveAccessInput{
		GroupID: 1,
		Grants:  map[int64]string{10: "view"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bumper.bumpedGroupIDs) != 0 {
		t.Fatalf("expected no bump on edit->view, got: %v", bumper.bumpedGroupIDs)
	}
}
