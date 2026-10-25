package testutil

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"strconv"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// SecretKey is the APP_SECRET_KEY used by fixtures and NewServer to encrypt connection passwords.
var SecretKey = bytes.Repeat([]byte{0x2a}, 32)

// TestSecretKey is an alias for SecretKey.
var TestSecretKey = SecretKey

// TargetInput returns connection settings for a database on the TEST_DATABASE_URL server.
func TargetInput(t *testing.T, name, targetDBName string) databases.CreateInput {
	t.Helper()
	RequireEnv(t)
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	port := 5432
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			t.Fatalf("TEST_DATABASE_URL port: %v", err)
		}
	}
	pw, _ := u.User.Password()
	return databases.CreateInput{
		Name: name, Host: u.Hostname(), Port: port, DBName: targetDBName,
		Username: u.User.Username(), Password: pw, SSLMode: "disable",
	}
}

// CreateConnection registers targetDBName (from NewTargetDB) as a connection and returns its id.
func CreateConnection(t *testing.T, db platformdb.DBTX, targetDBName string) int64 {
	t.Helper()
	in := TargetInput(t, "conn-"+targetDBName, targetDBName)
	id, err := databases.NewService(db, databases.NewPgRepo(), nil, SecretKey).Create(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateConnection(%q): %v", targetDBName, err)
	}
	return id
}

// Grant gives groupID the level ("view" or "edit") on dbID.
func Grant(t *testing.T, db platformdb.DBTX, groupID, dbID int64, level string) {
	t.Helper()
	const q = `
		INSERT INTO group_db_access (group_id, database_id, level) VALUES ($1, $2, $3)
		ON CONFLICT (group_id, database_id) DO UPDATE SET level = EXCLUDED.level`
	if _, err := db.Exec(context.Background(), q, groupID, dbID, level); err != nil {
		t.Fatalf("Grant(%d, %d, %s): %v", groupID, dbID, level, err)
	}
}
