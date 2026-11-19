package groups

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type Repo interface {
	List(ctx context.Context, db platformdb.DBTX) ([]Group, error)
	Get(ctx context.Context, db platformdb.DBTX, id int64) (Group, error)
	Create(ctx context.Context, db platformdb.DBTX, in Input) (int64, error)
	Update(ctx context.Context, db platformdb.DBTX, id int64, in Input) error
	Delete(ctx context.Context, db platformdb.DBTX, id int64) error
	ListMembers(ctx context.Context, db platformdb.DBTX, groupID int64) ([]Member, error)
	ListCandidates(ctx context.Context, db platformdb.DBTX, groupID int64) ([]UserOption, error)
	// AddMember mengembalikan added=false bila user sudah menjadi anggota.
	AddMember(ctx context.Context, db platformdb.DBTX, groupID, userID int64) (added bool, err error)
	RemoveMember(ctx context.Context, db platformdb.DBTX, groupID, userID int64) error
	// IsMember melaporkan apakah user anggota group.
	IsMember(ctx context.Context, db platformdb.DBTX, groupID, userID int64) (bool, error)
	// SetLeader menetapkan leader group; userID nil menghapus leader.
	SetLeader(ctx context.Context, db platformdb.DBTX, groupID int64, userID *int64) error
	// LedGroups mengembalikan group yang dipimpin user (kosong bila bukan leader).
	LedGroups(ctx context.Context, db platformdb.DBTX, userID int64) ([]Ref, error)
}

type PgRepo struct{}

func NewPgRepo() *PgRepo {
	return &PgRepo{}
}

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

const groupSelect = `
	SELECT g.id, g.name, g.description, g.created_at,
	       (SELECT count(*) FROM group_members gm WHERE gm.group_id = g.id),
	       g.leader_id,
	       COALESCE((SELECT u.username FROM users u WHERE u.id = g.leader_id), '')
	FROM groups g`

func scanGroup(row pgx.Row) (Group, error) {
	var g Group
	err := row.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.MemberCount, &g.LeaderID, &g.LeaderName)
	return g, err
}

func (r *PgRepo) List(ctx context.Context, db platformdb.DBTX) ([]Group, error) {
	rows, err := db.Query(ctx, groupSelect+` ORDER BY g.name`)
	if err != nil {
		return nil, fmt.Errorf("groups.List: %w", err)
	}
	defer rows.Close()

	var result []Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("groups.List scan: %w", err)
		}
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("groups.List rows: %w", err)
	}
	return result, nil
}

func (r *PgRepo) Get(ctx context.Context, db platformdb.DBTX, id int64) (Group, error) {
	g, err := scanGroup(db.QueryRow(ctx, groupSelect+` WHERE g.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, fmt.Errorf("groups.Get: %w", err)
	}
	return g, nil
}

func (r *PgRepo) Create(ctx context.Context, db platformdb.DBTX, in Input) (int64, error) {
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO groups (name, description) VALUES ($1, $2) RETURNING id`,
		in.Name, in.Description,
	).Scan(&id)
	if pgCode(err) == pgUniqueViolation {
		return 0, ErrNameTaken
	}
	if err != nil {
		return 0, fmt.Errorf("groups.Create: %w", err)
	}
	return id, nil
}

func (r *PgRepo) Update(ctx context.Context, db platformdb.DBTX, id int64, in Input) error {
	tag, err := db.Exec(ctx, `UPDATE groups SET name = $2, description = $3 WHERE id = $1`, id, in.Name, in.Description)
	if pgCode(err) == pgUniqueViolation {
		return ErrNameTaken
	}
	if err != nil {
		return fmt.Errorf("groups.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete menghapus group; keanggotaan ikut terhapus (ON DELETE CASCADE).
func (r *PgRepo) Delete(ctx context.Context, db platformdb.DBTX, id int64) error {
	tag, err := db.Exec(ctx, `DELETE FROM groups WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("groups.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PgRepo) ListMembers(ctx context.Context, db platformdb.DBTX, groupID int64) ([]Member, error) {
	const q = `
		SELECT u.id, u.username, u.is_active, (g.leader_id = u.id)
		FROM group_members gm
		JOIN users u ON u.id = gm.user_id
		JOIN groups g ON g.id = gm.group_id
		WHERE gm.group_id = $1
		ORDER BY u.username`
	rows, err := db.Query(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("groups.ListMembers: %w", err)
	}
	defer rows.Close()

	var result []Member
	for rows.Next() {
		var m Member
		var leader *bool
		if err := rows.Scan(&m.UserID, &m.Username, &m.IsActive, &leader); err != nil {
			return nil, fmt.Errorf("groups.ListMembers scan: %w", err)
		}
		m.IsLeader = leader != nil && *leader
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("groups.ListMembers rows: %w", err)
	}
	return result, nil
}

func (r *PgRepo) ListCandidates(ctx context.Context, db platformdb.DBTX, groupID int64) ([]UserOption, error) {
	const q = `
		SELECT u.id, u.username
		FROM users u
		WHERE u.is_active
		  AND NOT EXISTS (SELECT 1 FROM group_members gm WHERE gm.group_id = $1 AND gm.user_id = u.id)
		ORDER BY u.username`
	rows, err := db.Query(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("groups.ListCandidates: %w", err)
	}
	defer rows.Close()

	var result []UserOption
	for rows.Next() {
		var o UserOption
		if err := rows.Scan(&o.ID, &o.Username); err != nil {
			return nil, fmt.Errorf("groups.ListCandidates scan: %w", err)
		}
		result = append(result, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("groups.ListCandidates rows: %w", err)
	}
	return result, nil
}

func (r *PgRepo) AddMember(ctx context.Context, db platformdb.DBTX, groupID, userID int64) (bool, error) {
	tag, err := db.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		groupID, userID,
	)
	if pgCode(err) == pgForeignKeyViolation {
		// Group sudah dicek service sebelum insert, jadi FK yang gagal adalah user.
		return false, ErrUserNotFound
	}
	if err != nil {
		return false, fmt.Errorf("groups.AddMember: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *PgRepo) RemoveMember(ctx context.Context, db platformdb.DBTX, groupID, userID int64) error {
	if _, err := db.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID); err != nil {
		return fmt.Errorf("groups.RemoveMember: %w", err)
	}
	return nil
}

func (r *PgRepo) IsMember(ctx context.Context, db platformdb.DBTX, groupID, userID int64) (bool, error) {
	var ok bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM group_members gm JOIN users u ON u.id = gm.user_id
		                WHERE gm.group_id = $1 AND gm.user_id = $2 AND u.is_active)`,
		groupID, userID,
	).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("groups.IsMember: %w", err)
	}
	return ok, nil
}

func (r *PgRepo) SetLeader(ctx context.Context, db platformdb.DBTX, groupID int64, userID *int64) error {
	tag, err := db.Exec(ctx, `UPDATE groups SET leader_id = $2 WHERE id = $1`, groupID, userID)
	if err != nil {
		return fmt.Errorf("groups.SetLeader: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PgRepo) LedGroups(ctx context.Context, db platformdb.DBTX, userID int64) ([]Ref, error) {
	rows, err := db.Query(ctx, `SELECT id, name FROM groups WHERE leader_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, fmt.Errorf("groups.LedGroups: %w", err)
	}
	defer rows.Close()
	var refs []Ref
	for rows.Next() {
		var g Ref
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, fmt.Errorf("groups.LedGroups scan: %w", err)
		}
		refs = append(refs, g)
	}
	return refs, rows.Err()
}
