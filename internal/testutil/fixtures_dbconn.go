package testutil

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// TestSecretKey adalah kunci enkripsi 32-byte untuk testing.
var TestSecretKey = []byte("12345678901234567890123456789012")

// CreateConnection membuat koneksi database target baru di platform DB dan mengembalikan ID-nya.
func CreateConnection(t *testing.T, db platformdb.DBTX, targetDBName string) int64 {
	t.Helper()
	encPass, err := dbconn.Encrypt([]byte("postgres"), TestSecretKey)
	if err != nil {
		t.Fatalf("testutil.CreateConnection: encrypt: %v", err)
	}

	const q = `
		INSERT INTO db_connections (name, host, port, db_name, username, password_enc, sslmode)
		VALUES ($1, 'localhost', 5432, $1, 'postgres', $2, 'disable')
		RETURNING id`

	var id int64
	if err := db.QueryRow(context.Background(), q, targetDBName, encPass).Scan(&id); err != nil {
		t.Fatalf("testutil.CreateConnection(%q): %v", targetDBName, err)
	}
	return id
}

// Grant memberikan akses group ke target database dengan level tertentu ('view' atau 'edit').
func Grant(t *testing.T, db platformdb.DBTX, groupID, dbID int64, level string) {
	t.Helper()
	const q = `
		INSERT INTO group_db_access (group_id, database_id, level, granted_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (group_id, database_id)
		DO UPDATE SET level = $3, granted_at = now()`

	if _, err := db.Exec(context.Background(), q, groupID, dbID, level); err != nil {
		t.Fatalf("testutil.Grant(group=%d, db=%d, level=%q): %v", groupID, dbID, level, err)
	}
}
