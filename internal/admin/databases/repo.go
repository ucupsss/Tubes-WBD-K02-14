package databases

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type Repo interface {
	List(ctx context.Context, db platformdb.DBTX) ([]Connection, error)
	GetByID(ctx context.Context, db platformdb.DBTX, id int64) (Connection, error)
	Create(ctx context.Context, db platformdb.DBTX, conn Connection) (int64, error)
	Update(ctx context.Context, db platformdb.DBTX, conn Connection) error
	Delete(ctx context.Context, db platformdb.DBTX, id int64) error
	HasMigrations(ctx context.Context, db platformdb.DBTX, id int64) (bool, error)
}

type PgRepo struct{}

func NewPgRepo() *PgRepo {
	return &PgRepo{}
}

func (r *PgRepo) List(ctx context.Context, db platformdb.DBTX) ([]Connection, error) {
	const q = `
		SELECT id, name, host, port, db_name, username, password_enc, sslmode, created_by, created_at, updated_at
		FROM db_connections
		ORDER BY id ASC`

	rows, err := db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("databases.List: %w", err)
	}
	defer rows.Close()

	var result []Connection
	for rows.Next() {
		var c Connection
		err := rows.Scan(
			&c.ID, &c.Name, &c.Host, &c.Port, &c.DBName, &c.Username,
			&c.PasswordEnc, &c.SSLMode, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("databases.List scan: %w", err)
		}
		result = append(result, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("databases.List rows: %w", err)
	}

	return result, nil
}

func (r *PgRepo) GetByID(ctx context.Context, db platformdb.DBTX, id int64) (Connection, error) {
	const q = `
		SELECT id, name, host, port, db_name, username, password_enc, sslmode, created_by, created_at, updated_at
		FROM db_connections
		WHERE id = $1`

	var c Connection
	err := db.QueryRow(ctx, q, id).Scan(
		&c.ID, &c.Name, &c.Host, &c.Port, &c.DBName, &c.Username,
		&c.PasswordEnc, &c.SSLMode, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, fmt.Errorf("databases.GetByID: %w", err)
	}
	return c, nil
}

func (r *PgRepo) Create(ctx context.Context, db platformdb.DBTX, conn Connection) (int64, error) {
	const q = `
		INSERT INTO db_connections (name, host, port, db_name, username, password_enc, sslmode, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`

	var id int64
	err := db.QueryRow(ctx, q,
		conn.Name, conn.Host, conn.Port, conn.DBName, conn.Username,
		conn.PasswordEnc, conn.SSLMode, conn.CreatedBy,
	).Scan(&id)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, ErrDuplicateName
		}
		return 0, fmt.Errorf("databases.Create: %w", err)
	}
	return id, nil
}

func (r *PgRepo) Update(ctx context.Context, db platformdb.DBTX, conn Connection) error {
	const q = `
		UPDATE db_connections
		SET name = $1, host = $2, port = $3, db_name = $4, username = $5,
		    password_enc = $6, sslmode = $7, updated_at = now()
		WHERE id = $8`

	tag, err := db.Exec(ctx, q,
		conn.Name, conn.Host, conn.Port, conn.DBName, conn.Username,
		conn.PasswordEnc, conn.SSLMode, conn.ID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicateName
		}
		return fmt.Errorf("databases.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PgRepo) Delete(ctx context.Context, db platformdb.DBTX, id int64) error {
	const q = `DELETE FROM db_connections WHERE id = $1`
	tag, err := db.Exec(ctx, q, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign key violation
			return ErrHasMigrations
		}
		return fmt.Errorf("databases.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PgRepo) HasMigrations(ctx context.Context, db platformdb.DBTX, id int64) (bool, error) {
	// Cek apakah tabel migrations ada dan memiliki record untuk database_id ini
	const q = `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_name = 'migrations'
		) AND EXISTS (
			SELECT 1 FROM migrations WHERE database_id = $1
		)`

	var exists bool
	err := db.QueryRow(ctx, q, id).Scan(&exists)
	if err != nil {
		// Jika tabel migrations belum dibuat, anggap false
		return false, nil
	}
	return exists, nil
}
