//go:build integration

package databases_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestPg_ConnectionCRUD(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	admin := testutil.CreateUser(t, pool, "admin", true)
	targetName, _ := testutil.NewTargetDB(t, "")
	mgr := dbconn.NewManager(nil, testutil.SecretKey)
	svc := databases.NewService(pool, databases.NewPgRepo(), mgr, testutil.SecretKey)

	in := testutil.TargetInput(t, "utama", targetName)
	in.CreatedBy = admin
	id, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	// Password disimpan terenkripsi, bukan plaintext.
	var enc []byte
	if err := pool.QueryRow(ctx, `SELECT password_enc FROM db_connections WHERE id = $1`, id).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte(in.Password)) {
		t.Fatal("password koneksi tidak boleh disimpan plaintext")
	}
	if plain, err := dbconn.Decrypt(enc, testutil.SecretKey); err != nil || string(plain) != in.Password {
		t.Fatalf("password harus bisa didekripsi dengan APP_SECRET_KEY: %v", err)
	}

	if _, err := svc.Create(ctx, in); !errors.Is(err, databases.ErrDuplicateName) {
		t.Fatalf("nama duplikat: err = %v", err)
	}
	bad := in
	bad.Name, bad.Port = "", 0
	if _, err := svc.Create(ctx, bad); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("input tidak valid: err = %v", err)
	}

	// Update tanpa password mempertahankan password lama.
	upd := databases.UpdateInput{Name: "utama-baru", Host: in.Host, Port: in.Port, DBName: in.DBName, Username: in.Username, SSLMode: "disable"}
	if err := svc.Update(ctx, id, upd); err != nil {
		t.Fatal(err)
	}
	c, err := svc.GetByID(ctx, id)
	if err != nil || c.Name != "utama-baru" {
		t.Fatalf("GetByID = %+v, %v", c, err)
	}
	if plain, _ := dbconn.Decrypt(c.PasswordEnc, testutil.SecretKey); string(plain) != in.Password {
		t.Fatal("update tanpa password tidak boleh mengubah password")
	}
	if err := svc.TestConnection(ctx, id, upd); err != nil {
		t.Fatalf("uji koneksi memakai password tersimpan: %v", err)
	}

	if err := svc.Update(ctx, 999, upd); !errors.Is(err, databases.ErrNotFound) {
		t.Fatalf("update tidak ada: err = %v", err)
	}

	// Koneksi yang sudah punya riwayat migration tidak boleh dihapus.
	testutil.CreateDraft(t, pool, id, admin, "SELECT 1")
	if err := svc.Delete(ctx, id); !errors.Is(err, databases.ErrHasMigrations) {
		t.Fatalf("hapus koneksi dengan migration: err = %v", err)
	}

	id2, err := svc.Create(ctx, testutil.TargetInput(t, "kedua", targetName))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, id2); !errors.Is(err, databases.ErrNotFound) {
		t.Fatalf("hapus dua kali: err = %v", err)
	}

	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
}
