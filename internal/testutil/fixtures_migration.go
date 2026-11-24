package testutil

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// CreateDraft menyisipkan migration yang belum diajukan dan mengembalikan id-nya.
func CreateDraft(t *testing.T, db platformdb.DBTX, dbID, userID int64, sqlText string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(context.Background(), `
		INSERT INTO migrations (database_id, title, created_by)
		VALUES ($1, 'Draft', $2)
		RETURNING id`, dbID, userID).Scan(&id); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	down := "-- belum diisi"
	hash := migration.HashRevision(sqlText, down, migration.ModeNormal, "", "", 0)
	var revID int64
	if err := db.QueryRow(context.Background(), `
		INSERT INTO migration_revisions (
			migration_id, revision_no, up_sql, down_sql, irreversible, exec_mode, sql_hash, created_by)
		VALUES ($1, 1, $2, $3, false, 'normal', $4, $5)
		RETURNING id`, id, sqlText, down, hash, userID).Scan(&revID); err != nil {
		t.Fatalf("CreateDraft revision: %v", err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE migrations SET current_revision_id = $1 WHERE id = $2`, revID, id); err != nil {
		t.Fatalf("CreateDraft link: %v", err)
	}
	return id
}
