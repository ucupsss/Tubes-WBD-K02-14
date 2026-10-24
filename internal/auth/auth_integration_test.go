//go:build integration

package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func privilegeVersion(t *testing.T, db platformdb.DBTX, userID int64) int32 {
	t.Helper()
	var v int32
	if err := db.QueryRow(context.Background(), `SELECT privilege_version FROM users WHERE id = $1`, userID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPg_Repo(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	repo := auth.NewPgRepo()
	ctx := context.Background()

	if ok, err := repo.HasAnyUser(ctx, pool); err != nil || ok {
		t.Fatalf("DB baru: HasAnyUser = %v, %v", ok, err)
	}
	id := testutil.CreateUser(t, pool, "alice", true)

	u, err := repo.GetByUsername(ctx, pool, "alice")
	if err != nil || u.ID != id || !u.IsAdmin || !u.IsActive || u.PrivilegeVersion != 1 {
		t.Fatalf("GetByUsername = %+v, %v", u, err)
	}
	if u2, err := repo.GetByID(ctx, pool, id); err != nil || u2.Username != "alice" {
		t.Fatalf("GetByID = %+v, %v", u2, err)
	}
	if _, err := repo.GetByID(ctx, pool, 999); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("GetByID tidak ada: err = %v", err)
	}
	if _, err := repo.Create(ctx, pool, auth.User{Username: "alice", PasswordHash: "x", IsActive: true}); !errors.Is(err, auth.ErrUsernameTaken) {
		t.Fatalf("username duplikat: err = %v", err)
	}
	if ok, err := repo.HasAnyUser(ctx, pool); err != nil || !ok {
		t.Fatalf("HasAnyUser = %v, %v", ok, err)
	}
}

func TestPg_Authenticate(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	svc := auth.NewService(pool, auth.NewPgRepo())
	ctx := context.Background()
	testutil.CreateUser(t, pool, "alice", false)
	bob := testutil.CreateUser(t, pool, "bob", false)
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, bob); err != nil {
		t.Fatal(err)
	}

	if u, err := svc.Authenticate(ctx, "alice", testutil.TestPassword); err != nil || u.Username != "alice" {
		t.Fatalf("login benar: %+v, %v", u, err)
	}
	for _, tt := range []struct{ name, user, pass string }{
		{"password salah", "alice", "salahsalah"},
		{"user tidak ada", "mallory", testutil.TestPassword},
		{"user nonaktif", "bob", testutil.TestPassword},
	} {
		if _, err := svc.Authenticate(ctx, tt.user, tt.pass); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("%s: err = %v, want ErrInvalidCredentials", tt.name, err)
		}
	}
}

func TestPg_EnsureBootstrapAdmin(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	svc := auth.NewService(pool, auth.NewPgRepo())
	ctx := context.Background()

	created, err := svc.EnsureBootstrapAdmin(ctx, "admin", "adminpass123")
	if err != nil || !created {
		t.Fatalf("DB kosong: created=%v err=%v", created, err)
	}
	u, err := svc.Authenticate(ctx, "admin", "adminpass123")
	if err != nil || !u.IsAdmin {
		t.Fatalf("admin bootstrap harus bisa login sebagai admin: %+v, %v", u, err)
	}

	created, err = svc.EnsureBootstrapAdmin(ctx, "admin2", "adminpass123")
	if err != nil || created {
		t.Fatalf("sudah ada user: created=%v err=%v, want false", created, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jumlah user = %d, want 1", n)
	}
}

func TestPg_PrivilegeBumper(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	b := auth.NewPrivilegeBumper(pool)
	ctx := context.Background()
	alice := testutil.CreateUser(t, pool, "alice", false)
	bob := testutil.CreateUser(t, pool, "bob", false)
	carol := testutil.CreateUser(t, pool, "carol", false)

	if err := b.BumpPrivilege(ctx, alice, bob); err != nil {
		t.Fatal(err)
	}
	if privilegeVersion(t, pool, alice) != 2 || privilegeVersion(t, pool, bob) != 2 || privilegeVersion(t, pool, carol) != 1 {
		t.Fatal("BumpPrivilege hanya menaikkan user yang disebut")
	}

	g := testutil.CreateGroup(t, pool, "analis")
	testutil.AddMember(t, pool, g, carol)
	if err := b.BumpGroupMembers(ctx, g); err != nil {
		t.Fatal(err)
	}
	if privilegeVersion(t, pool, carol) != 2 || privilegeVersion(t, pool, alice) != 2 {
		t.Fatal("BumpGroupMembers hanya menaikkan anggota group")
	}
}
