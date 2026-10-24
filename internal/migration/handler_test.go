package migration

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/clock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

func TestHandler_Create_Validation(t *testing.T) {
	h := NewHandler(NewService(nil, &fakeRepo{}, nil, clock.Real{}), view.New(), nil)
	form := url.Values{}
	form.Set("title", "")
	form.Set("sql_text", "")
	req := httptest.NewRequest(http.MethodPost, "/db/1/migrations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("dbID", "1")
	rec := httptest.NewRecorder()

	h.Create(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestHandler_Create_Success(t *testing.T) {
	h := NewHandler(NewService(nil, &fakeRepo{created: 9}, nil, clock.Real{}), view.New(), nil)
	form := url.Values{}
	form.Set("title", "Buat tabel")
	form.Set("sql_text", "CREATE TABLE t (id int)")
	req := httptest.NewRequest(http.MethodPost, "/db/1/migrations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("dbID", "1")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec := httptest.NewRecorder()

	h.Create(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/db/1/migrations/9" {
		t.Fatalf("location = %q", loc)
	}
}

func TestHandler_Execute_SuccessIsRejected(t *testing.T) {
	repo := &fakeRepo{m: sample(StatusSuccess)}
	h := NewHandler(NewService(nil, repo, nil, clock.NewFake(time.Now())), view.New(), nil)
	req := httptest.NewRequest(http.MethodPost, "/db/1/migrations/1/execute", nil)
	req.SetPathValue("dbID", "1")
	req.SetPathValue("id", "1")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec := httptest.NewRecorder()

	h.Execute(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}
