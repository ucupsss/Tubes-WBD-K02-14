//go:build integration

package groups_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestPg_GroupCRUD(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	repo := groups.NewPgRepo()
	ctx := context.Background()

	id, err := repo.Create(ctx, pool, groups.Input{Name: "analis", Description: "tim analis"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, pool, groups.Input{Name: "analis"}); !errors.Is(err, groups.ErrNameTaken) {
		t.Fatalf("nama duplikat: err = %v", err)
	}
	other, _ := repo.Create(ctx, pool, groups.Input{Name: "developer"})
	if err := repo.Update(ctx, pool, other, groups.Input{Name: "analis"}); !errors.Is(err, groups.ErrNameTaken) {
		t.Fatalf("rename ke nama terpakai: err = %v", err)
	}
	if err := repo.Update(ctx, pool, id, groups.Input{Name: "analis-data", Description: "baru"}); err != nil {
		t.Fatal(err)
	}
	g, err := repo.Get(ctx, pool, id)
	if err != nil || g.Name != "analis-data" || g.Description != "baru" || g.MemberCount != 0 {
		t.Fatalf("Get = %+v, %v", g, err)
	}

	list, err := repo.List(ctx, pool)
	if err != nil || len(list) != 2 || list[0].Name != "analis-data" {
		t.Fatalf("List = %+v, %v", list, err)
	}

	for name, err := range map[string]error{
		"get":    func() error { _, err := repo.Get(ctx, pool, 999); return err }(),
		"update": repo.Update(ctx, pool, 999, groups.Input{Name: "x"}),
		"delete": repo.Delete(ctx, pool, 999),
	} {
		if !errors.Is(err, groups.ErrNotFound) {
			t.Fatalf("%s tidak ada: err = %v", name, err)
		}
	}
}

func TestPg_Members(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	repo := groups.NewPgRepo()
	ctx := context.Background()
	g := testutil.CreateGroup(t, pool, "analis")
	alice := testutil.CreateUser(t, pool, "alice", false)
	bob := testutil.CreateUser(t, pool, "bob", false)
	carol := testutil.CreateUser(t, pool, "carol", false)
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, carol); err != nil {
		t.Fatal(err)
	}

	if added, err := repo.AddMember(ctx, pool, g, alice); err != nil || !added {
		t.Fatalf("AddMember = %v, %v", added, err)
	}
	if added, err := repo.AddMember(ctx, pool, g, alice); err != nil || added {
		t.Fatalf("AddMember ulang = %v, %v, want false tanpa error", added, err)
	}
	if _, err := repo.AddMember(ctx, pool, g, 999); !errors.Is(err, groups.ErrUserNotFound) {
		t.Fatalf("user tidak ada: err = %v", err)
	}

	members, err := repo.ListMembers(ctx, pool, g)
	if err != nil || len(members) != 1 || members[0].UserID != alice || !members[0].IsActive {
		t.Fatalf("ListMembers = %+v, %v", members, err)
	}
	cands, err := repo.ListCandidates(ctx, pool, g)
	if err != nil || len(cands) != 1 || cands[0].ID != bob {
		t.Fatalf("kandidat hanya user aktif non-anggota (bob), got %+v, %v", cands, err)
	}
	if gr, _ := repo.Get(ctx, pool, g); gr.MemberCount != 1 {
		t.Fatalf("MemberCount = %d, want 1", gr.MemberCount)
	}

	if err := repo.RemoveMember(ctx, pool, g, alice); err != nil {
		t.Fatal(err)
	}
	if members, _ := repo.ListMembers(ctx, pool, g); len(members) != 0 {
		t.Fatal("alice harus keluar")
	}

	testutil.AddMember(t, pool, g, bob)
	targetName, _ := testutil.NewTargetDB(t, "")
	testutil.Grant(t, pool, g, testutil.CreateConnection(t, pool, targetName), "edit")
	if err := repo.Delete(ctx, pool, g); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id = $1`, g).Scan(&n); err != nil || n != 0 {
		t.Fatalf("keanggotaan harus ikut terhapus (cascade), sisa %d", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_db_access WHERE group_id = $1`, g).Scan(&n); err != nil || n != 0 {
		t.Fatalf("grant group harus ikut terhapus (cascade), sisa %d", n)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_db_access (group_id, database_id, level) VALUES (999999, 1, 'view')`); err == nil {
		t.Fatal("grant untuk group yang tidak ada harus ditolak FK")
	}
}

func TestPg_AddMemberBumpsPrivilege(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	svc := groups.NewService(pool, groups.NewPgRepo(), auth.NewPrivilegeBumper(pool))
	g := testutil.CreateGroup(t, pool, "analis")
	bob := testutil.CreateUser(t, pool, "bob", false)

	version := func() int32 {
		var v int32
		if err := pool.QueryRow(ctx, `SELECT privilege_version FROM users WHERE id = $1`, bob).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if err := svc.AddMember(ctx, g, bob); err != nil {
		t.Fatal(err)
	}
	if version() != 2 {
		t.Fatalf("masuk group: version = %d, want 2 (D6)", version())
	}
	if err := svc.AddMember(ctx, g, bob); err != nil || version() != 2 {
		t.Fatalf("sudah anggota: version = %d err = %v, want tetap 2", version(), err)
	}
	if err := svc.RemoveMember(ctx, g, bob); err != nil || version() != 2 {
		t.Fatalf("keluar group: version = %d err = %v, want tetap 2", version(), err)
	}
	if err := svc.AddMember(ctx, 999, bob); !errors.Is(err, groups.ErrNotFound) {
		t.Fatalf("group tidak ada: err = %v", err)
	}
}

func TestPg_Leader(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	svc := groups.NewService(pool, groups.NewPgRepo(), auth.NewPrivilegeBumper(pool))
	ctx := context.Background()

	gid, err := svc.Create(ctx, groups.Input{Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	alice := testutil.CreateUser(t, pool, "alice", false)
	bob := testutil.CreateUser(t, pool, "bob", false)
	if err := svc.AddMember(ctx, gid, alice); err != nil {
		t.Fatal(err)
	}

	// Bukan anggota → ditolak; anggota → diterima.
	if err := svc.SetLeader(ctx, gid, &bob); !errors.Is(err, groups.ErrLeaderNotMember) {
		t.Fatalf("bob bukan anggota: err = %v", err)
	}
	if err := svc.SetLeader(ctx, gid, &alice); err != nil {
		t.Fatalf("SetLeader: %v", err)
	}

	d, err := svc.Detail(ctx, gid)
	if err != nil {
		t.Fatal(err)
	}
	if d.Group.LeaderID == nil || *d.Group.LeaderID != alice || d.Group.LeaderName != "alice" {
		t.Fatalf("group = %+v", d.Group)
	}
	if len(d.Members) != 1 || !d.Members[0].IsLeader {
		t.Fatalf("members = %+v", d.Members)
	}
	if names, _ := svc.LedGroupNames(ctx, alice); len(names) != 1 || names[0] != "dev" {
		t.Fatalf("LedGroupNames = %v", names)
	}

	// Leader tidak boleh dikeluarkan.
	if err := svc.RemoveMember(ctx, gid, alice); !errors.Is(err, groups.ErrIsLeader) {
		t.Fatalf("RemoveMember leader: err = %v", err)
	}

	// Menghapus user yang jadi leader mengosongkan leader_id (ON DELETE SET NULL).
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	g, _ := svc.Get(ctx, gid)
	if g.LeaderID != nil {
		t.Fatalf("leader_id seharusnya NULL, got %v", *g.LeaderID)
	}
}
