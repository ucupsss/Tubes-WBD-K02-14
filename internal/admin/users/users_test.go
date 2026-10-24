package users_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

var errDB = errors.New("platform db down")

const (
	adminID = int64(1)
	bobID   = int64(2)
)

type flagsCall struct {
	id   int64
	in   users.UpdateInput
	bump bool
}

type stubRepo struct {
	users     map[int64]users.User
	flags     []flagsCall
	passwords map[int64]string
	err       error
}

func newStubRepo(list ...users.User) *stubRepo {
	r := &stubRepo{users: map[int64]users.User{}, passwords: map[int64]string{}}
	for _, u := range list {
		r.users[u.ID] = u
	}
	return r
}

func (r *stubRepo) List(ctx context.Context, db platformdb.DBTX) ([]users.User, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []users.User
	for _, u := range r.users {
		out = append(out, u)
	}
	return out, nil
}

func (r *stubRepo) Get(ctx context.Context, db platformdb.DBTX, id int64) (users.User, error) {
	if r.err != nil {
		return users.User{}, r.err
	}
	u, ok := r.users[id]
	if !ok {
		return users.User{}, users.ErrNotFound
	}
	return u, nil
}

func (r *stubRepo) UpdateFlags(ctx context.Context, db platformdb.DBTX, id int64, in users.UpdateInput, bump bool) error {
	if r.err != nil {
		return r.err
	}
	u, ok := r.users[id]
	if !ok {
		return users.ErrNotFound
	}
	r.flags = append(r.flags, flagsCall{id, in, bump})
	u.IsAdmin, u.IsActive = in.IsAdmin, in.IsActive
	r.users[id] = u
	return nil
}

func (r *stubRepo) SetPasswordHash(ctx context.Context, db platformdb.DBTX, id int64, hash string) error {
	if r.err != nil {
		return r.err
	}
	if _, ok := r.users[id]; !ok {
		return users.ErrNotFound
	}
	r.passwords[id] = hash
	return nil
}

type stubAccounts struct {
	created []auth.CreateUserInput
	err     error
}

func (a *stubAccounts) CreateUser(ctx context.Context, in auth.CreateUserInput) (int64, error) {
	if a.err != nil {
		return 0, a.err
	}
	if err := in.Validate(); err != nil {
		return 0, err
	}
	a.created = append(a.created, in)
	return 100, nil
}

type stubRevoker struct {
	revoked []int64
	err     error
}

func (s *stubRevoker) RevokeAllForUser(ctx context.Context, userID int64) error {
	if s.err != nil {
		return s.err
	}
	s.revoked = append(s.revoked, userID)
	return nil
}

func fixtures() []users.User {
	return []users.User{
		{ID: adminID, Username: "admin", IsAdmin: true, IsActive: true},
		{ID: bobID, Username: "bob", IsAdmin: false, IsActive: true},
	}
}

func newService(repo *stubRepo) (*users.Service, *stubAccounts, *stubRevoker) {
	acc, rev := &stubAccounts{}, &stubRevoker{}
	return users.NewService(nil, repo, acc, rev), acc, rev
}

func TestService_Update_SideEffects(t *testing.T) {
	tests := []struct {
		name       string
		start      users.User
		in         users.UpdateInput
		wantBump   bool
		wantRevoke bool
	}{
		{"jadi admin → bump (D6)", users.User{ID: bobID, IsActive: true},
			users.UpdateInput{IsAdmin: true, IsActive: true}, true, false},
		{"admin dicabut → revoke (D7)", users.User{ID: bobID, IsAdmin: true, IsActive: true},
			users.UpdateInput{IsAdmin: false, IsActive: true}, false, true},
		{"dinonaktifkan → revoke (D7)", users.User{ID: bobID, IsActive: true},
			users.UpdateInput{IsAdmin: false, IsActive: false}, false, true},
		{"admin dinonaktifkan → revoke", users.User{ID: bobID, IsAdmin: true, IsActive: true},
			users.UpdateInput{IsAdmin: true, IsActive: false}, false, true},
		{"diaktifkan lagi → tanpa efek", users.User{ID: bobID, IsActive: false},
			users.UpdateInput{IsAdmin: false, IsActive: true}, false, false},
		{"tidak berubah → tanpa efek", users.User{ID: bobID, IsActive: true},
			users.UpdateInput{IsAdmin: false, IsActive: true}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newStubRepo(tt.start)
			svc, _, rev := newService(repo)

			if err := svc.Update(context.Background(), adminID, bobID, tt.in); err != nil {
				t.Fatal(err)
			}
			if len(repo.flags) != 1 || repo.flags[0].in != tt.in || repo.flags[0].bump != tt.wantBump {
				t.Fatalf("UpdateFlags = %+v, want in=%+v bump=%v", repo.flags, tt.in, tt.wantBump)
			}
			if gotRevoke := len(rev.revoked) == 1 && rev.revoked[0] == bobID; gotRevoke != tt.wantRevoke {
				t.Fatalf("revoked = %v, want revoke=%v", rev.revoked, tt.wantRevoke)
			}
		})
	}
}

func TestService_Update_SelfLockout(t *testing.T) {
	for _, in := range []users.UpdateInput{
		{IsAdmin: false, IsActive: true},
		{IsAdmin: true, IsActive: false},
		{IsAdmin: false, IsActive: false},
	} {
		repo := newStubRepo(fixtures()...)
		svc, _, rev := newService(repo)
		err := svc.Update(context.Background(), adminID, adminID, in)
		if !errors.Is(err, users.ErrSelfLockout) {
			t.Fatalf("%+v: err = %v, want ErrSelfLockout", in, err)
		}
		if len(repo.flags) != 0 || len(rev.revoked) != 0 {
			t.Fatal("tidak boleh ada perubahan")
		}
	}

	// Menyimpan diri sendiri tanpa mengubah apa pun tetap boleh.
	repo := newStubRepo(fixtures()...)
	svc, _, _ := newService(repo)
	if err := svc.Update(context.Background(), adminID, adminID, users.UpdateInput{IsAdmin: true, IsActive: true}); err != nil {
		t.Fatal(err)
	}
}

func TestService_Update_Errors(t *testing.T) {
	svc, _, _ := newService(newStubRepo(fixtures()...))
	if err := svc.Update(context.Background(), adminID, 999, users.UpdateInput{IsActive: true}); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("user tidak ada: err = %v", err)
	}

	repo := newStubRepo(fixtures()...)
	repo.err = errDB
	svc, _, _ = newService(repo)
	if err := svc.Update(context.Background(), adminID, bobID, users.UpdateInput{IsActive: true}); !errors.Is(err, errDB) {
		t.Fatalf("db error: err = %v", err)
	}

	repo = newStubRepo(fixtures()...)
	svc, _, rev := newService(repo)
	rev.err = errDB
	if err := svc.Update(context.Background(), adminID, bobID, users.UpdateInput{IsActive: false}); !errors.Is(err, errDB) {
		t.Fatalf("revoke gagal harus dilaporkan, err = %v", err)
	}
}

func TestService_ResetPassword(t *testing.T) {
	repo := newStubRepo(fixtures()...)
	svc, _, rev := newService(repo)

	if err := svc.ResetPassword(context.Background(), adminID, bobID, "passwordbaru1"); err != nil {
		t.Fatal(err)
	}
	hash := repo.passwords[bobID]
	if hash == "" || hash == "passwordbaru1" || !auth.CheckPassword(hash, "passwordbaru1") {
		t.Fatal("password harus disimpan sebagai bcrypt hash")
	}
	if len(rev.revoked) != 1 || rev.revoked[0] != bobID {
		t.Fatalf("session user harus dicabut, got %v", rev.revoked)
	}
}

func TestService_ResetPassword_Self(t *testing.T) {
	repo := newStubRepo(fixtures()...)
	svc, _, rev := newService(repo)
	if err := svc.ResetPassword(context.Background(), adminID, adminID, "passwordbaru1"); err != nil {
		t.Fatal(err)
	}
	if len(rev.revoked) != 0 {
		t.Fatal("reset password diri sendiri tidak boleh mengakhiri session yang sedang dipakai")
	}
}

func TestService_ResetPassword_Invalid(t *testing.T) {
	repo := newStubRepo(fixtures()...)
	svc, _, rev := newService(repo)

	err := svc.ResetPassword(context.Background(), adminID, bobID, "pendek")
	fe, ok := apperr.FieldErrors(err)
	if !ok || fe["password"] == "" {
		t.Fatalf("err = %v, want field error password", err)
	}
	if len(repo.passwords) != 0 || len(rev.revoked) != 0 {
		t.Fatal("password invalid tidak boleh mengubah apa pun")
	}

	if err := svc.ResetPassword(context.Background(), adminID, 999, "passwordbaru1"); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("user tidak ada: err = %v", err)
	}
	if len(rev.revoked) != 0 {
		t.Fatal("user tidak ada: tidak boleh revoke")
	}
}

func TestService_Create(t *testing.T) {
	svc, acc, _ := newService(newStubRepo())
	id, err := svc.Create(context.Background(), auth.CreateUserInput{Username: "carol", Password: "password123"})
	if err != nil || id != 100 || len(acc.created) != 1 {
		t.Fatalf("id=%d err=%v created=%v", id, err, acc.created)
	}
}
