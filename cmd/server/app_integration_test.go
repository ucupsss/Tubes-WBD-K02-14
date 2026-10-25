//go:build integration

package main

// Test end-to-end lewat router produksi (newApp) dengan template HTML asli, mengikuti alur minimum spesifikasi:
// Login → Access Control → Select Database → View Schema/Data → Create Migration → Execute →
// Commit/Rollback → Update Migration History.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/csrf"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
	"github.com/ucupsss/Tubes-WBD-K02-14/web"
)

type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]*http.Cookie
	csrf    string // token CSRF dari halaman terakhir yang memuat form
}

func newBrowser(t *testing.T, h http.Handler) *browser {
	return &browser{t: t, h: h, cookies: map[string]*http.Cookie{}}
}

func (b *browser) do(method, path string, form url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	var body io.Reader = http.NoBody
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", "http://example.com")
	}
	for _, c := range b.cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	if tok := extractCSRF(rec.Body.String()); tok != "" {
		b.csrf = tok
	}
	return rec
}

func (b *browser) get(path string) *httptest.ResponseRecorder { return b.do(http.MethodGet, path, nil) }

// post mengirim form dengan token CSRF yang dibaca dari halaman sebelumnya, seperti browser sungguhan.
func (b *browser) post(path string, form url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if _, ok := form[csrf.FormField]; !ok {
		form.Set(csrf.FormField, b.csrf)
	}
	return b.do(http.MethodPost, path, form)
}

func (b *browser) login(username, password string) *httptest.ResponseRecorder {
	b.t.Helper()
	page := b.get("/login")
	expect(b.t, page, http.StatusOK, "")
	return b.post("/login", url.Values{"username": {username}, "password": {password}})
}

func extractCSRF(html string) string {
	const marker = `name="_csrf" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i+len(marker):]
	if j := strings.IndexByte(rest, '"'); j > 0 {
		return rest[:j]
	}
	return ""
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, location string) {
	t.Helper()
	if rec.Code != status || (location != "" && rec.Header().Get("Location") != location) {
		t.Fatalf("status=%d loc=%q, want %d %q\nbody: %.400s", rec.Code, rec.Header().Get("Location"), status, location, rec.Body.String())
	}
}

func expectBody(t *testing.T, rec *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	body := rec.Body.String()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("body tidak memuat %q\nbody: %.600s", w, body)
		}
	}
}

func scalar[T any](t *testing.T, pool *pgxpool.Pool, q string, args ...any) T {
	t.Helper()
	var v T
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

type env struct {
	pool       *pgxpool.Pool
	app        http.Handler
	targetName string
	target     *pgxpool.Pool
}

func setup(t *testing.T) env {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	targetName, target := testutil.NewTargetDB(t, `
		CREATE TABLE customers (
			id    SERIAL PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			name  TEXT NOT NULL
		);
		CREATE INDEX idx_customers_name ON customers (name);
		INSERT INTO customers (email, name) VALUES ('a@x.test', 'Ani'), ('b@x.test', 'Budi');`)

	cfg := config.Config{
		AppEnv:                  "development",
		SecretKey:               bytes.Repeat([]byte{7}, 32),
		SessionIdleTimeout:      30 * time.Minute,
		SessionAbsoluteTimeout:  8 * time.Hour,
		SessionRotationInterval: 15 * time.Minute,
		SessionGracePeriod:      30 * time.Second,
	}
	a, err := newApp(cfg, pool, web.FS, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Conns.CloseAll)
	return env{pool: pool, app: a.Handler, targetName: targetName, target: target}
}

// targetForm mengisi form koneksi baru yang menunjuk ke database target milik test.
func targetForm(t *testing.T, name, dbName string) url.Values {
	t.Helper()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	return url.Values{
		"name": {name}, "host": {u.Hostname()}, "port": {port}, "db_name": {dbName},
		"username": {u.User.Username()}, "password": {pw}, "sslmode": {"disable"},
	}
}

func TestApp_PublicRoutes(t *testing.T) {
	e := setup(t)
	b := newBrowser(t, e.app)

	expect(t, b.get("/healthz"), http.StatusOK, "")
	expect(t, b.get("/static/css/app.css"), http.StatusOK, "")
	rec := b.get("/login")
	expect(t, rec, http.StatusOK, "")
	expectBody(t, rec, `name="username"`, `name="password"`)
	if b.csrf == "" {
		t.Fatal("form login harus berisi token CSRF")
	}
	expect(t, b.get("/halaman-yang-tidak-ada"), http.StatusNotFound, "")

	for _, p := range []string{"/", "/admin/users", "/admin/groups", "/admin/databases", "/db/1", "/db/1/migrations"} {
		expect(t, b.get(p), http.StatusSeeOther, "/login")
	}
}

func TestApp_SpecFlow(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "admin", true)
	bobID := testutil.CreateUser(t, e.pool, "bob", false)

	// 1. Login admin lewat form (cookie session HttpOnly, tanpa password/token di body).
	admin := newBrowser(t, e.app)
	rec := admin.login("admin", testutil.TestPassword)
	expect(t, rec, http.StatusSeeOther, "/")
	if tok := admin.cookies[session.CookieName]; tok == nil || !tok.HttpOnly {
		t.Fatal("login harus memasang cookie session HttpOnly")
	}
	expectBody(t, admin.get("/admin/users"), "bob")

	// 2. Admin mendaftarkan koneksi database target, membuat group, menambah bob, memberi akses view.
	expect(t, admin.get("/admin/databases/new"), http.StatusOK, "")
	expect(t, admin.post("/admin/databases", targetForm(t, "target-test", e.targetName)), http.StatusSeeOther, "/admin/databases")
	dbID := scalar[int64](t, e.pool, `SELECT id FROM db_connections WHERE name = 'target-test'`)
	list := admin.get("/admin/databases")
	if strings.Contains(list.Body.String(), "password_enc") {
		t.Fatal("password koneksi tidak boleh tampil")
	}

	admin.get("/admin/groups/new")
	expect(t, admin.post("/admin/groups", url.Values{"name": {"analis"}}), http.StatusSeeOther, "")
	groupID := scalar[int64](t, e.pool, `SELECT id FROM groups WHERE name = 'analis'`)
	admin.get(fmt.Sprintf("/admin/groups/%d", groupID))
	expect(t, admin.post(fmt.Sprintf("/admin/groups/%d/members", groupID), url.Values{"user_id": {fmt.Sprint(bobID)}}),
		http.StatusSeeOther, fmt.Sprintf("/admin/groups/%d", groupID))

	accessPath := fmt.Sprintf("/admin/groups/%d/access", groupID)
	admin.get(accessPath)
	expect(t, admin.post(accessPath, url.Values{fmt.Sprintf("grant_%d", dbID): {"view"}}), http.StatusSeeOther, accessPath)

	// Admin tidak otomatis punya akses ke database (D2).
	expect(t, admin.get(fmt.Sprintf("/db/%d", dbID)), http.StatusForbidden, "")

	// 3. Bob login, memilih database dari dashboard, melihat schema & data.
	bob := newBrowser(t, e.app)
	expect(t, bob.login("bob", testutil.TestPassword), http.StatusSeeOther, "/")
	expectBody(t, bob.get("/"), "target-test", fmt.Sprintf("/db/%d", dbID))
	expect(t, bob.get("/admin/users"), http.StatusForbidden, "")

	expectBody(t, bob.get(fmt.Sprintf("/db/%d", dbID)), "customers")
	detail := bob.get(fmt.Sprintf("/db/%d/tables/public/customers", dbID))
	expect(t, detail, http.StatusOK, "")
	expectBody(t, detail, "email", "idx_customers_name", "customers_pkey")
	data := bob.get(fmt.Sprintf("/db/%d/tables/public/customers/data", dbID))
	expect(t, data, http.StatusOK, "")
	expectBody(t, data, "a@x.test", "Budi")
	expect(t, bob.get(fmt.Sprintf("/db/%d/tables/public/tidak_ada", dbID)), http.StatusNotFound, "")
	expect(t, bob.get(fmt.Sprintf("/db/%d/tables/public/customers%%3B%%20DROP%%20TABLE%%20customers/data", dbID)), http.StatusNotFound, "")

	// View Only: bisa melihat riwayat migration, tidak bisa membuat.
	migPath := fmt.Sprintf("/db/%d/migrations", dbID)
	expect(t, bob.get(migPath), http.StatusOK, "")
	expect(t, bob.get(migPath+"/new"), http.StatusForbidden, "")
	expect(t, bob.post(migPath, url.Values{"title": {"x"}, "sql_text": {"CREATE TABLE x (id int)"}}), http.StatusForbidden, "")

	// 4. Admin menaikkan akses ke edit → session bob dirotasi pada request berikutnya.
	admin.get(accessPath)
	expect(t, admin.post(accessPath, url.Values{fmt.Sprintf("grant_%d", dbID): {"edit"}}), http.StatusSeeOther, accessPath)
	before := bob.cookies[session.CookieName].Value
	expect(t, bob.get(migPath+"/new"), http.StatusOK, "")
	if bob.cookies[session.CookieName].Value == before {
		t.Fatal("privilege naik harus merotasi session")
	}

	// 5. Create migration → Execute → commit; versi 1 tercatat.
	expect(t, bob.post(migPath, url.Values{
		"title":    {"Tambah kolom phone"},
		"sql_text": {"ALTER TABLE customers ADD COLUMN phone TEXT; CREATE INDEX idx_customers_phone ON customers (phone);"},
	}), http.StatusSeeOther, "")
	okID := scalar[int64](t, e.pool, `SELECT id FROM migrations WHERE title = 'Tambah kolom phone'`)
	bob.get(fmt.Sprintf("%s/%d", migPath, okID))
	expect(t, bob.post(fmt.Sprintf("%s/%d/execute", migPath, okID), nil), http.StatusSeeOther, "")
	if s := scalar[string](t, e.pool, `SELECT status FROM migrations WHERE id = $1`, okID); s != "success" {
		t.Fatalf("status = %s, want success", s)
	}
	if v := scalar[int32](t, e.pool, `SELECT version FROM migrations WHERE id = $1`, okID); v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	if n := scalar[int](t, e.target, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'customers' AND column_name = 'phone'`); n != 1 {
		t.Fatal("kolom phone harus ada di target setelah commit")
	}

	// 6. Migration gagal di statement kedua → seluruhnya di-rollback, status failed, tanpa versi.
	expect(t, bob.post(migPath, url.Values{
		"title":    {"Gagal"},
		"sql_text": {"CREATE TABLE orders (id int); INSERT INTO tabel_tidak_ada VALUES (1);"},
	}), http.StatusSeeOther, "")
	failID := scalar[int64](t, e.pool, `SELECT id FROM migrations WHERE title = 'Gagal'`)
	bob.get(fmt.Sprintf("%s/%d", migPath, failID))
	expect(t, bob.post(fmt.Sprintf("%s/%d/execute", migPath, failID), nil), http.StatusSeeOther, "")
	if s := scalar[string](t, e.pool, `SELECT status FROM migrations WHERE id = $1`, failID); s != "failed" {
		t.Fatalf("status = %s, want failed", s)
	}
	if n := scalar[int](t, e.target, `SELECT count(*) FROM information_schema.tables WHERE table_name = 'orders'`); n != 0 {
		t.Fatal("statement pertama harus ikut di-rollback")
	}

	// 7. Riwayat migration menampilkan versi, SQL, pelaksana, dan status.
	hist := bob.get(migPath)
	expect(t, hist, http.StatusOK, "")
	expectBody(t, hist, "Tambah kolom phone", "Gagal")
	show := bob.get(fmt.Sprintf("%s/%d", migPath, okID))
	expectBody(t, show, "ALTER TABLE customers ADD COLUMN phone TEXT", "bob")
	if runs := scalar[int](t, e.pool, `SELECT count(*) FROM migration_runs`); runs != 2 {
		t.Fatalf("migration_runs = %d, want 2 (sukses + gagal)", runs)
	}

	// 8. Pencabutan akses: bob dinonaktifkan → session langsung mati.
	admin.get(fmt.Sprintf("/admin/users/%d/edit", bobID))
	expect(t, admin.post(fmt.Sprintf("/admin/users/%d/edit", bobID), url.Values{}), http.StatusSeeOther, "")
	expect(t, bob.get(migPath), http.StatusSeeOther, "/login")

	// 9. Logout admin → cookie lama ditolak.
	oldCookie := admin.cookies[session.CookieName].Value
	admin.get("/")
	expect(t, admin.post("/logout", nil), http.StatusSeeOther, "/login")
	stale := newBrowser(t, e.app)
	stale.cookies[session.CookieName] = &http.Cookie{Name: session.CookieName, Value: oldCookie}
	expect(t, stale.get("/admin/users"), http.StatusSeeOther, "/login")
}

func TestApp_CSRFRequired(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "admin", true)
	b := newBrowser(t, e.app)
	expect(t, b.login("admin", testutil.TestPassword), http.StatusSeeOther, "/")

	form := url.Values{"name": {"tanpa-csrf"}}
	form.Set(csrf.FormField, "")
	expect(t, b.post("/admin/groups", form), http.StatusForbidden, "")
	form.Set(csrf.FormField, "salah")
	expect(t, b.post("/admin/groups", form), http.StatusForbidden, "")
	if n := scalar[int](t, e.pool, `SELECT count(*) FROM groups`); n != 0 {
		t.Fatal("POST tanpa CSRF valid tidak boleh mengubah data")
	}

	b.get("/admin/groups/new")
	expect(t, b.post("/admin/groups", url.Values{"name": {"dengan-csrf"}}), http.StatusSeeOther, "")
}

func TestApp_UnavailableTarget(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "admin", true)
	bobID := testutil.CreateUser(t, e.pool, "bob", false)

	admin := newBrowser(t, e.app)
	admin.login("admin", testutil.TestPassword)
	form := targetForm(t, "mati", e.targetName)
	form.Set("port", "1") // tidak ada Postgres di port ini
	admin.get("/admin/databases/new")
	expect(t, admin.post("/admin/databases", form), http.StatusSeeOther, "/admin/databases")
	dbID := scalar[int64](t, e.pool, `SELECT id FROM db_connections WHERE name = 'mati'`)

	g := testutil.CreateGroup(t, e.pool, "g")
	testutil.AddMember(t, e.pool, g, bobID)
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO group_db_access (group_id, database_id, level) VALUES ($1, $2, 'view')`, g, dbID); err != nil {
		t.Fatal(err)
	}

	bob := newBrowser(t, e.app)
	bob.login("bob", testutil.TestPassword)
	rec := bob.get(fmt.Sprintf("/db/%d", dbID))
	expect(t, rec, http.StatusBadGateway, "")
	if strings.Contains(rec.Body.String(), form.Get("password")) && form.Get("password") != "" {
		t.Fatal("pesan error tidak boleh memuat password koneksi")
	}
}
