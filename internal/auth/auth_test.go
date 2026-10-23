package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

var errDB = errors.New("platform db down")

// stubRepo adalah Repo in-memory untuk unit test service.
type stubRepo struct {
	users  map[string]auth.User
	nextID int64
	err    error
}

func newStubRepo(users ...auth.User) *stubRepo {
	r := &stubRepo{users: map[string]auth.User{}, nextID: 1}
	for _, u := range users {
		if u.ID == 0 {
			u.ID = r.nextID
		}
		r.nextID = u.ID + 1
		r.users[u.Username] = u
	}
	return r
}

func (r *stubRepo) GetByID(ctx context.Context, db platformdb.DBTX, id int64) (auth.User, error) {
	if r.err != nil {
		return auth.User{}, r.err
	}
	for _, u := range r.users {
		if u.ID == id {
			return u, nil
		}
	}
	return auth.User{}, auth.ErrNotFound
}

func (r *stubRepo) GetByUsername(ctx context.Context, db platformdb.DBTX, username string) (auth.User, error) {
	if r.err != nil {
		return auth.User{}, r.err
	}
	u, ok := r.users[username]
	if !ok {
		return auth.User{}, auth.ErrNotFound
	}
	return u, nil
}

func (r *stubRepo) Create(ctx context.Context, db platformdb.DBTX, u auth.User) (int64, error) {
	if r.err != nil {
		return 0, r.err
	}
	if _, ok := r.users[u.Username]; ok {
		return 0, auth.ErrUsernameTaken
	}
	u.ID = r.nextID
	r.nextID++
	r.users[u.Username] = u
	return u.ID, nil
}

func (r *stubRepo) HasAnyUser(ctx context.Context, db platformdb.DBTX) (bool, error) {
	return len(r.users) > 0, r.err
}

func (r *stubRepo) CreateAdminIfNoUsers(ctx context.Context, db platformdb.DBTX, username, passwordHash string) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	if len(r.users) > 0 {
		return false, nil
	}
	_, err := r.Create(ctx, db, auth.User{Username: username, PasswordHash: passwordHash, IsAdmin: true, IsActive: true})
	return err == nil, err
}

// aliceHash di-cache karena bcrypt cost 12 lambat (~250 ms).
var aliceHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword("password123")
	if err != nil {
		panic(err)
	}
	return h
})

func alice(active bool) auth.User {
	return auth.User{ID: 10, Username: "alice", PasswordHash: aliceHash(), IsActive: active, PrivilegeVersion: 1}
}

func TestService_Authenticate(t *testing.T) {
	tests := []struct {
		name     string
		repo     *stubRepo
		username string
		password string
		wantErr  error
	}{
		{"success", newStubRepo(alice(true)), "alice", "password123", nil},
		{"success with surrounding spaces in username", newStubRepo(alice(true)), "  alice ", "password123", nil},
		{"wrong password", newStubRepo(alice(true)), "alice", "salah12345", auth.ErrInvalidCredentials},
		{"unknown user", newStubRepo(alice(true)), "mallory", "password123", auth.ErrInvalidCredentials},
		{"inactive user", newStubRepo(alice(false)), "alice", "password123", auth.ErrInvalidCredentials},
		{"empty password", newStubRepo(alice(true)), "alice", "", auth.ErrInvalidCredentials},
		{"db error", &stubRepo{err: errDB}, "alice", "password123", errDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := auth.NewService(nil, tt.repo).Authenticate(context.Background(), tt.username, tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && u.ID != 10 {
				t.Fatalf("user ID = %d, want 10", u.ID)
			}
			if tt.wantErr != nil && u.ID != 0 {
				t.Fatalf("user harus kosong saat gagal, got ID %d", u.ID)
			}
		})
	}
}

func TestService_Authenticate_SameMessageForAllFailures(t *testing.T) {
	svc := auth.NewService(nil, newStubRepo(alice(true), auth.User{ID: 11, Username: "bob", PasswordHash: aliceHash()}))
	ctx := context.Background()

	_, errWrongPw := svc.Authenticate(ctx, "alice", "salah12345")
	_, errUnknown := svc.Authenticate(ctx, "mallory", "password123")
	_, errInactive := svc.Authenticate(ctx, "bob", "password123")

	if errWrongPw.Error() != errUnknown.Error() || errUnknown.Error() != errInactive.Error() {
		t.Fatalf("pesan error harus sama: %q / %q / %q", errWrongPw, errUnknown, errInactive)
	}
}

func TestService_CreateUser(t *testing.T) {
	repo := newStubRepo()
	svc := auth.NewService(nil, repo)

	id, err := svc.CreateUser(context.Background(), auth.CreateUserInput{Username: " carol ", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	u, ok := repo.users["carol"]
	if !ok {
		t.Fatal("username harus di-trim lalu disimpan sebagai 'carol'")
	}
	if u.ID != id || !u.IsActive || u.IsAdmin {
		t.Fatalf("user tersimpan salah: %+v", u)
	}
	if u.PasswordHash == "password123" || !auth.CheckPassword(u.PasswordHash, "password123") {
		t.Fatal("password harus disimpan sebagai hash bcrypt")
	}
}

func TestService_CreateUser_Duplicate(t *testing.T) {
	svc := auth.NewService(nil, newStubRepo(alice(true)))
	_, err := svc.CreateUser(context.Background(), auth.CreateUserInput{Username: "alice", Password: "password123"})
	if !errors.Is(err, auth.ErrUsernameTaken) {
		t.Fatalf("err = %v, want ErrUsernameTaken", err)
	}
}

func TestCreateUserInput_Validate(t *testing.T) {
	tests := []struct {
		name       string
		in         auth.CreateUserInput
		wantFields []string
	}{
		{"valid", auth.CreateUserInput{Username: "bob.dev_1", Password: "password123"}, nil},
		{"empty", auth.CreateUserInput{}, []string{"username", "password"}},
		{"username too short", auth.CreateUserInput{Username: "ab", Password: "password123"}, []string{"username"}},
		{"username with space", auth.CreateUserInput{Username: "bob dev", Password: "password123"}, []string{"username"}},
		{"username with sql", auth.CreateUserInput{Username: "x';DROP--", Password: "password123"}, []string{"username"}},
		{"password too short", auth.CreateUserInput{Username: "bob", Password: "short"}, []string{"password"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Validate()
			if tt.wantFields == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			fields, ok := apperr.FieldErrors(err)
			if !ok {
				t.Fatalf("err = %v, want field errors", err)
			}
			if len(fields) != len(tt.wantFields) {
				t.Fatalf("fields = %v, want keys %v", fields, tt.wantFields)
			}
			for _, f := range tt.wantFields {
				if _, ok := fields[f]; !ok {
					t.Fatalf("field %q tidak ada di %v", f, fields)
				}
			}
		})
	}
}

func TestService_EnsureBootstrapAdmin(t *testing.T) {
	t.Run("empty table creates admin", func(t *testing.T) {
		repo := newStubRepo()
		created, err := auth.NewService(nil, repo).EnsureBootstrapAdmin(context.Background(), "admin", "adminpass123")
		if err != nil || !created {
			t.Fatalf("got (%v, %v), want (true, nil)", created, err)
		}
		u := repo.users["admin"]
		if !u.IsAdmin || !u.IsActive || !auth.CheckPassword(u.PasswordHash, "adminpass123") {
			t.Fatalf("admin tersimpan salah: %+v", u)
		}
	})

	t.Run("existing users skips even with empty env", func(t *testing.T) {
		repo := newStubRepo(alice(true))
		created, err := auth.NewService(nil, repo).EnsureBootstrapAdmin(context.Background(), "", "")
		if err != nil || created {
			t.Fatalf("got (%v, %v), want (false, nil)", created, err)
		}
		if len(repo.users) != 1 {
			t.Fatalf("tidak boleh ada user baru, got %d user", len(repo.users))
		}
	})

	t.Run("empty table with empty password fails", func(t *testing.T) {
		repo := newStubRepo()
		created, err := auth.NewService(nil, repo).EnsureBootstrapAdmin(context.Background(), "admin", "")
		if err == nil || created {
			t.Fatalf("got (%v, %v), want error", created, err)
		}
		if len(repo.users) != 0 {
			t.Fatal("admin tidak boleh dibuat dengan password kosong")
		}
	})

	t.Run("db error", func(t *testing.T) {
		_, err := auth.NewService(nil, &stubRepo{err: errDB}).EnsureBootstrapAdmin(context.Background(), "admin", "adminpass123")
		if !errors.Is(err, errDB) {
			t.Fatalf("err = %v, want %v", err, errDB)
		}
	})
}

func TestUser_Principal(t *testing.T) {
	p := auth.User{ID: 3, Username: "bob", IsAdmin: true, PasswordHash: "x"}.Principal()
	if p.UserID != 3 || p.Username != "bob" || !p.IsAdmin {
		t.Fatalf("principal salah: %+v", p)
	}
}

var _ auth.Repo = (*auth.PgRepo)(nil)
