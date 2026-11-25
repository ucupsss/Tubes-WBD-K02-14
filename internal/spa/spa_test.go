package spa_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/spa"
)

func built() fstest.MapFS {
	return fstest.MapFS{
		"dist/index.html":       {Data: []byte("<!doctype html><div id=root></div>")},
		"dist/assets/app-1.js":  {Data: []byte("console.log(1)")},
		"dist/assets/app-1.css": {Data: []byte("body{}")},
		"dist/favicon.svg":      {Data: []byte("<svg/>")},
		"templates/x.html":      {Data: []byte("bukan spa")},
	}
}

func serve(t *testing.T, h *spa.Handler, method, target string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	ok := h.Serve(rec, httptest.NewRequest(method, target, nil))
	return rec, ok
}

func TestServe_IndexForClientRoutes(t *testing.T) {
	h := spa.New(built())
	if !h.Available() {
		t.Fatal("Available() = false")
	}
	for _, p := range []string{"/", "/login", "/admin/groups/3", "/approvals", "/index.html"} {
		rec, ok := serve(t, h, http.MethodGet, p)
		if !ok || rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id=root`) {
			t.Fatalf("%s: ok=%v status=%d body=%q", p, ok, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: content-type = %q", p, ct)
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: index tidak boleh di-cache lama", p)
		}
	}
}

func TestServe_StaticFiles(t *testing.T) {
	h := spa.New(built())
	rec, ok := serve(t, h, http.MethodGet, "/assets/app-1.js")
	if !ok || rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("asset: ok=%v status=%d body=%q", ok, rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("asset ber-hash harus immutable, got %q", cc)
	}
	rec, ok = serve(t, h, http.MethodGet, "/favicon.svg")
	if !ok || rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("favicon: ok=%v status=%d cc=%q", ok, rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestServe_MissingFileWithExtensionIsNotIndex(t *testing.T) {
	h := spa.New(built())
	rec, ok := serve(t, h, http.MethodGet, "/assets/hilang.js")
	if ok {
		t.Fatalf("file ber-ekstensi yang tidak ada tidak boleh dijawab index.html (status %d)", rec.Code)
	}
}

func TestServe_OnlyGetAndHead(t *testing.T) {
	h := spa.New(built())
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if _, ok := serve(t, h, m, "/login"); ok {
			t.Fatalf("%s tidak boleh dijawab SPA", m)
		}
	}
	if _, ok := serve(t, h, http.MethodHead, "/login"); !ok {
		t.Fatal("HEAD harus dijawab")
	}
}

func TestServe_DoesNotExposeOtherEmbeddedFolders(t *testing.T) {
	h := spa.New(built())
	rec, ok := serve(t, h, http.MethodGet, "/../templates/x.html")
	if ok && strings.Contains(rec.Body.String(), "bukan spa") {
		t.Fatal("folder di luar dist bocor")
	}
	rec, ok = serve(t, h, http.MethodGet, "/templates/x.html")
	if ok {
		t.Fatalf("path ber-ekstensi yang bukan di dist harus ditolak, status %d", rec.Code)
	}
}

func TestServe_SecurityHeaders(t *testing.T) {
	rec, _ := serve(t, spa.New(built()), http.MethodGet, "/")
	for _, k := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rec.Header().Get(k) == "" {
			t.Fatalf("header %s hilang", k)
		}
	}
}

func TestNew_NotBuilt(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"tanpa dist":         {"templates/x.html": {Data: []byte("x")}},
		"dist tanpa index":   {"dist/.gitkeep": {Data: nil}},
		"dist dengan assets": {"dist/assets/a.js": {Data: []byte("x")}},
	} {
		h := spa.New(fsys)
		if h.Available() {
			t.Fatalf("%s: Available() seharusnya false", name)
		}
		if _, ok := serve(t, h, http.MethodGet, "/"); ok {
			t.Fatalf("%s: tidak boleh menjawab tanpa index.html", name)
		}
	}
}
