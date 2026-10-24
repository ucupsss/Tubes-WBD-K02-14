package view

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/web"
)

func TestRender_Pages(t *testing.T) {
	v := MustNew(web.FS)
	req := httptest.NewRequest(http.MethodGet, "/db/1/migrations", nil)
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))

	t.Run("migration list", func(t *testing.T) {
		rec := httptest.NewRecorder()
		v.Render(rec, req, http.StatusOK, "migration/list.html", PageData{
			Title: "Riwayat Migration",
			Data: map[string]any{
				"DatabaseID": int64(1),
				"Migrations": []struct{}{},
				"CanEdit":    true,
			},
		})
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, "Belum ada migration") || !strings.Contains(body, "alice") {
			t.Fatalf("status=%d body=%s", rec.Code, body)
		}
	})

	t.Run("migration detail without explain partial", func(t *testing.T) {
		rec := httptest.NewRecorder()
		v.Render(rec, req, http.StatusOK, "migration/detail.html", PageData{
			Title: "Buat tabel",
			Data: map[string]any{
				"Migration": sampleMigration(),
				"Runs":      []runRow{},
				"CanEdit":   true,
				"ShowPlan":  false,
			},
		})
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, "Buat tabel") || !strings.Contains(body, "Analisis") || !strings.Contains(body, "Belum ada percobaan") {
			t.Fatalf("status=%d body=%s", rec.Code, body)
		}
	})

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

	t.Run("migration new", func(t *testing.T) {
		rec := httptest.NewRecorder()
		v.Render(rec, req, http.StatusOK, "migration/new.html", PageData{
			Title: "Migration Baru",
			Data: map[string]any{
				"DatabaseID": int64(1),
				"Input":      map[string]any{"Title": "", "SQLText": ""},
				"Errors":     map[string]string{},
				"CanEdit":    true,
			},
		})
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, "Simpan") || !strings.Contains(body, "Batal") || !strings.Contains(body, `name="_csrf"`) {
			t.Fatalf("status=%d body=%s", rec.Code, body)
		}
	})

	t.Run("migration edit", func(t *testing.T) {
		rec := httptest.NewRecorder()
		v.Render(rec, req, http.StatusUnprocessableEntity, "migration/edit.html", PageData{
			Title: "Ubah Migration",
			Data: map[string]any{
				"DatabaseID":  int64(1),
				"MigrationID": int64(9),
				"CanEdit":     true,
				"ShowPlan":    false,
				"Input":       map[string]any{"Title": "Judul", "SQLText": "SELECT 1"},
				"Errors":      map[string]string{"title": "Judul wajib diisi"},
			},
		})
		body := rec.Body.String()
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Analisis") || !strings.Contains(body, "Judul wajib diisi") || !strings.Contains(body, `name="analyze"`) {
			t.Fatalf("status=%d body=%s", rec.Code, body)
		}
	})

	t.Run("database list", func(t *testing.T) {
		rec := httptest.NewRecorder()
		v.Render(rec, req, http.StatusOK, "admin/databases/index.html", PageData{
			Title: "Kelola Target Database",
			Data:  map[string]any{"Connections": []dbconn.Connection{}},
		})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Daftar Target Database") {
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

type migRow struct {
	ID                 int64
	DatabaseID         int64
	Title              string
	SQLText            string
	Status             string
	CreatedByUsername  string
	ExecutedByUsername string
	CreatedAt          time.Time
	DurationMS         *int32
}

func (migRow) VersionLabel() string { return "—" }
func (migRow) ErrorText() string    { return "" }
func (m migRow) WhenLabel() time.Time {
	return m.CreatedAt
}

func sampleMigration() migRow {
	return migRow{
		ID: 1, DatabaseID: 1, Title: "Buat tabel", SQLText: "SELECT 1",
		Status: "draft", CreatedByUsername: "alice", CreatedAt: time.Now(),
	}
}

type runRow struct{}

func (runRow) ErrorText() string { return "" }
func (runRow) Duration() int     { return 0 }
