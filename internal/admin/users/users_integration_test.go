//go:build integration

package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func TestPg_Repo(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	repo := users.NewPgRepo()
	ctx := context.Background()
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", false)

	list, err := repo.List(ctx, pool)
	if err != nil || len(list) != 2 || list[0].Username != "admin" || list[1].Username != "bob" {
		t.Fatalf("List = %+v, %v (urut username)", list, err)
	}

	if err := repo.UpdateFlags(ctx, pool, bob, users.UpdateInput{IsAdmin: true, IsActive: true}, true); err != nil {
		t.Fatal(err)
	}
	var isAdmin bool
	var ver int32
	if err := pool.QueryRow(ctx, `SELECT is_admin, privilege_version FROM users WHERE id = $1`, bob).Scan(&isAdmin, &ver); err != nil {
		t.Fatal(err)
	}
	if !isAdmin || ver != 2 {
		t.Fatalf("is_admin=%v version=%d, want true 2", isAdmin, ver)
	}
	if err := repo.UpdateFlags(ctx, pool, bob, users.UpdateInput{IsAdmin: true, IsActive: false}, false); err != nil {
		t.Fatal(err)
	}
	if u, _ := repo.Get(ctx, pool, bob); u.IsActive {
		t.Fatal("bob harus nonaktif")
	}
	if err := pool.QueryRow(ctx, `SELECT privilege_version FROM users WHERE id = $1`, bob).Scan(&ver); err != nil || ver != 2 {
		t.Fatalf("bump=false tidak boleh menaikkan versi, got %d", ver)
	}

	if _, err := repo.Get(ctx, pool, 999); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("Get tidak ada: %v", err)
	}
	if err := repo.UpdateFlags(ctx, pool, 999, users.UpdateInput{}, false); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("UpdateFlags tidak ada: %v", err)
	}
	if err := repo.SetPasswordHash(ctx, pool, 999, "x"); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("SetPasswordHash tidak ada: %v", err)
	}
	_ = admin
}

func TestPg_ServiceSideEffects(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", false)

	authSvc := auth.NewService(pool, auth.NewPgRepo())
	mgr := session.NewManager(pool, session.NewPgRepo(), authSvc, wallClock{}, session.DefaultConfig())
	svc := users.NewService(pool, users.NewPgRepo(), authSvc, mgr)

	bobCookie := testutil.LoginSession(t, pool, bob)
	adminCookie := testutil.LoginSession(t, pool, admin)

	// Reset password oleh admin → password lama tidak berlaku, session bob dicabut.
	if err := svc.ResetPassword(ctx, admin, bob, "passwordbaru1"); err != nil {
		t.Fatal(err)
	}
	if _, err := authSvc.Authenticate(ctx, "bob", testutil.TestPassword); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("password lama harus ditolak")
	}
	if _, err := authSvc.Authenticate(ctx, "bob", "passwordbaru1"); err != nil {
		t.Fatalf("password baru harus diterima: %v", err)
	}
	if _, err := mgr.Resolve(ctx, bobCookie.Value); !session.IsInvalid(err) {
		t.Fatal("session bob harus dicabut setelah reset password")
	}

	// Reset password diri sendiri → session admin tetap hidup.
	if err := svc.ResetPassword(ctx, admin, admin, "adminbaru123"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Resolve(ctx, adminCookie.Value); err != nil {
		t.Fatalf("session admin sendiri harus tetap valid: %v", err)
	}

	// Nonaktifkan bob → semua session dicabut (D7).
	bobCookie = testutil.LoginSession(t, pool, bob)
	if err := svc.Update(ctx, admin, bob, users.UpdateInput{IsActive: false}); err != nil {
		t.Fatal(err)
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, bob).Scan(&live); err != nil || live != 0 {
		t.Fatalf("session bob yang masih hidup = %d, want 0", live)
	}

	if err := svc.Update(ctx, admin, admin, users.UpdateInput{IsAdmin: false, IsActive: true}); !errors.Is(err, users.ErrSelfLockout) {
		t.Fatalf("self-lockout: err = %v", err)
	}

	if _, err := svc.Create(ctx, auth.CreateUserInput{Username: "bob", Password: "password123"}); !errors.Is(err, auth.ErrUsernameTaken) {
		t.Fatalf("username duplikat: err = %v", err)
	}
}
