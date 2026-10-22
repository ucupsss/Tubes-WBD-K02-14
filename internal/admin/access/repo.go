package access

import (
	"context"
	"fmt"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type Repo interface {
	GetGroupGrants(ctx context.Context, db platformdb.DBTX, groupID int64) ([]DatabaseGrant, error)
	GetGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64) (string, error)
	SetGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64, level string, grantedBy int64) error
	RevokeGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64) error
}

type PgRepo struct{}

func NewPgRepo() *PgRepo {
	return &PgRepo{}
}

// GetGroupGrants mengambil seluruh target database beserta level akses yang dimiliki group saat ini.
func (r *PgRepo) GetGroupGrants(ctx context.Context, db platformdb.DBTX, groupID int64) ([]DatabaseGrant, error) {
	const q = `
		SELECT dc.id, dc.name, COALESCE(gda.level, 'none') AS level
		FROM db_connections dc
		LEFT JOIN group_db_access gda ON gda.database_id = dc.id AND gda.group_id = $1
		ORDER BY dc.name ASC`

	rows, err := db.Query(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("access.GetGroupGrants: %w", err)
	}
	defer rows.Close()

	var result []DatabaseGrant
	for rows.Next() {
		var g DatabaseGrant
		if err := rows.Scan(&g.DatabaseID, &g.DatabaseName, &g.Level); err != nil {
			return nil, fmt.Errorf("access.GetGroupGrants scan: %w", err)
		}
		result = append(result, g)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("access.GetGroupGrants rows: %w", err)
	}

	return result, nil
}

func (r *PgRepo) GetGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64) (string, error) {
	const q = `SELECT level FROM group_db_access WHERE group_id = $1 AND database_id = $2`
	var level string
	err := db.QueryRow(ctx, q, groupID, dbID).Scan(&level)
	if err != nil {
		return "none", nil
	}
	return level, nil
}

func (r *PgRepo) SetGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64, level string, grantedBy int64) error {
	const q = `
		INSERT INTO group_db_access (group_id, database_id, level, granted_by, granted_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (group_id, database_id)
		DO UPDATE SET level = $3, granted_by = $4, granted_at = now()`

	_, err := db.Exec(ctx, q, groupID, dbID, level, grantedBy)
	if err != nil {
		return fmt.Errorf("access.SetGrant: %w", err)
	}
	return nil
}

func (r *PgRepo) RevokeGrant(ctx context.Context, db platformdb.DBTX, groupID, dbID int64) error {
	const q = `DELETE FROM group_db_access WHERE group_id = $1 AND database_id = $2`
	_, err := db.Exec(ctx, q, groupID, dbID)
	if err != nil {
		return fmt.Errorf("access.RevokeGrant: %w", err)
	}
	return nil
}
