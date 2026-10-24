package testutil

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// CreateDraft inserts a draft migration and returns its id.
func CreateDraft(t *testing.T, db platformdb.DBTX, dbID, userID int64, sqlText string) int64 {
	t.Helper()
	const q = `
		INSERT INTO migrations (database_id, title, sql_text, status, created_by)
		VALUES ($1, 'Draft', $2, 'draft', $3)
		RETURNING id`
	var id int64
	if err := db.QueryRow(context.Background(), q, dbID, sqlText, userID).Scan(&id); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	return id
}
