package migration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

func TestHandler_Create_RejectsInvalidJSON(t *testing.T) {
	h := NewHandler(NewService(nil, nil, nil), nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/databases/1/migrations", strings.NewReader(`{"title":"x","unknown":1}`))
	req.SetPathValue("dbID", "1")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Create_RejectsEmptyTitle(t *testing.T) {
	h := NewHandler(NewService(nil, nil, nil), nil, nil)
	body := `{"title":" ","up_sql":"SELECT 1","down_sql":"SELECT 1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/databases/1/migrations", strings.NewReader(body))
	req.SetPathValue("dbID", "1")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 1, Username: "alice"}))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "title") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Validate_OK(t *testing.T) {
	h := NewHandler(NewService(nil, nil, nil), nil, nil)
	body := `{"title":"t","up_sql":"CREATE TABLE t (id int)","down_sql":"DROP TABLE t"}`
	req := httptest.NewRequest(http.MethodPost, "/api/databases/1/migrations/3/validate", strings.NewReader(body))
	req.SetPathValue("dbID", "1")
	req.SetPathValue("id", "3")
	rec := httptest.NewRecorder()
	h.Validate(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"valid":true`) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
