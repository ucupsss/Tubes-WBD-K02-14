package view

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/web"
)

func TestRender_Pages(t *testing.T) {
	v := MustNew(web.FS)
	req := httptest.NewRequest(http.MethodGet, "/db/1/migrations", nil)
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))

	t.Run("error page", func(t *testing.T) {
		rec := httptest.NewRecorder()
		v.Render(rec, req, http.StatusForbidden, "errors/error.html", PageData{
			Title: "Akses ditolak",
			Data:  map[string]any{"Status": 403, "Message": "Anda tidak memiliki akses"},
		})
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Anda tidak memiliki akses") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestStaticFiles(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(web.FS))

	for _, path := range []string{"/static/css/app.css", "/static/css/pico.min.css"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Fatalf("%s status=%d len=%d", path, rec.Code, rec.Body.Len())
		}
	}
}

func TestRender_EmptyViewWritesStatusOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	New().Render(rec, req, http.StatusTeapot, "migration/list.html", PageData{})
	if rec.Code != http.StatusTeapot || rec.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}
