//go:build integration

package main

// Test end-to-end untuk API JSON milik A (auth, admin, approval) lewat router produksi.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

type apiClient struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]*http.Cookie
	csrf    string
}

func newAPIClient(t *testing.T, h http.Handler) *apiClient {
	return &apiClient{t: t, h: h, cookies: map[string]*http.Cookie{}}
}

// do mengirim JSON. Token CSRF dikirim di header seperti SPA; skipCSRF menguji request tanpa token.
func (c *apiClient) do(method, path string, body any, skipCSRF bool) (*httptest.ResponseRecorder, map[string]any) {
	c.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", "http://example.com")
		if !skipCSRF && c.csrf != "" {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	for _, ck := range c.cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	var out map[string]any
	if rec.Body.Len() > 0 && strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			c.t.Fatalf("%s %s: respons bukan JSON: %v\n%s", method, path, err, rec.Body.String())
		}
	}
	return rec, out
}

func (c *apiClient) get(path string) (*httptest.ResponseRecorder, map[string]any) {
	return c.do(http.MethodGet, path, nil, false)
}
func (c *apiClient) send(method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	return c.do(method, path, body, false)
}

// login mengikuti alur SPA: GET /api/csrf → POST /api/auth/login → pakai csrf_token dari respons.
func (c *apiClient) login(username, password string) *httptest.ResponseRecorder {
	c.t.Helper()
	rec, out := c.get("/api/csrf")
	expectStatus(c.t, rec, http.StatusOK)
	c.csrf, _ = out["csrf_token"].(string)
	rec, out = c.send(http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": password})
	if rec.Code == http.StatusOK {
		c.csrf, _ = out["csrf_token"].(string)
	}
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d\nbody: %.500s", rec.Code, want, rec.Body.String())
	}
}

// expectError memeriksa status, code, dan (opsional) details.reason pada body error.
func expectError(t *testing.T, rec *httptest.ResponseRecorder, out map[string]any, status int, code, reason string) {
	t.Helper()
	expectStatus(t, rec, status)
	e, _ := out["error"].(map[string]any)
	if e == nil || e["code"] != code {
		t.Fatalf("error = %v, want code %q", out, code)
	}
	if e["message"] == "" {
		t.Fatalf("message kosong: %v", out)
	}
	if reason != "" {
		d, _ := e["details"].(map[string]any)
		if d["reason"] != reason {
			t.Fatalf("details = %v, want reason %q", d, reason)
		}
	}
}

func idOf(t *testing.T, out map[string]any) int64 {
	t.Helper()
	v, ok := out["id"].(float64)
	if !ok {
		t.Fatalf("body tanpa id: %v", out)
	}
	return int64(v)
}

func TestAPI_AuthFlow(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "bob", false)
	c := newAPIClient(t, e.app)

	rec, out := c.get("/api/me")
	expectError(t, rec, out, http.StatusUnauthorized, "unauthenticated", "")

	rec = c.login("bob", "salah-sekali")
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	expectError(t, rec, body, http.StatusUnauthorized, "invalid_credentials", "")

	expectStatus(t, c.login("bob", testutil.TestPassword), http.StatusOK)
	rec, out = c.get("/api/me")
	expectStatus(t, rec, http.StatusOK)
	user := out["user"].(map[string]any)
	if user["username"] != "bob" || user["is_admin"] != false || out["csrf_token"] == "" {
		t.Fatalf("me = %v", out)
	}
	if leds, ok := out["led_groups"].([]any); !ok || len(leds) != 0 {
		t.Fatalf("led_groups = %v", out["led_groups"])
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("respons me memuat kata password: %s", rec.Body.String())
	}

	// Logout: 204, lalu session tidak berlaku lagi.
	rec, _ = c.send(http.MethodPost, "/api/auth/logout", nil)
	expectStatus(t, rec, http.StatusNoContent)
	rec, out = c.get("/api/me")
	expectError(t, rec, out, http.StatusUnauthorized, "unauthenticated", "")
}

func TestAPI_CSRFRequired(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "admin2", true)
	c := newAPIClient(t, e.app)
	expectStatus(t, c.login("admin2", testutil.TestPassword), http.StatusOK)

	rec, out := c.do(http.MethodPost, "/api/admin/groups", map[string]string{"name": "x"}, true)
	expectError(t, rec, out, http.StatusForbidden, "forbidden", "")

	rec, _ = c.send(http.MethodPost, "/api/admin/groups", map[string]string{"name": "x"})
	expectStatus(t, rec, http.StatusCreated)

	// Login tanpa token pre-login juga ditolak.
	anon := newAPIClient(t, e.app)
	rec, out = anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin2", "password": testutil.TestPassword}, true)
	expectError(t, rec, out, http.StatusForbidden, "forbidden", "")
}

func TestAPI_AdminRequiresAdmin(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "bob", false)
	c := newAPIClient(t, e.app)

	rec, out := c.get("/api/admin/users")
	expectError(t, rec, out, http.StatusUnauthorized, "unauthenticated", "")

	expectStatus(t, c.login("bob", testutil.TestPassword), http.StatusOK)
	for _, p := range []string{"/api/admin/users", "/api/admin/groups", "/api/admin/databases"} {
		rec, out = c.get(p)
		expectError(t, rec, out, http.StatusForbidden, "forbidden", "")
	}
}

func TestAPI_UnknownPathIsJSON404(t *testing.T) {
	e := setup(t)
	c := newAPIClient(t, e.app)
	rec, out := c.get("/api/tidak-ada")
	expectError(t, rec, out, http.StatusNotFound, "not_found", "")
}

func TestAPI_AdminUsers(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "admin2", true)
	c := newAPIClient(t, e.app)
	expectStatus(t, c.login("admin2", testutil.TestPassword), http.StatusOK)

	rec, out := c.send(http.MethodPost, "/api/admin/users", map[string]any{"username": "carol", "password": "password123"})
	expectStatus(t, rec, http.StatusCreated)
	carol := idOf(t, out)

	rec, out = c.send(http.MethodPost, "/api/admin/users", map[string]any{"username": "carol", "password": "password123"})
	expectError(t, rec, out, http.StatusConflict, "conflict", "")

	rec, out = c.send(http.MethodPost, "/api/admin/users", map[string]any{"username": "x", "password": "pendek"})
	expectError(t, rec, out, http.StatusBadRequest, "invalid", "")
	fields := out["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	if fields["username"] == nil || fields["password"] == nil {
		t.Fatalf("fields = %v", fields)
	}

	rec, out = c.send(http.MethodPost, "/api/admin/users", map[string]any{"username": "dave", "password": "password123", "bukan_field": 1})
	expectError(t, rec, out, http.StatusBadRequest, "invalid", "")

	rec, out = c.get("/api/admin/users")
	expectStatus(t, rec, http.StatusOK)
	if len(out["items"].([]any)) != 2 || strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatalf("daftar user: %s", rec.Body.String())
	}

	rec, _ = c.send(http.MethodPut, "/api/admin/users/"+strconv.FormatInt(carol, 10), map[string]any{"is_admin": false, "is_active": false})
	expectStatus(t, rec, http.StatusNoContent)
	rec, out = c.get("/api/admin/users/" + strconv.FormatInt(carol, 10))
	expectStatus(t, rec, http.StatusOK)
	if out["is_active"] != false {
		t.Fatalf("user = %v", out)
	}

	// Admin tidak boleh menonaktifkan dirinya sendiri.
	self := e.adminID(t, "admin2")
	rec, out = c.send(http.MethodPut, fmt.Sprintf("/api/admin/users/%d", self), map[string]any{"is_admin": true, "is_active": false})
	expectError(t, rec, out, http.StatusConflict, "conflict", "")

	rec, _ = c.send(http.MethodPost, fmt.Sprintf("/api/admin/users/%d/password", carol), map[string]string{"password": "passwordbaru123"})
	expectStatus(t, rec, http.StatusNoContent)
	rec, out = c.get("/api/admin/users/999999")
	expectError(t, rec, out, http.StatusNotFound, "not_found", "")
}

func (e env) adminID(t *testing.T, username string) int64 {
	t.Helper()
	return scalar[int64](t, e.pool, `SELECT id FROM users WHERE username = $1`, username)
}

func TestAPI_GroupsLeaderAndAccess(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "admin2", true)
	alice := testutil.CreateUser(t, e.pool, "alice", false)
	bob := testutil.CreateUser(t, e.pool, "bob", false)
	c := newAPIClient(t, e.app)
	expectStatus(t, c.login("admin2", testutil.TestPassword), http.StatusOK)

	// Koneksi database target.
	conn := targetJSON(t, "target-test", e.targetName)
	rec, out := c.send(http.MethodPost, "/api/admin/databases", conn)
	expectStatus(t, rec, http.StatusCreated)
	dbID := idOf(t, out)
	rec, out = c.get("/api/admin/databases")
	expectStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("daftar koneksi membocorkan password: %s", rec.Body.String())
	}
	if len(out["items"].([]any)) != 1 {
		t.Fatalf("items = %v", out["items"])
	}
	rec, out = c.send(http.MethodPost, fmt.Sprintf("/api/admin/databases/%d/test", dbID), map[string]any{
		"host": conn["host"], "port": conn["port"], "db_name": conn["db_name"], "username": conn["username"], "sslmode": "disable",
	})
	expectStatus(t, rec, http.StatusOK)
	if out["ok"] != true {
		t.Fatalf("uji koneksi tersimpan: %v", out)
	}
	bad := targetJSON(t, "x", e.targetName)
	bad["password"] = "password-salah"
	rec, out = c.send(http.MethodPost, "/api/admin/databases/test", bad)
	expectStatus(t, rec, http.StatusOK)
	if out["ok"] != false || strings.Contains(rec.Body.String(), "password-salah") {
		t.Fatalf("uji koneksi gagal harus ok=false tanpa bocor password: %s", rec.Body.String())
	}

	// Group, anggota, leader.
	rec, out = c.send(http.MethodPost, "/api/admin/groups", map[string]string{"name": "dev", "description": "tim dev"})
	expectStatus(t, rec, http.StatusCreated)
	gid := idOf(t, out)
	g := fmt.Sprintf("/api/admin/groups/%d", gid)

	rec, out = c.send(http.MethodPost, "/api/admin/groups", map[string]string{"name": "dev"})
	expectError(t, rec, out, http.StatusConflict, "conflict", "")

	rec, _ = c.send(http.MethodPost, g+"/members", map[string]int64{"user_id": alice})
	expectStatus(t, rec, http.StatusNoContent)

	rec, out = c.send(http.MethodPut, g+"/leader", map[string]int64{"user_id": bob})
	expectError(t, rec, out, http.StatusConflict, "conflict", "leader_not_member")

	rec, _ = c.send(http.MethodPut, g+"/leader", map[string]int64{"user_id": alice})
	expectStatus(t, rec, http.StatusNoContent)

	rec, out = c.get(g)
	expectStatus(t, rec, http.StatusOK)
	grp := out["group"].(map[string]any)
	if grp["leader_name"] != "alice" || len(out["members"].([]any)) != 1 || !out["members"].([]any)[0].(map[string]any)["is_leader"].(bool) {
		t.Fatalf("detail group = %v", out)
	}

	// Leader tidak boleh dikeluarkan maupun dinonaktifkan.
	rec, out = c.send(http.MethodDelete, fmt.Sprintf("%s/members/%d", g, alice), nil)
	expectError(t, rec, out, http.StatusConflict, "conflict", "is_leader")
	rec, out = c.send(http.MethodPut, fmt.Sprintf("/api/admin/users/%d", alice), map[string]any{"is_admin": false, "is_active": false})
	expectError(t, rec, out, http.StatusConflict, "conflict", "is_leader")

	// Akses database.
	rec, out = c.get(g + "/access")
	expectStatus(t, rec, http.StatusOK)
	if len(out["items"].([]any)) != 1 || out["items"].([]any)[0].(map[string]any)["level"] != "none" {
		t.Fatalf("akses awal = %v", out)
	}
	rec, _ = c.send(http.MethodPut, g+"/access", map[string]any{"grants": map[string]string{strconv.FormatInt(dbID, 10): "edit"}})
	expectStatus(t, rec, http.StatusNoContent)
	rec, out = c.send(http.MethodPut, g+"/access", map[string]any{"grants": map[string]string{strconv.FormatInt(dbID, 10): "admin"}})
	expectError(t, rec, out, http.StatusBadRequest, "invalid", "")
	rec, out = c.get(g + "/access")
	if out["items"].([]any)[0].(map[string]any)["level"] != "edit" {
		t.Fatalf("akses setelah simpan = %v", out)
	}

	// Alice kini leader yang melihat group-nya di /api/me.
	alicec := newAPIClient(t, e.app)
	expectStatus(t, alicec.login("alice", testutil.TestPassword), http.StatusOK)
	rec, out = alicec.get("/api/me")
	if leds := out["led_groups"].([]any); len(leds) != 1 || leds[0].(map[string]any)["name"] != "dev" {
		t.Fatalf("led_groups = %v", out["led_groups"])
	}

	// Setelah leader dikosongkan, group boleh dikeluarkan anggotanya dan dihapus.
	rec, _ = c.send(http.MethodPut, g+"/leader", map[string]any{"user_id": nil})
	expectStatus(t, rec, http.StatusNoContent)
	rec, _ = c.send(http.MethodDelete, fmt.Sprintf("%s/members/%d", g, alice), nil)
	expectStatus(t, rec, http.StatusNoContent)
	rec, _ = c.send(http.MethodDelete, g, nil)
	expectStatus(t, rec, http.StatusNoContent)
	rec, out = c.get(g)
	expectError(t, rec, out, http.StatusNotFound, "not_found", "")

	// Koneksi yang sudah dihapus.
	rec, _ = c.send(http.MethodDelete, fmt.Sprintf("/api/admin/databases/%d", dbID), nil)
	expectStatus(t, rec, http.StatusNoContent)
	rec, out = c.get(fmt.Sprintf("/api/admin/databases/%d", dbID))
	expectError(t, rec, out, http.StatusNotFound, "not_found", "")
}

func TestAPI_Approvals(t *testing.T) {
	e := setup(t)
	testutil.CreateUser(t, e.pool, "bob", false)
	c := newAPIClient(t, e.app)
	expectStatus(t, c.login("bob", testutil.TestPassword), http.StatusOK)

	rec, out := c.get("/api/approvals")
	expectStatus(t, rec, http.StatusOK)
	if items, ok := out["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("inbox kosong = %v", out)
	}

	// Modul execution (C) belum tersedia: keputusan ditolak dengan alasan yang jelas, bukan 500.
	rec, out = c.send(http.MethodPost, "/api/executions/1/approve", map[string]string{"comment": "ok"})
	expectError(t, rec, out, http.StatusConflict, "conflict", "executions_unavailable")
	rec, out = c.send(http.MethodPost, "/api/executions/abc/reject", nil)
	expectError(t, rec, out, http.StatusNotFound, "not_found", "")

	anon := newAPIClient(t, e.app)
	rec, out = anon.get("/api/approvals")
	expectError(t, rec, out, http.StatusUnauthorized, "unauthenticated", "")
}

func TestAPI_ErrorBodiesNeverLeakInternals(t *testing.T) {
	e := setup(t)
	c := newAPIClient(t, e.app)
	rec, _ := c.get("/api/tidak-ada")
	for _, bad := range []string{"pgx", "sql:", "goroutine", "panic"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), bad) {
			t.Fatalf("body error memuat %q: %s", bad, rec.Body.String())
		}
	}
}

// targetJSON mengisi body koneksi baru yang menunjuk ke database target milik test.
func targetJSON(t *testing.T, name, dbName string) map[string]any {
	t.Helper()
	f := targetForm(t, name, dbName)
	port, err := strconv.Atoi(f.Get("port"))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"name": name, "host": f.Get("host"), "port": port, "db_name": dbName,
		"username": f.Get("username"), "password": f.Get("password"), "sslmode": "disable",
	}
}
