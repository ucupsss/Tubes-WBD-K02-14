package dbconn

import (
	"context"
	"fmt"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// GrantRepo mengelola data hak akses group terhadap database di platform DB.
// Mengimplementasikan interface authz.GrantSource.
type GrantRepo struct {
	db platformdb.DBTX
}

// NewGrantRepo membuat pembaca grant untuk authz.Authorizer.
func NewGrantRepo(db platformdb.DBTX) *GrantRepo {
	return &GrantRepo{db: db}
}

// LevelsForUser mengambil semua level akses user untuk suatu target DB dari seluruh grup yang diikutinya.
func (r *GrantRepo) LevelsForUser(ctx context.Context, userID, dbID int64) ([]authz.Level, error) {
	const q = `
		SELECT gda.level
		FROM group_db_access gda
		JOIN group_members gm ON gm.group_id = gda.group_id
		WHERE gm.user_id = $1 AND gda.database_id = $2`

	rows, err := r.db.Query(ctx, q, userID, dbID)
	if err != nil {
		return nil, fmt.Errorf("GrantRepo.LevelsForUser: %w", err)
	}
	defer rows.Close()

	var levels []authz.Level
	for rows.Next() {
		var lvlStr string
		if err := rows.Scan(&lvlStr); err != nil {
			return nil, fmt.Errorf("GrantRepo.LevelsForUser scan: %w", err)
		}
		levels = append(levels, authz.ParseLevel(lvlStr))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GrantRepo.LevelsForUser rows: %w", err)
	}

	return levels, nil
}

// AccessibleDatabases mengambil daftar seluruh database yang boleh diakses oleh user,
// dengan level tertinggi (max: edit > view) dihitung per database (D1).
func (r *GrantRepo) AccessibleDatabases(ctx context.Context, userID int64) ([]authz.DatabaseAccess, error) {
	const q = `
		SELECT dc.id, dc.name, gda.level
		FROM db_connections dc
		JOIN group_db_access gda ON gda.database_id = dc.id
		JOIN group_members gm ON gm.group_id = gda.group_id
		WHERE gm.user_id = $1
		ORDER BY dc.name ASC`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("GrantRepo.AccessibleDatabases: %w", err)
	}
	defer rows.Close()

	// Peta untuk menghitung max level per DB
	dbMap := make(map[int64]*authz.DatabaseAccess)
	var orderedIDs []int64

	for rows.Next() {
		var dbID int64
		var name, lvlStr string
		if err := rows.Scan(&dbID, &name, &lvlStr); err != nil {
			return nil, fmt.Errorf("GrantRepo.AccessibleDatabases scan: %w", err)
		}

		lvl := authz.ParseLevel(lvlStr)
		if existing, exists := dbMap[dbID]; exists {
			// Rule D1: edit > view
			if lvl > existing.Level {
				existing.Level = lvl
			}
		} else {
			dbMap[dbID] = &authz.DatabaseAccess{
				DatabaseID: dbID,
				Name:       name,
				Level:      lvl,
			}
			orderedIDs = append(orderedIDs, dbID)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GrantRepo.AccessibleDatabases rows: %w", err)
	}

	result := make([]authz.DatabaseAccess, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		result = append(result, *dbMap[id])
	}

	return result, nil
}

// Ensure GrantRepo implements authz.GrantSource
var _ authz.GrantSource = (*GrantRepo)(nil)
