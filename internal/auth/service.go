package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

type Service struct {
	db   platformdb.DBTX
	repo Repo
}

func NewService(db platformdb.DBTX, repo Repo) *Service {
	return &Service{db: db, repo: repo}
}

type CreateUserInput struct {
	Username string
	Password string
	IsAdmin  bool
}

func (in CreateUserInput) Validate() error {
	fields := map[string]string{}
	if !usernamePattern.MatchString(strings.TrimSpace(in.Username)) {
		fields["username"] = "Username 3–32 karakter: huruf, angka, titik, garis bawah, atau strip"
	}
	if msg := ValidatePassword(in.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		return apperr.InvalidFields("Input tidak valid", fields)
	}
	return nil
}

// CreateUser membuat user aktif baru dengan password ter-hash.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (int64, error) {
	if err := in.Validate(); err != nil {
		return 0, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return 0, err
	}
	return s.repo.Create(ctx, s.db, User{
		Username:     strings.TrimSpace(in.Username),
		PasswordHash: hash,
		IsAdmin:      in.IsAdmin,
		IsActive:     true,
	})
}

// Authenticate memverifikasi username & password. Semua kegagalan (user tidak ada, password salah,
// user nonaktif) mengembalikan ErrInvalidCredentials yang sama, dan bcrypt selalu dijalankan.
func (s *Service) Authenticate(ctx context.Context, username, password string) (User, error) {
	u, err := s.repo.GetByUsername(ctx, s.db, strings.TrimSpace(username))
	if errors.Is(err, ErrNotFound) {
		burnPasswordCheck(password)
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if !CheckPassword(u.PasswordHash, password) || !u.IsActive {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

func (s *Service) GetByID(ctx context.Context, id int64) (User, error) {
	return s.repo.GetByID(ctx, s.db, id)
}

// LookupUser mengimplementasi session.Users: memuat ulang status user (aktif, admin,
// privilege_version) di setiap request.
func (s *Service) LookupUser(ctx context.Context, userID int64) (session.User, error) {
	u, err := s.repo.GetByID(ctx, s.db, userID)
	if err != nil {
		return session.User{}, err
	}
	return u.SessionUser(), nil
}

var _ session.Users = (*Service)(nil)

// EnsureBootstrapAdmin membuat admin pertama dari env bila tabel users masih kosong.
// Dipanggil sekali saat server start. created=false bila sudah ada user (env diabaikan).
func (s *Service) EnsureBootstrapAdmin(ctx context.Context, username, password string) (created bool, err error) {
	// Env hanya wajib valid saat tabel masih kosong; server yang sudah punya user boleh mengosongkannya.
	exists, err := s.repo.HasAnyUser(ctx, s.db)
	if err != nil || exists {
		return false, err
	}
	in := CreateUserInput{Username: username, Password: password, IsAdmin: true}
	if err := in.Validate(); err != nil {
		return false, fmt.Errorf("auth.EnsureBootstrapAdmin: BOOTSTRAP_ADMIN_USERNAME/PASSWORD tidak valid: %w", err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	return s.repo.CreateAdminIfNoUsers(ctx, s.db, strings.TrimSpace(username), hash)
}
