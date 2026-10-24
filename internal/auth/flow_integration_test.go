//go:build integration

package auth_test

// Test alur lengkap lewat middleware asli: session.Load → csrf.Verify → mux (authz.Protect → handler).

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/csrf"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

const origin = "http://example.com" // host default httptest.NewRequest

func newApp(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	v := view.New()
	onErr := func(w http.ResponseWriter, r *http.Request, err error) { httpx.HandleError(w, r, v, err) }
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	authSvc := auth.NewService(pool, auth.NewPgRepo())
	sessMgr := session.NewManager(pool, session.NewPgRepo(), authSvc, wallClock{}, session.DefaultConfig())
	authzMw := authz.NewMiddleware(authz.NewAuthorizer(dbconn.NewGrantRepo(pool)), onErr)

	mux := http.NewServeMux()
	auth.RegisterRoutes(mux, auth.NewHandler(authSvc, sessMgr, v, logger), authzMw)
	users.RegisterRoutes(mux, users.NewHandler(users.NewService(pool, users.NewPgRepo(), authSvc, sessMgr), v), authzMw)
	groups.RegisterRoutes(mux, groups.NewHandler(groups.NewService(pool, groups.NewPgRepo(), auth.NewPrivilegeBumper(pool)), v), authzMw)

	return session.NewMiddleware(sessMgr, onErr).Load(csrf.NewMiddleware(csrf.Config{}, onErr).Verify(mux))
}

// browser menyimpan cookie antar request seperti browser sungguhan.
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

func (b *browser) do(method, path string, form url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", origin)
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

func (b *browser) get(path string) *httptest.ResponseRecorder { return b.do(http.MethodGet, path, nil) }

func (b *browser) post(path string, form url.Values) *httptest.ResponseRecorder {
	return b.do(http.MethodPost, path, form)
}

func (b *browser) sessionToken() string {
	if c, ok := b.cookies[session.CookieName]; ok {
		return c.Value
	}
	return ""
}

// formLogin menjalani alur login asli: GET /login (dapat cookie CSRF pre-session) lalu POST /login.
func (b *browser) formLogin(username, password string) *httptest.ResponseRecorder {
	b.t.Helper()
	b.get("/login")
	pre, ok := b.cookies[csrf.PreCookieName]
	if !ok {
		b.t.Fatal("GET /login harus menerbitkan cookie CSRF pre-session")
	}
	return b.post("/login", url.Values{"username": {username}, "password": {password}, csrf.FormField: {pre.Value}})
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, location string) {
	t.Helper()
	if rec.Code != status || (location != "" && rec.Header().Get("Location") != location) {
		t.Fatalf("status=%d loc=%q, want %d %q", rec.Code, rec.Header().Get("Location"), status, location)
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

	rec := b.formLogin("alice", testutil.TestPassword)
	expectStatus(t, rec, http.StatusSeeOther, "/")

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
		t.Fatal("password/token tidak boleh ada di body")
	}
	if _, total := revokedSessions(t, pool, alice); total != 1 {
		t.Fatalf("session di DB = %d, want 1", total)
	}
	// Sudah login: halaman admin bisa dibuka.
	expectStatus(t, b.get("/admin/users"), http.StatusOK, "")
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
			expectStatus(t, b.formLogin(tt.user, tt.pass), http.StatusUnauthorized, "")
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
	creds := url.Values{"username": {"alice"}, "password": {testutil.TestPassword}}

	b := newBrowser(t, app)
	b.get("/login")
	expectStatus(t, b.post("/login", creds), http.StatusForbidden, "")

	// Origin dari situs lain ditolak walau token cocok.
	b = newBrowser(t, app)
	b.get("/login")
	form := url.Values{"username": creds["username"], "password": creds["password"], csrf.FormField: {b.cookies[csrf.PreCookieName].Value}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.test")
	req.AddCookie(b.cookies[csrf.PreCookieName])
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	expectStatus(t, rec, http.StatusForbidden, "")
	if strings.Contains(rec.Header().Get("Set-Cookie"), session.CookieName) {
		t.Fatal("login CSRF tidak boleh membuat session")
	}
}

func TestFlow_AccessControl(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	bob := testutil.CreateUser(t, pool, "bob", false)
	app := newApp(t, pool)

	anon := newBrowser(t, app)
	for _, p := range []string{"/admin/users", "/admin/groups", "/admin/users/new"} {
		expectStatus(t, anon.get(p), http.StatusSeeOther, "/login")
	}

	nonAdmin := newBrowser(t, app, testutil.LoginSession(t, pool, bob))
	for _, p := range []string{"/admin/users", "/admin/groups", "/admin/users/new"} {
		expectStatus(t, nonAdmin.get(p), http.StatusForbidden, "")
	}
}

func TestFlow_MutationNeedsCSRF(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	cookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	b := newBrowser(t, newApp(t, pool), cookie)
	form := func(tok string) url.Values {
		v := url.Values{"username": {"carol"}, "password": {testutil.TestPassword}}
		if tok != "" {
			v.Set(csrf.FormField, tok)
		}
		return v
	}

	expectStatus(t, b.post("/admin/users", form("")), http.StatusForbidden, "")
	expectStatus(t, b.post("/admin/users", form("token-salah")), http.StatusForbidden, "")
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE username = 'carol'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("POST tanpa CSRF valid tidak boleh membuat user (n=%d)", n)
	}

	expectStatus(t, b.post("/admin/users", form(token)), http.StatusSeeOther, "/admin/users")
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE username = 'carol'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("POST dengan CSRF valid harus membuat user (n=%d)", n)
	}
}

func TestFlow_Logout(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	app := newApp(t, pool)
	cookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	b := newBrowser(t, app, cookie)

	expectStatus(t, b.post("/logout", url.Values{csrf.FormField: {token}}), http.StatusSeeOther, "/login")
	if b.sessionToken() != "" {
		t.Fatal("logout harus menghapus cookie")
	}
	if revoked, _ := revokedSessions(t, pool, admin); revoked != 1 {
		t.Fatal("logout harus mengisi revoked_at")
	}

	// Cookie lama dipakai ulang (mis. dicuri sebelum logout) → sudah tidak berlaku.
	stolen := newBrowser(t, app, cookie)
	expectStatus(t, stolen.get("/admin/users"), http.StatusSeeOther, "/login")
}

func TestFlow_DeactivateKillsSession(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", true)
	app := newApp(t, pool)

	adminCookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	adminB := newBrowser(t, app, adminCookie)
	bobB := newBrowser(t, app, testutil.LoginSession(t, pool, bob))
	expectStatus(t, bobB.get("/admin/users"), http.StatusOK, "")

	// Checkbox is_active tidak dikirim → bob dinonaktifkan (is_admin tetap).
	path := fmt.Sprintf("/admin/users/%d/edit", bob)
	expectStatus(t, adminB.post(path, url.Values{csrf.FormField: {token}, "is_admin": {"1"}}), http.StatusSeeOther, path)

	if revoked, total := revokedSessions(t, pool, bob); revoked != total || total != 1 {
		t.Fatalf("session bob revoked=%d total=%d, want semua dicabut (D7)", revoked, total)
	}
	expectStatus(t, bobB.get("/admin/users"), http.StatusSeeOther, "/login")
	expectStatus(t, newBrowser(t, app).formLogin("bob", testutil.TestPassword), http.StatusUnauthorized, "")
}

func TestFlow_DemoteAdminKillsSession(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	bob := testutil.CreateUser(t, pool, "bob", true)
	app := newApp(t, pool)

	adminCookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	bobB := newBrowser(t, app, testutil.LoginSession(t, pool, bob))

	path := fmt.Sprintf("/admin/users/%d/edit", bob)
	expectStatus(t, newBrowser(t, app, adminCookie).post(path, url.Values{csrf.FormField: {token}, "is_active": {"1"}}),
		http.StatusSeeOther, path)
	expectStatus(t, bobB.get("/admin/users"), http.StatusSeeOther, "/login")
}

func TestFlow_SelfLockoutRejected(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	admin := testutil.CreateUser(t, pool, "admin", true)
	cookie, token := testutil.LoginSessionCSRF(t, pool, admin)
	b := newBrowser(t, newApp(t, pool), cookie)

	path := fmt.Sprintf("/admin/users/%d/edit", admin)
	b.post(path, url.Values{csrf.FormField: {token}})
	expectStatus(t, b.get("/admin/users"), http.StatusOK, "") // masih admin & masih login
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
	bobB.get("/login")
	if bobB.sessionToken() != before {
		t.Fatal("tanpa perubahan privilege tidak boleh rotasi")
	}

	// Masuk group → privilege_version++ → request bob berikutnya dirotasi (D6).
	expectStatus(t, adminB.post(fmt.Sprintf("/admin/groups/%d/members", g), url.Values{
		csrf.FormField: {token}, "user_id": {fmt.Sprint(bob)},
	}), http.StatusSeeOther, fmt.Sprintf("/admin/groups/%d", g))
	bobB.get("/login")
	afterGroup := bobB.sessionToken()
	if afterGroup == before || afterGroup == "" {
		t.Fatal("masuk group harus memicu rotasi session")
	}

	// Dijadikan admin → rotasi lagi, dan halaman admin langsung bisa dibuka.
	expectStatus(t, bobB.get("/admin/users"), http.StatusForbidden, "")
	path := fmt.Sprintf("/admin/users/%d/edit", bob)
	expectStatus(t, adminB.post(path, url.Values{csrf.FormField: {token}, "is_admin": {"1"}, "is_active": {"1"}}),
		http.StatusSeeOther, path)
	expectStatus(t, bobB.get("/admin/users"), http.StatusOK, "")
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
