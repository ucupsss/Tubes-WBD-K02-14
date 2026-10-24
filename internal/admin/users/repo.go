package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type Repo interface {
	List(ctx context.Context, db platformdb.DBTX) ([]User, error)
	Get(ctx context.Context, db platformdb.DBTX, id int64) (User, error)
	// UpdateFlags mengubah is_admin/is_active; bump=true sekaligus menaikkan privilege_version (D6).
	UpdateFlags(ctx context.Context, db platformdb.DBTX, id int64, in UpdateInput, bump bool) error
	SetPasswordHash(ctx context.Context, db platformdb.DBTX, id int64, hash string) error
}

type PgRepo struct{}

func NewPgRepo() *PgRepo {
	return &PgRepo{}
}

const userColumns = `id, username, is_admin, is_active, created_at, updated_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (r *PgRepo) List(ctx context.Context, db platformdb.DBTX) ([]User, error) {
	rows, err := db.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("users.List: %w", err)
	}
	defer rows.Close()

	var result []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("users.List scan: %w", err)
		}
		result = append(result, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("users.List rows: %w", err)
	}
	return result, nil
}

func (r *PgRepo) Get(ctx context.Context, db platformdb.DBTX, id int64) (User, error) {
	u, err := scanUser(db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("users.Get: %w", err)
	}
	return u, nil
}

func (r *PgRepo) UpdateFlags(ctx context.Context, db platformdb.DBTX, id int64, in UpdateInput, bump bool) error {
	const q = `
		UPDATE users
		SET is_admin = $2, is_active = $3,
		    privilege_version = privilege_version + CASE WHEN $4 THEN 1 ELSE 0 END,
		    updated_at = now()
		WHERE id = $1`
	tag, err := db.Exec(ctx, q, id, in.IsAdmin, in.IsActive, bump)
	if err != nil {
		return fmt.Errorf("users.UpdateFlags: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PgRepo) SetPasswordHash(ctx context.Context, db platformdb.DBTX, id int64, hash string) error {
	tag, err := db.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	if err != nil {
		return fmt.Errorf("users.SetPasswordHash: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
