package testutil

// Fixture tabel milik A: users, groups, group_members, sessions.

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
)

// TestPassword adalah password semua user yang dibuat CreateUser.
const TestPassword = "password123"

// Hash dibuat sekali per proses test (bcrypt cost 12 ~ ratusan ms).
var testPasswordHash = sync.OnceValues(func() (string, error) {
	return auth.HashPassword(TestPassword)
})

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// CreateUser membuat user aktif dengan password TestPassword.
func CreateUser(t *testing.T, db platformdb.DBTX, username string, isAdmin bool) int64 {
	t.Helper()
	hash, err := testPasswordHash()
	if err != nil {
		t.Fatalf("testutil.CreateUser: hash: %v", err)
	}
	id, err := auth.NewPgRepo().Create(context.Background(), db, auth.User{
		Username: username, PasswordHash: hash, IsAdmin: isAdmin, IsActive: true,
	})
	if err != nil {
		t.Fatalf("testutil.CreateUser(%q): %v", username, err)
	}
	return id
}

// CreateGroup menyisipkan group dan mengembalikan id-nya.
func CreateGroup(t *testing.T, db platformdb.DBTX, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(context.Background(),
		`INSERT INTO groups (name) VALUES ($1) RETURNING id`, name,
	).Scan(&id); err != nil {
		t.Fatalf("testutil.CreateGroup(%q): %v", name, err)
	}
	return id
}

// AddMember menambahkan user ke group. Pemanggilan ulang tidak menggagalkan tes.
func AddMember(t *testing.T, db platformdb.DBTX, groupID, userID int64) {
	t.Helper()
	if _, err := db.Exec(context.Background(),
		`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, groupID, userID,
	); err != nil {
		t.Fatalf("testutil.AddMember(%d, %d): %v", groupID, userID, err)
	}
}

// LoginSession membuat session asli untuk user dan mengembalikan cookie-nya
// (tanpa lewat form login, jadi cepat dan tidak butuh CSRF pre-login).
func LoginSession(t *testing.T, db platformdb.DBTX, userID int64) *http.Cookie {
	t.Helper()
	c, _ := LoginSessionCSRF(t, db, userID)
	return c
}

// LoginSessionCSRF seperti LoginSession, plus token CSRF session untuk request POST
// (field "_csrf" atau header "X-CSRF-Token").
func LoginSessionCSRF(t *testing.T, db platformdb.DBTX, userID int64) (*http.Cookie, string) {
	t.Helper()
	ctx := context.Background()
	users := auth.NewService(db, auth.NewPgRepo())
	u, err := users.LookupUser(ctx, userID)
	if err != nil {
		t.Fatalf("testutil.LoginSession(%d): %v", userID, err)
	}
	mgr := session.NewManager(db, session.NewPgRepo(), users, wallClock{}, session.DefaultConfig())
	token, s, err := mgr.Create(ctx, u)
	if err != nil {
		t.Fatalf("testutil.LoginSession(%d): %v", userID, err)
	}
	return &http.Cookie{Name: session.CookieName, Value: token}, s.CSRFToken
}
