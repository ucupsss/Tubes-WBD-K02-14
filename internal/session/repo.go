package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type Repo interface {
	Create(ctx context.Context, db platformdb.DBTX, s Session) (int64, error)
	// FindByTokenHash mencari session dengan token current, atau token prev yang masih dalam grace (now < prev_valid_until).
	FindByTokenHash(ctx context.Context, db platformdb.DBTX, hash []byte, now time.Time) (Session, error)
	// Rotate memindahkan token current ke prev. Hanya berhasil bila token current masih oldHash,
	// sehingga dua request paralel tidak merotasi dua kali. rotated=false bila sudah dirotasi request lain.
	Rotate(ctx context.Context, db platformdb.DBTX, in RotateInput) (rotated bool, err error)
	Touch(ctx context.Context, db platformdb.DBTX, id int64, now time.Time) error
	Revoke(ctx context.Context, db platformdb.DBTX, id int64, now time.Time) error
	RevokeAllForUser(ctx context.Context, db platformdb.DBTX, userID int64, now time.Time) error
}

type RotateInput struct {
	ID               int64
	OldHash          []byte
	NewHash          []byte
	PrevValidUntil   time.Time
	Now              time.Time
	PrivilegeVersion int32
}

type PgRepo struct{}

func NewPgRepo() *PgRepo {
	return &PgRepo{}
}

func (r *PgRepo) Create(ctx context.Context, db platformdb.DBTX, s Session) (int64, error) {
	const q = `
		INSERT INTO sessions (user_id, token_hash, csrf_token, privilege_version,
		                      created_at, last_seen_at, rotated_at, absolute_expires_at)
		VALUES ($1, $2, $3, $4, $5, $5, $5, $6)
		RETURNING id`

	var id int64
	err := db.QueryRow(ctx, q,
		s.UserID, s.TokenHash, s.CSRFToken, s.PrivilegeVersion, s.CreatedAt, s.AbsoluteExpiresAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("session.Create: %w", err)
	}
	return id, nil
}

func (r *PgRepo) FindByTokenHash(ctx context.Context, db platformdb.DBTX, hash []byte, now time.Time) (Session, error) {
	const q = `
		SELECT id, user_id, token_hash, prev_token_hash, prev_valid_until, csrf_token, privilege_version,
		       created_at, last_seen_at, rotated_at, absolute_expires_at, revoked_at
		FROM sessions
		WHERE token_hash = $1
		   OR (prev_token_hash = $1 AND prev_valid_until > $2)`

	var s Session
	err := db.QueryRow(ctx, q, hash, now).Scan(
		&s.ID, &s.UserID, &s.TokenHash, &s.PrevTokenHash, &s.PrevValidUntil, &s.CSRFToken, &s.PrivilegeVersion,
		&s.CreatedAt, &s.LastSeenAt, &s.RotatedAt, &s.AbsoluteExpiresAt, &s.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalid
	}
	if err != nil {
		return Session{}, fmt.Errorf("session.FindByTokenHash: %w", err)
	}
	return s, nil
}

func (r *PgRepo) Rotate(ctx context.Context, db platformdb.DBTX, in RotateInput) (bool, error) {
	const q = `
		UPDATE sessions
		SET prev_token_hash   = token_hash,
		    prev_valid_until  = $3,
		    token_hash        = $4,
		    rotated_at        = $5,
		    last_seen_at      = $5,
		    privilege_version = $6
		WHERE id = $1 AND token_hash = $2 AND revoked_at IS NULL`

	tag, err := db.Exec(ctx, q, in.ID, in.OldHash, in.PrevValidUntil, in.NewHash, in.Now, in.PrivilegeVersion)
	if err != nil {
		return false, fmt.Errorf("session.Rotate: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *PgRepo) Touch(ctx context.Context, db platformdb.DBTX, id int64, now time.Time) error {
	const q = `
		UPDATE sessions
		SET last_seen_at = $2
		WHERE id = $1 AND last_seen_at < $2`

	if _, err := db.Exec(ctx, q, id, now); err != nil {
		return fmt.Errorf("session.Touch: %w", err)
	}
	return nil
}

func (r *PgRepo) Revoke(ctx context.Context, db platformdb.DBTX, id int64, now time.Time) error {
	const q = `
		UPDATE sessions
		SET revoked_at = $2
		WHERE id = $1 AND revoked_at IS NULL`

	if _, err := db.Exec(ctx, q, id, now); err != nil {
		return fmt.Errorf("session.Revoke: %w", err)
	}
	return nil
}

func (r *PgRepo) RevokeAllForUser(ctx context.Context, db platformdb.DBTX, userID int64, now time.Time) error {
	const q = `
		UPDATE sessions
		SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL`

	if _, err := db.Exec(ctx, q, userID, now); err != nil {
		return fmt.Errorf("session.RevokeAllForUser: %w", err)
	}
	return nil
}
