//go:build integration

package main

// Test end-to-end lewat router produksi (newApp). Login, administrasi, dan migration memakai API JSON.
// Explorer masih halaman HTML. Alur yang sudah ada di server: login → akses → lihat schema/data →
// buat migration → ajukan review. Menjalankan SQL di database target dilakukan worker, bukan API.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
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

// api mengirim JSON seperti SPA: cookie milik browser ini dan token CSRF di header X-CSRF-Token.
func (b *browser) api(method, path string, body any) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.apiWithToken(method, path, body, b.csrf)
}

func (b *browser) apiWithToken(method, path string, body any, token string) *httptest.ResponseRecorder {
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
		req.Header.Set("Origin", "http://example.com")
		if token != "" {
			req.Header.Set(csrf.HeaderName, token)
		}
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
	return rec
}

func jsonField(t *testing.T, rec *httptest.ResponseRecorder, key string) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bukan JSON: %v\n%.300s", err, rec.Body.String())
	}
	v, _ := out[key].(string)
	return v
}

// login mengikuti alur SPA: GET /api/csrf → POST /api/auth/login. Token CSRF milik session disimpan
// di b.csrf sehingga form HTML (explorer/migration) dan request API berikutnya memakainya.
func (b *browser) login(username, password string) *httptest.ResponseRecorder {
	b.t.Helper()
	pre := jsonField(b.t, b.get("/api/csrf"), "csrf_token")
	rec := b.apiWithToken(http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": password}, pre)
	if rec.Code == http.StatusOK {
		b.csrf = jsonField(b.t, rec, "csrf_token")
	}
	return rec
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
		SecretKey:               testutil.SecretKey,
		SessionIdleTimeout:      30 * time.Minute,
		SessionAbsoluteTimeout:  8 * time.Hour,
		SessionRotationInterval: 15 * time.Minute,
		SessionGracePeriod:      30 * time.Second,
	}
	a, err := newApp(cfg, pool, withSPA(web.FS), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Conns.CloseAll)
	return env{pool: pool, app: a.Handler, targetName: targetName, target: target}
}

// withSPA menambahkan dist/index.html palsu ke web.FS: di CI frontend belum di-build, tetapi routing
// SPA (fallback index.html) tetap harus teruji.
func withSPA(base fs.FS) fs.FS {
	return overlayFS{base: base, extra: fstest.MapFS{
		"dist/index.html": {Data: []byte(`<!doctype html><html><body><div id="root"></div></body></html>`)},
	}}
}

type overlayFS struct {
	base  fs.FS
	extra fstest.MapFS
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if f, err := o.extra.Open(name); err == nil {
		return f, nil
	}
	return o.base.Open(name)
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

	// Halaman SPA: path klien dijawab index.html (guard login ada di router SPA, bukan di server).
	for _, p := range []string{"/", "/login", "/approvals", "/admin/users", "/admin/groups/3", "/admin/databases"} {
		rec := b.get(p)
		expect(t, rec, http.StatusOK, "")
		expectBody(t, rec, `id="root"`)
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s: SPA harus memakai header CSP", p)
		}
	}

	// File ber-ekstensi yang tidak ada → halaman error bawaan, bukan index.html menyamar sebagai aset.
	nf := b.get("/assets/tidak-ada.js")
	expect(t, nf, http.StatusNotFound, "")
	expectBody(t, nf, `class="error-code">404<`, "Tidak Ditemukan", "<title>404 Tidak Ditemukan · Tubes WBD</title>", "<strong>Tubes WBD</strong>")

	// API: 404 JSON, dan endpoint terproteksi 401 JSON (bukan redirect).
	rec := b.get("/api/tidak-ada")
	expect(t, rec, http.StatusNotFound, "")
	expectBody(t, rec, `"not_found"`)
	rec = b.get("/api/admin/users")
	expect(t, rec, http.StatusUnauthorized, "")
	expectBody(t, rec, `"unauthenticated"`)

	// Explorer HTML tetap mengarahkan tamu ke /login. Halaman migration lama sudah diganti API;
	// path itu jatuh ke SPA.
	expect(t, b.get("/db/1"), http.StatusSeeOther, "/login")
	spaMig := b.get("/db/1/migrations")
	expect(t, spaMig, http.StatusOK, "")
	expectBody(t, spaMig, `id="root"`)
}

func TestApp_SpecFlow(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "admin", true)
	bobID := testutil.CreateUser(t, e.pool, "bob", false)

	// 1. Login admin lewat API (cookie session HttpOnly, tanpa password/token di body).
	admin := newBrowser(t, e.app)
	rec := admin.login("admin", testutil.TestPassword)
	expect(t, rec, http.StatusOK, "")
	if tok := admin.cookies[session.CookieName]; tok == nil || !tok.HttpOnly {
		t.Fatal("login harus memasang cookie session HttpOnly")
	}
	expectBody(t, admin.api(http.MethodGet, "/api/admin/users", nil), "bob")

	// 2. Admin mendaftarkan koneksi database target, membuat group, menambah bob, memberi akses view.
	rec = admin.api(http.MethodPost, "/api/admin/databases", targetJSON(t, "target-test", e.targetName))
	expect(t, rec, http.StatusCreated, "")
	dbID := scalar[int64](t, e.pool, `SELECT id FROM db_connections WHERE name = 'target-test'`)
	list := admin.api(http.MethodGet, "/api/admin/databases", nil)
	if strings.Contains(list.Body.String(), "password") {
		t.Fatal("password koneksi tidak boleh tampil")
	}

	expect(t, admin.api(http.MethodPost, "/api/admin/groups", map[string]string{"name": "analis"}), http.StatusCreated, "")
	groupID := scalar[int64](t, e.pool, `SELECT id FROM groups WHERE name = 'analis'`)
	expect(t, admin.api(http.MethodPost, fmt.Sprintf("/api/admin/groups/%d/members", groupID), map[string]int64{"user_id": bobID}),
		http.StatusNoContent, "")

	accessPath := fmt.Sprintf("/api/admin/groups/%d/access", groupID)
	grant := func(level string) map[string]any {
		return map[string]any{"grants": map[string]string{fmt.Sprint(dbID): level}}
	}
	expect(t, admin.api(http.MethodPut, accessPath, grant("view")), http.StatusNoContent, "")

	// Admin tidak otomatis punya akses ke database (D2).
	expect(t, admin.get(fmt.Sprintf("/db/%d", dbID)), http.StatusForbidden, "")

	// 3. Bob login, memilih database dari dashboard, melihat schema & data.
	bob := newBrowser(t, e.app)
	expect(t, bob.login("bob", testutil.TestPassword), http.StatusOK, "")
	dash := bob.api(http.MethodGet, "/api/databases", nil)
	expect(t, dash, http.StatusOK, "")
	expectBody(t, dash, "target-test", fmt.Sprintf(`"id":%d`, dbID), `"level":"view"`)
	expect(t, bob.api(http.MethodGet, "/api/admin/users", nil), http.StatusForbidden, "")

	// D15: ID database tak ada tidak dibedakan dari tak berhak; halaman error memuat kode dan penjelasan.
	forbidden := bob.get("/db/999999")
	expect(t, forbidden, http.StatusForbidden, "")
	expectBody(t, forbidden, `class="error-code">403<`, "Akses Ditolak", "Hubungi Platform Admin")

	expectBody(t, bob.get(fmt.Sprintf("/db/%d", dbID)), "customers")
	detail := bob.get(fmt.Sprintf("/db/%d/tables/public/customers", dbID))
	expect(t, detail, http.StatusOK, "")
	expectBody(t, detail, "email", "idx_customers_name", "customers_pkey")
	data := bob.get(fmt.Sprintf("/db/%d/tables/public/customers/data", dbID))
	expect(t, data, http.StatusOK, "")
	expectBody(t, data, "a@x.test", "Budi")
	expect(t, bob.get(fmt.Sprintf("/db/%d/tables/public/tidak_ada", dbID)), http.StatusNotFound, "")
	expect(t, bob.get(fmt.Sprintf("/db/%d/tables/public/customers%%3B%%20DROP%%20TABLE%%20customers/data", dbID)), http.StatusNotFound, "")

	// View Only: boleh membaca riwayat lewat API, tidak boleh membuat.
	migAPI := fmt.Sprintf("/api/databases/%d/migrations", dbID)
	expect(t, bob.api(http.MethodGet, migAPI, nil), http.StatusOK, "")
	expect(t, bob.api(http.MethodPost, migAPI, map[string]any{
		"title": "x", "up_sql": "CREATE TABLE x (id int)", "down_sql": "DROP TABLE x",
	}), http.StatusForbidden, "")

	// 4. Admin menaikkan akses ke edit → session bob dirotasi pada request berikutnya.
	expect(t, admin.api(http.MethodPut, accessPath, grant("edit")), http.StatusNoContent, "")
	before := bob.cookies[session.CookieName].Value
	expect(t, bob.api(http.MethodGet, "/api/databases", nil), http.StatusOK, "")
	if bob.cookies[session.CookieName].Value == before {
		t.Fatal("privilege naik harus merotasi session")
	}

	// 5. Buat migration dan ajukan review. API tidak menjalankan SQL di database target.
	created := bob.api(http.MethodPost, migAPI, map[string]any{
		"title":    "Tambah kolom phone",
		"up_sql":   "ALTER TABLE customers ADD COLUMN phone TEXT",
		"down_sql": "ALTER TABLE customers DROP COLUMN phone",
	})
	expect(t, created, http.StatusCreated, "")
	okID := scalar[int64](t, e.pool, `SELECT id FROM migrations WHERE title = 'Tambah kolom phone'`)
	if n := scalar[int](t, e.target, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'customers' AND column_name = 'phone'`); n != 0 {
		t.Fatal("membuat migration tidak boleh mengubah database target")
	}
	submitted := bob.api(http.MethodPost, fmt.Sprintf("%s/%d/submit", migAPI, okID), map[string]any{})
	expect(t, submitted, http.StatusCreated, "")
	expectBody(t, submitted, `"status":"pending_review"`, `"kind":"apply"`)
	var version *int32
	if err := e.pool.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, okID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != nil {
		t.Fatalf("version = %v, want null sebelum worker berjalan", *version)
	}

	hist := bob.api(http.MethodGet, fmt.Sprintf("/api/databases/%d/executions", dbID), nil)
	expect(t, hist, http.StatusOK, "")
	expectBody(t, hist, "Tambah kolom phone", `"pending_review"`)

	// 6. Pencabutan akses: bob dinonaktifkan → session langsung mati.
	expect(t, admin.api(http.MethodPut, fmt.Sprintf("/api/admin/users/%d", bobID), map[string]bool{"is_admin": false, "is_active": false}),
		http.StatusNoContent, "")
	expect(t, bob.api(http.MethodGet, "/api/me", nil), http.StatusUnauthorized, "")
	expect(t, bob.api(http.MethodGet, migAPI, nil), http.StatusUnauthorized, "")

	// 9. Logout admin → cookie lama ditolak.
	oldCookie := admin.cookies[session.CookieName].Value
	expect(t, admin.api(http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent, "")
	stale := newBrowser(t, e.app)
	stale.cookies[session.CookieName] = &http.Cookie{Name: session.CookieName, Value: oldCookie}
	expect(t, stale.api(http.MethodGet, "/api/admin/users", nil), http.StatusUnauthorized, "")
}

func TestApp_CSRFRequired(t *testing.T) {
	// Mutasi API wajib membawa token CSRF milik session.
	e := setup(t)
	bobID := testutil.CreateUser(t, e.pool, "bob", false)
	dbID := testutil.CreateConnection(t, e.pool, e.targetName)
	g := testutil.CreateGroup(t, e.pool, "editors")
	testutil.AddMember(t, e.pool, g, bobID)
	testutil.Grant(t, e.pool, g, dbID, "edit")

	b := newBrowser(t, e.app)
	expect(t, b.login("bob", testutil.TestPassword), http.StatusOK, "")
	migPath := fmt.Sprintf("/api/databases/%d/migrations", dbID)
	form := url.Values{"title": {"tanpa-csrf"}, "sql_text": {"SELECT 1"}}

	form.Set(csrf.FormField, "")
	expect(t, b.do(http.MethodPost, migPath, form), http.StatusForbidden, "")
	form.Set(csrf.FormField, "salah")
	expect(t, b.do(http.MethodPost, migPath, form), http.StatusForbidden, "")
	if n := scalar[int](t, e.pool, `SELECT count(*) FROM migrations`); n != 0 {
		t.Fatal("POST tanpa CSRF valid tidak boleh mengubah data")
	}

	// Token CSRF salah ditolak. Token yang benar membuat migration lewat API dan tidak menyentuh target.
	expect(t, b.api(http.MethodPost, migPath, map[string]any{
		"title": "dengan-csrf", "up_sql": "SELECT 1", "down_sql": "SELECT 1",
	}), http.StatusCreated, "")
	if n := scalar[int](t, e.pool, `SELECT count(*) FROM migrations`); n != 1 {
		t.Fatalf("migrations = %d, want 1", n)
	}
}

func TestApp_UnavailableTarget(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "admin", true)
	bobID := testutil.CreateUser(t, e.pool, "bob", false)

	admin := newBrowser(t, e.app)
	admin.login("admin", testutil.TestPassword)
	conn := targetJSON(t, "mati", e.targetName)
	conn["port"] = 1 // tidak ada Postgres di port ini
	expect(t, admin.api(http.MethodPost, "/api/admin/databases", conn), http.StatusCreated, "")
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
	if pw, _ := conn["password"].(string); pw != "" && strings.Contains(rec.Body.String(), pw) {
		t.Fatal("pesan error tidak boleh memuat password koneksi")
	}
}

func TestApp_Explain(t *testing.T) {
	e := setup(t)
	bobID := testutil.CreateUser(t, e.pool, "bob", false)
	carolID := testutil.CreateUser(t, e.pool, "carol", false)
	dbID := testutil.CreateConnection(t, e.pool, e.targetName)
	editors := testutil.CreateGroup(t, e.pool, "editors")
	viewers := testutil.CreateGroup(t, e.pool, "viewers")
	testutil.AddMember(t, e.pool, editors, bobID)
	testutil.AddMember(t, e.pool, viewers, carolID)
	testutil.Grant(t, e.pool, editors, dbID, "edit")
	testutil.Grant(t, e.pool, viewers, dbID, "view")
	migID := testutil.CreateDraft(t, e.pool, dbID, bobID,
		"CREATE TABLE t (id INT); INSERT INTO t VALUES (1); SELECT * FROM customers WHERE email = 'a@x.test'")
	explainURL := fmt.Sprintf("/db/%d/migrations/%d/explain", dbID, migID)

	bob := newBrowser(t, e.app)
	bob.login("bob", testutil.TestPassword)
	rec := bob.post(explainURL, url.Values{"analyze": {"1"}})
	expect(t, rec, http.StatusOK, "")
	expectBody(t, rec, "ModifyTable", "customers_email_key", "di-rollback")
	if n := scalar[int](t, e.target, `SELECT count(*) FROM information_schema.tables WHERE table_name = 't'`); n != 0 {
		t.Fatal("analisis tidak boleh meninggalkan table t di target")
	}
	var frozen *time.Time
	if err := e.pool.QueryRow(context.Background(), `
		SELECT frozen_at FROM migration_revisions WHERE migration_id = $1`, migID).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	if frozen != nil {
		t.Fatal("analisis tidak boleh membekukan revisi")
	}

	carol := newBrowser(t, e.app)
	carol.login("carol", testutil.TestPassword)
	expect(t, carol.post(explainURL, url.Values{}), http.StatusForbidden, "")

	if _, err := e.pool.Exec(context.Background(), `
		UPDATE migration_revisions SET frozen_at = now() WHERE migration_id = $1`, migID); err != nil {
		t.Fatal(err)
	}
	expect(t, bob.post(explainURL, url.Values{}), http.StatusConflict, "")
}
