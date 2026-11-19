//go:build integration

package auth_test

// Test alur lengkap lewat middleware asli: session.Load → csrf.Verify → mux (authz.Protect → handler API JSON).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/api"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/csrf"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

const origin = "http://example.com" // host default httptest.NewRequest

func newApp(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	onErr := func(w http.ResponseWriter, r *http.Request, err error) { httpx.HandleError(w, r, nil, err) }
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	authSvc := auth.NewService(pool, auth.NewPgRepo())
	sessMgr := session.NewManager(pool, session.NewPgRepo(), authSvc, wallClock{}, session.DefaultConfig())
	bumper := auth.NewPrivilegeBumper(pool)
	grants := dbconn.NewGrantRepo(pool)
	az := authz.NewAuthorizer(grants)
	authzMw := authz.NewMiddleware(az, onErr)

	groupsSvc := groups.NewService(pool, groups.NewPgRepo(), bumper)
	usersSvc := users.NewService(pool, users.NewPgRepo(), authSvc, sessMgr).WithLeaderGuard(groupsSvc)

	mux := http.NewServeMux()
	api.New(api.Deps{
		Auth: authSvc, Sessions: sessMgr, Users: usersSvc, Groups: groupsSvc,
		Access:    access.NewService(pool, access.NewPgRepo(), bumper),
		Databases: databases.NewService(pool, databases.NewPgRepo(), nil, testutil.SecretKey),
		Approval:  approval.NewService(pool, approval.PoolTx{Pool: pool}, approval.NewPgRepo(), nil),
		Authz:     az, Logger: logger,
	}).RegisterRoutes(mux, authzMw)
	auth.RegisterRoutes(mux, auth.NewHandler(authSvc, sessMgr, nil, logger), authzMw)

	return session.NewMiddleware(sessMgr, onErr).Load(csrf.NewMiddleware(csrf.Config{}, onErr).Verify(mux))
}

// browser menyimpan cookie antar request seperti browser sungguhan, dan berbicara JSON seperti SPA.
type browser struct {
	t       *testing.T
	app     http.Handler
	cookies map[string]*http.Cookie
}

func newBrowser(t *testing.T, app http.Handler, cookies ...*http.Cookie) *browser {
	b := &browser{t: t, app: app, cookies: map[string]*http.Cookie{}}
	for _, c := range cookies {
		b.cookies[c.Name] = c
	}
	return b
}

func (b *browser) request(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	b.t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", origin)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range b.cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	rec := httptest.NewRecorder()
	b.app.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	return rec
}

func (b *browser) get(path string) *httptest.ResponseRecorder {
	return b.request(http.MethodGet, path, nil, nil)
}

// send mengirim mutasi JSON dengan token CSRF di header X-CSRF-Token (kosong = tanpa header).
func (b *browser) send(method, path string, body any, token string) *httptest.ResponseRecorder {
	h := map[string]string{}
	if token != "" {
		h[csrf.HeaderName] = token
	}
	return b.request(method, path, body, h)
}

func (b *browser) sessionToken() string {
	if c, ok := b.cookies[session.CookieName]; ok {
		return c.Value
	}
	return ""
}

// apiLogin menjalani alur login SPA: GET /api/csrf (cookie CSRF pre-session) lalu POST /api/auth/login.
func (b *browser) apiLogin(username, password string) *httptest.ResponseRecorder {
	b.t.Helper()
	rec := b.get("/api/csrf")
	var out struct {
		Token string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		b.t.Fatalf("GET /api/csrf harus mengembalikan token: %v %s", err, rec.Body.String())
	}
	if _, ok := b.cookies[csrf.PreCookieName]; !ok {
		b.t.Fatal("GET /api/csrf harus menerbitkan cookie CSRF pre-session")
	}
	return b.send(http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": password}, out.Token)
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d, want %d\nbody: %.300s", rec.Code, status, rec.Body.String())
	}
}

func revokedSessions(t *testing.T, pool *pgxpool.Pool, userID int64) (revoked, total int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE revoked_at IS NOT NULL), count(*) FROM sessions WHERE user_id = $1`, userID,
	).Scan(&revoked, &total); err != nil {
		t.Fatal(err)
	}
	return revoked, total
}

func TestFlow_LoginSuccess(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	alice := testutil.CreateUser(t, pool, "alice", true)
	b := newBrowser(t, newApp(t, pool))

	rec := b.apiLogin("alice", testutil.TestPassword)
	expectStatus(t, rec, http.StatusOK)

	var ck *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName {
			ck = c
		}
	}
	if ck == nil || ck.Value == "" || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie session salah: %+v", ck)
	}
	body := rec.Body.String()
	if strings.Contains(body, testutil.TestPassword) || strings.Contains(body, ck.Value) {
		t.Fatal("password/token session tidak boleh ada di body")
	}
	if _, total := revokedSessions(t, pool, alice); total != 1 {
		t.Fatalf("session di DB = %d, want 1", total)
	}
	// Sudah login: endpoint admin bisa dibuka.
	expectStatus(t, b.get("/api/admin/users"), http.StatusOK)
}

func TestFlow_LoginFailures(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	testutil.CreateUser(t, pool, "alice", false)
	bob := testutil.CreateUser(t, pool, "bob", false)
	if _, err := pool.Exec(context.Background(), `UPDATE users SET is_active = false WHERE id = $1`, bob); err != nil {
		t.Fatal(err)
	}
	app := newApp(t, pool)

	for _, tt := range []struct{ name, user, pass string }{
		{"password salah", "alice", "salahsalah"},
		{"user tidak ada", "mallory", testutil.TestPassword},
		{"user nonaktif", "bob", testutil.TestPassword},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := newBrowser(t, app)
			rec := b.apiLogin(tt.user, tt.pass)
			expectStatus(t, rec, http.StatusUnauthorized)
			if !strings.Contains(rec.Body.String(), "invalid_credentials") {
				t.Fatalf("body = %s", rec.Body.String())
			}
			if b.sessionToken() != "" {
				t.Fatal("login gagal tidak boleh membuat cookie session")
			}
		})
	}
}

func TestFlow_LoginCSRF(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	testutil.CreateUser(t, pool, "alice", false)
	app := newApp(t, pool)
	creds := map[string]string{"username": "alice", "password": testutil.TestPassword}

	// Tanpa token CSRF.
	b := newBrowser(t, app)
	b.get("/api/csrf")
	expectStatus(t, b.send(http.MethodPost, "/api/auth/login", creds, ""), http.StatusForbidden)

	// Token salah.
	expectStatus(t, b.send(http.MethodPost, "/api/auth/login", creds, "token-salah"), http.StatusForbidden)

	// Origin dari situs lain ditolak walau token cocok.
	b = newBrowser(t, app)
	b.get("/api/csrf")
	pre := b.cookies[csrf.PreCookieName].Value
	rec := b.request(http.MethodPost, "/api/auth/login", creds, map[string]string{csrf.HeaderName: pre, "Origin": "http://evil.test"})
	expectStatus(t, rec, http.StatusForbidden)
	if strings.Contains(rec.Header().Get("Set-Cookie"), session.CookieName) {
		t.Fatal("login CSRF tidak boleh membuat session")
	}
}

func TestFlow_AccessControl(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	bob := testutil.CreateUser(t, pool, "bob", false)
	app := newApp(t, pool)
	paths := []string{"/api/admin/users", "/api/admin/groups", "/api/admin/databases"}

	anon := newBrowser(t, app)
	for _, p := range paths {
		expectStatus(t, anon.get(p), http.StatusUnauthorized)
	}

	nonAdmin := newBrowser(t, app, testutil.LoginSession(t, pool, bob))
	for _, p := range paths {
		expectStatus(t, nonAdmin.get(p), http.StatusForbidden)
	}
}

func countUsers(t *testing.T, pool *pgxpool.Pool, username string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE username = $1`, username).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFlow_MutationNeedsCSRF(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	cookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	b := newBrowser(t, newApp(t, pool), cookie)
	body := map[string]any{"username": "carol", "password": testutil.TestPassword}

	expectStatus(t, b.send(http.MethodPost, "/api/admin/users", body, ""), http.StatusForbidden)
	expectStatus(t, b.send(http.MethodPost, "/api/admin/users", body, "token-salah"), http.StatusForbidden)
	if n := countUsers(t, pool, "carol"); n != 0 {
		t.Fatalf("POST tanpa CSRF valid tidak boleh membuat user (n=%d)", n)
	}

	expectStatus(t, b.send(http.MethodPost, "/api/admin/users", body, token), http.StatusCreated)
	if n := countUsers(t, pool, "carol"); n != 1 {
		t.Fatalf("POST dengan CSRF valid harus membuat user (n=%d)", n)
	}
}

func TestFlow_Logout(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	app := newApp(t, pool)
	cookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	b := newBrowser(t, app, cookie)

	expectStatus(t, b.send(http.MethodPost, "/api/auth/logout", nil, token), http.StatusNoContent)
	if b.sessionToken() != "" {
		t.Fatal("logout harus menghapus cookie")
	}
	if revoked, _ := revokedSessions(t, pool, admin); revoked != 1 {
		t.Fatal("logout harus mengisi revoked_at")
	}

	// Cookie lama dipakai ulang (mis. dicuri sebelum logout) → sudah tidak berlaku.
	stolen := newBrowser(t, app, cookie)
	expectStatus(t, stolen.get("/api/admin/users"), http.StatusUnauthorized)
}

func TestFlow_LegacyFormLogoutStillWorks(t *testing.T) {
	// Halaman HTML lama (explorer/migration) masih memakai form POST /logout.
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	cookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	b := newBrowser(t, newApp(t, pool), cookie)

	rec := b.send(http.MethodPost, "/logout", nil, token)
	expectStatus(t, rec, http.StatusSeeOther)
	if rec.Header().Get("Location") != "/login" {
		t.Fatalf("location = %q", rec.Header().Get("Location"))
	}
	if revoked, _ := revokedSessions(t, pool, admin); revoked != 1 {
		t.Fatal("logout form harus mencabut session")
	}
}

func TestFlow_DeactivateKillsSession(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", true)
	app := newApp(t, pool)

	adminCookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	adminB := newBrowser(t, app, adminCookie)
	bobB := newBrowser(t, app, testutil.LoginSession(t, pool, bob))
	expectStatus(t, bobB.get("/api/admin/users"), http.StatusOK)

	path := fmt.Sprintf("/api/admin/users/%d", bob)
	expectStatus(t, adminB.send(http.MethodPut, path, map[string]bool{"is_admin": true, "is_active": false}, token), http.StatusNoContent)

	if revoked, total := revokedSessions(t, pool, bob); revoked != total || total != 1 {
		t.Fatalf("session bob revoked=%d total=%d, want semua dicabut (D7)", revoked, total)
	}
	expectStatus(t, bobB.get("/api/admin/users"), http.StatusUnauthorized)
	expectStatus(t, newBrowser(t, app).apiLogin("bob", testutil.TestPassword), http.StatusUnauthorized)
}

func TestFlow_DemoteAdminKillsSession(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", true)
	app := newApp(t, pool)

	adminCookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	bobB := newBrowser(t, app, testutil.LoginSession(t, pool, bob))

	path := fmt.Sprintf("/api/admin/users/%d", bob)
	expectStatus(t, newBrowser(t, app, adminCookie).send(http.MethodPut, path, map[string]bool{"is_admin": false, "is_active": true}, token),
		http.StatusNoContent)
	expectStatus(t, bobB.get("/api/admin/users"), http.StatusUnauthorized)
}

func TestFlow_SelfLockoutRejected(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	cookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	b := newBrowser(t, newApp(t, pool), cookie)

	path := fmt.Sprintf("/api/admin/users/%d", admin)
	expectStatus(t, b.send(http.MethodPut, path, map[string]bool{"is_admin": false, "is_active": false}, token), http.StatusConflict)
	expectStatus(t, b.get("/api/admin/users"), http.StatusOK) // masih admin & masih login
}

func TestFlow_PrivilegeGainRotatesSession(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", false)
	g := testutil.CreateGroup(t, pool, "analis")
	app := newApp(t, pool)

	adminCookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	adminB := newBrowser(t, app, adminCookie)
	bobB := newBrowser(t, app, testutil.LoginSession(t, pool, bob))

	before := bobB.sessionToken()
	expectStatus(t, bobB.get("/api/me"), http.StatusOK)
	if bobB.sessionToken() != before {
		t.Fatal("tanpa perubahan privilege tidak boleh rotasi")
	}

	// Masuk group → privilege_version++ → request bob berikutnya dirotasi (D6).
	expectStatus(t, adminB.send(http.MethodPost, fmt.Sprintf("/api/admin/groups/%d/members", g), map[string]int64{"user_id": bob}, token),
		http.StatusNoContent)
	expectStatus(t, bobB.get("/api/me"), http.StatusOK)
	afterGroup := bobB.sessionToken()
	if afterGroup == before || afterGroup == "" {
		t.Fatal("masuk group harus memicu rotasi session")
	}

	// Dijadikan admin → rotasi lagi, dan endpoint admin langsung bisa dibuka.
	expectStatus(t, bobB.get("/api/admin/users"), http.StatusForbidden)
	path := fmt.Sprintf("/api/admin/users/%d", bob)
	expectStatus(t, adminB.send(http.MethodPut, path, map[string]bool{"is_admin": true, "is_active": true}, token), http.StatusNoContent)
	expectStatus(t, bobB.get("/api/admin/users"), http.StatusOK)
	if bobB.sessionToken() == afterGroup {
		t.Fatal("dijadikan admin harus memicu rotasi session")
	}
}

func TestFlow_EffectiveLevel(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", false)
	gView := testutil.CreateGroup(t, pool, "viewer")
	gEdit := testutil.CreateGroup(t, pool, "editor")
	testutil.AddMember(t, pool, gView, bob)

	var dbID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO db_connections (name, host, port, db_name, username, password_enc)
		VALUES ('target', 'localhost', 5432, 'target', 'u', '\x00') RETURNING id`).Scan(&dbID); err != nil {
		t.Fatal(err)
	}
	grant := func(g int64, level string) {
		if _, err := pool.Exec(ctx, `INSERT INTO group_db_access (group_id, database_id, level) VALUES ($1, $2, $3)`, g, dbID, level); err != nil {
			t.Fatal(err)
		}
	}
	grant(gView, "view")
	grant(gEdit, "edit")
	az := authz.NewAuthorizer(dbconn.NewGrantRepo(pool))

	level := func(uid int64) authz.Level {
		t.Helper()
		l, err := az.EffectiveLevel(ctx, uid, dbID)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if level(bob) != authz.LevelView || level(admin) != authz.LevelNone {
		t.Fatalf("bob=%v admin=%v, want view & none (admin tidak otomatis punya akses, D2)", level(bob), level(admin))
	}
	testutil.AddMember(t, pool, gEdit, bob)
	if level(bob) != authz.LevelEdit {
		t.Fatalf("view + edit = %v, want edit (D1)", level(bob))
	}
	if ok, err := az.Can(ctx, authz.Principal{UserID: bob}, authz.ActMigrationExecute, dbID); err != nil || !ok {
		t.Fatalf("bob edit harus boleh execute: %v %v", ok, err)
	}
	if ok, _ := az.Can(ctx, authz.Principal{UserID: admin, IsAdmin: true}, authz.ActDBView, dbID); ok {
		t.Fatal("admin tanpa group tidak boleh melihat DB (D2)")
	}
}
