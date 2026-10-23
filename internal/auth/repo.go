package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type Repo interface {
	GetByID(ctx context.Context, db platformdb.DBTX, id int64) (User, error)
	GetByUsername(ctx context.Context, db platformdb.DBTX, username string) (User, error)
	Create(ctx context.Context, db platformdb.DBTX, u User) (int64, error)
	HasAnyUser(ctx context.Context, db platformdb.DBTX) (bool, error)
	// CreateAdminIfNoUsers membuat admin hanya bila tabel users kosong. created=false bila sudah ada user.
	CreateAdminIfNoUsers(ctx context.Context, db platformdb.DBTX, username, passwordHash string) (created bool, err error)
}

type PgRepo struct{}

func NewPgRepo() *PgRepo {
	return &PgRepo{}
}

const userColumns = `id, username, password_hash, is_admin, is_active, privilege_version, created_at, updated_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &u.IsActive,
		&u.PrivilegeVersion, &u.CreatedAt, &u.UpdatedAt,
	)
	return u, err
}

func (r *PgRepo) GetByID(ctx context.Context, db platformdb.DBTX, id int64) (User, error) {
	const q = `
		SELECT ` + userColumns + `
		FROM users
		WHERE id = $1`

	u, err := scanUser(db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth.GetByID: %w", err)
	}
	return u, nil
}

func (r *PgRepo) GetByUsername(ctx context.Context, db platformdb.DBTX, username string) (User, error) {
	const q = `
		SELECT ` + userColumns + `
		FROM users
		WHERE username = $1`

	u, err := scanUser(db.QueryRow(ctx, q, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth.GetByUsername: %w", err)
	}
	return u, nil
}

func (r *PgRepo) Create(ctx context.Context, db platformdb.DBTX, u User) (int64, error) {
	const q = `
		INSERT INTO users (username, password_hash, is_admin, is_active)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	var id int64
	err := db.QueryRow(ctx, q, u.Username, u.PasswordHash, u.IsAdmin, u.IsActive).Scan(&id)
	if isUniqueViolation(err) {
		return 0, ErrUsernameTaken
	}
	if err != nil {
		return 0, fmt.Errorf("auth.Create: %w", err)
	}
	return id, nil
}

func (r *PgRepo) HasAnyUser(ctx context.Context, db platformdb.DBTX) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM users)`

	var exists bool
	if err := db.QueryRow(ctx, q).Scan(&exists); err != nil {
		return false, fmt.Errorf("auth.HasAnyUser: %w", err)
	}
	return exists, nil
}

func (r *PgRepo) CreateAdminIfNoUsers(ctx context.Context, db platformdb.DBTX, username, passwordHash string) (bool, error) {
	// Satu statement: aman bila dua instance server start bersamaan dengan username yang sama.
	const q = `
		INSERT INTO users (username, password_hash, is_admin, is_active)
		SELECT $1, $2, true, true
		WHERE NOT EXISTS (SELECT 1 FROM users)
		ON CONFLICT (username) DO NOTHING`

	tag, err := db.Exec(ctx, q, username, passwordHash)
	if err != nil {
		return false, fmt.Errorf("auth.CreateAdminIfNoUsers: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
