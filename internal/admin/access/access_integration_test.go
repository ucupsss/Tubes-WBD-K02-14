//go:build integration

package access_test

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestPg_SaveAccessReflectedInEffectiveLevel(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", false)
	g := testutil.CreateGroup(t, pool, "analis")
	testutil.AddMember(t, pool, g, bob)
	targetName, _ := testutil.NewTargetDB(t, "")
	dbID := testutil.CreateConnection(t, pool, targetName)

	svc := access.NewService(pool, access.NewPgRepo(), auth.NewPrivilegeBumper(pool))
	az := authz.NewAuthorizer(dbconn.NewGrantRepo(pool))
	level := func() authz.Level {
		t.Helper()
		l, err := az.EffectiveLevel(ctx, bob, dbID)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	version := func() int32 {
		t.Helper()
		var v int32
		if err := pool.QueryRow(ctx, `SELECT privilege_version FROM users WHERE id = $1`, bob).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	save := func(lvl string) {
		t.Helper()
		if err := svc.SaveAccess(ctx, access.SaveAccessInput{GroupID: g, GrantedBy: admin, Grants: map[int64]string{dbID: lvl}}); err != nil {
			t.Fatal(err)
		}
	}

	if level() != authz.LevelNone {
		t.Fatal("belum ada grant: harus none")
	}
	save("view")
	if level() != authz.LevelView || version() != 2 {
		t.Fatalf("grant view: level=%v version=%d, want view & privilege naik", level(), version())
	}
	save("edit")
	if level() != authz.LevelEdit || version() != 3 {
		t.Fatalf("naik ke edit: level=%v version=%d, want edit & privilege naik", level(), version())
	}
	save("view")
	if level() != authz.LevelView || version() != 3 {
		t.Fatalf("turun ke view: level=%v version=%d, want view tanpa bump", level(), version())
	}
	save("none")
	if level() != authz.LevelNone {
		t.Fatalf("dicabut: level=%v, want none", level())
	}

	matrix, err := svc.GetAccessMatrix(ctx, g)
	if err != nil || len(matrix) != 1 || matrix[0].Level != "none" {
		t.Fatalf("matriks akses = %+v, %v", matrix, err)
	}

	if err := svc.SaveAccess(ctx, access.SaveAccessInput{GroupID: g, Grants: map[int64]string{dbID: "superuser"}}); err == nil {
		t.Fatal("level tidak dikenal harus ditolak")
	}
}
