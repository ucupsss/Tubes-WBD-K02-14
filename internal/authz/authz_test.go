package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

// stubGrants adalah GrantSource in-memory untuk unit test.
type stubGrants struct {
	levels []authz.Level
	dbs    []authz.DatabaseAccess
	err    error

	gotUserID, gotDBID int64
	calls              int
}

func (s *stubGrants) LevelsForUser(ctx context.Context, userID, dbID int64) ([]authz.Level, error) {
	s.calls++
	s.gotUserID, s.gotDBID = userID, dbID
	return s.levels, s.err
}

func (s *stubGrants) AccessibleDatabases(ctx context.Context, userID int64) ([]authz.DatabaseAccess, error) {
	s.gotUserID = userID
	return s.dbs, s.err
}

var errGrantDB = errors.New("platform db down")

func TestRequirementFor_PolicyTable(t *testing.T) {
	tests := []struct {
		action authz.Action
		want   authz.Requirement
	}{
		{authz.ActAdminManageUsers, authz.Requirement{Admin: true}},
		{authz.ActAdminManageGroups, authz.Requirement{Admin: true}},
		{authz.ActAdminManageConnections, authz.Requirement{Admin: true}},
		{authz.ActAdminManageGrants, authz.Requirement{Admin: true}},
		{authz.ActDBView, authz.Requirement{MinLevel: authz.LevelView}},
		{authz.ActMigrationView, authz.Requirement{MinLevel: authz.LevelView}},
		{authz.ActMigrationCreate, authz.Requirement{MinLevel: authz.LevelEdit}},
		{authz.ActMigrationEdit, authz.Requirement{MinLevel: authz.LevelEdit}},
		{authz.ActMigrationExecute, authz.Requirement{MinLevel: authz.LevelEdit}},
		{authz.ActMigrationDelete, authz.Requirement{MinLevel: authz.LevelEdit}},
		{authz.ActMigrationExplain, authz.Requirement{MinLevel: authz.LevelEdit}},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			got, ok := authz.RequirementFor(tt.action)
			if !ok {
				t.Fatalf("action %q tidak ada di policy", tt.action)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRequirementFor_UnknownAction(t *testing.T) {
	if _, ok := authz.RequirementFor("db.drop_everything"); ok {
		t.Fatal("aksi tidak dikenal seharusnya tidak ada di policy")
	}
}

func TestAuthorizer_EffectiveLevel(t *testing.T) {
	tests := []struct {
		name   string
		levels []authz.Level
		want   authz.Level
	}{
		{"no group", nil, authz.LevelNone},
		{"view only", []authz.Level{authz.LevelView}, authz.LevelView},
		{"edit only", []authz.Level{authz.LevelEdit}, authz.LevelEdit},
		{"view + edit = edit", []authz.Level{authz.LevelView, authz.LevelEdit}, authz.LevelEdit},
		{"edit + view = edit", []authz.Level{authz.LevelEdit, authz.LevelView}, authz.LevelEdit},
		{"none + view = view", []authz.Level{authz.LevelNone, authz.LevelView}, authz.LevelView},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &stubGrants{levels: tt.levels}
			got, err := authz.NewAuthorizer(g).EffectiveLevel(context.Background(), 7, 3)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if g.gotUserID != 7 || g.gotDBID != 3 {
				t.Fatalf("GrantSource dipanggil dengan user=%d db=%d, want 7/3", g.gotUserID, g.gotDBID)
			}
		})
	}
}

func TestAuthorizer_EffectiveLevel_GrantError(t *testing.T) {
	_, err := authz.NewAuthorizer(&stubGrants{err: errGrantDB}).EffectiveLevel(context.Background(), 1, 1)
	if !errors.Is(err, errGrantDB) {
		t.Fatalf("err = %v, want wrap %v", err, errGrantDB)
	}
}

func TestAuthorizer_AccessibleDatabases(t *testing.T) {
	want := []authz.DatabaseAccess{{DatabaseID: 1, Name: "db1", Level: authz.LevelEdit}}
	g := &stubGrants{dbs: want}
	got, err := authz.NewAuthorizer(g).AccessibleDatabases(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != want[0] || g.gotUserID != 5 {
		t.Fatalf("got %+v (user %d), want %+v (user 5)", got, g.gotUserID, want)
	}

	if _, err := authz.NewAuthorizer(&stubGrants{err: errGrantDB}).AccessibleDatabases(context.Background(), 5); !errors.Is(err, errGrantDB) {
		t.Fatalf("err = %v, want wrap %v", err, errGrantDB)
	}
}

func TestAuthorizer_Can(t *testing.T) {
	admin := authz.Principal{UserID: 1, Username: "admin", IsAdmin: true}
	user := authz.Principal{UserID: 2, Username: "alice"}

	tests := []struct {
		name   string
		p      authz.Principal
		action authz.Action
		levels []authz.Level
		want   bool
	}{
		{"admin manage users", admin, authz.ActAdminManageUsers, nil, true},
		{"non-admin manage users", user, authz.ActAdminManageUsers, []authz.Level{authz.LevelEdit}, false},
		{"non-admin manage grants", user, authz.ActAdminManageGrants, nil, false},
		{"admin without grant cannot view db (D2)", admin, authz.ActDBView, nil, false},
		{"admin with view grant can view db", admin, authz.ActDBView, []authz.Level{authz.LevelView}, true},
		{"no grant view db", user, authz.ActDBView, nil, false},
		{"view grant view db", user, authz.ActDBView, []authz.Level{authz.LevelView}, true},
		{"view grant migration view", user, authz.ActMigrationView, []authz.Level{authz.LevelView}, true},
		{"view grant migration create", user, authz.ActMigrationCreate, []authz.Level{authz.LevelView}, false},
		{"view grant migration execute", user, authz.ActMigrationExecute, []authz.Level{authz.LevelView}, false},
		{"view grant migration explain", user, authz.ActMigrationExplain, []authz.Level{authz.LevelView}, false},
		{"edit grant migration execute", user, authz.ActMigrationExecute, []authz.Level{authz.LevelEdit}, true},
		{"edit grant db view", user, authz.ActDBView, []authz.Level{authz.LevelEdit}, true},
		{"view + edit groups execute (D1)", user, authz.ActMigrationExecute, []authz.Level{authz.LevelView, authz.LevelEdit}, true},
		{"unknown action denied", admin, "db.unknown", []authz.Level{authz.LevelEdit}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := authz.NewAuthorizer(&stubGrants{levels: tt.levels}).Can(context.Background(), tt.p, tt.action, 9)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthorizer_Can_AdminActionDoesNotQueryGrants(t *testing.T) {
	g := &stubGrants{err: errGrantDB}
	ok, err := authz.NewAuthorizer(g).Can(context.Background(), authz.Principal{UserID: 1, IsAdmin: true}, authz.ActAdminManageUsers, 0)
	if err != nil || !ok {
		t.Fatalf("got (%v, %v), want (true, nil)", ok, err)
	}
	if g.calls != 0 {
		t.Fatalf("GrantSource dipanggil %d kali untuk aksi admin, want 0", g.calls)
	}
}

func TestAuthorizer_Can_GrantError(t *testing.T) {
	ok, err := authz.NewAuthorizer(&stubGrants{err: errGrantDB}).Can(context.Background(), authz.Principal{UserID: 2}, authz.ActDBView, 1)
	if !errors.Is(err, errGrantDB) {
		t.Fatalf("err = %v, want wrap %v", err, errGrantDB)
	}
	if ok {
		t.Fatal("akses harus ditolak saat GrantSource error")
	}
}
