package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

func TestPathInt64(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/db/4", nil)
	req.SetPathValue("dbID", "4")
	id, err := PathInt64(req, "dbID")
	if err != nil || id != 4 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	req.SetPathValue("dbID", "nope")
	_, err = PathInt64(req, "dbID")
	if apperr.KindOf(err) != apperr.KindNotFound {
		t.Fatalf("kind = %v", apperr.KindOf(err))
	}
}

func TestHandleError_Statuses(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{apperr.Invalid("Permintaan tidak valid"), http.StatusBadRequest},
		{apperr.Forbidden("Anda tidak memiliki akses"), http.StatusForbidden},
		{apperr.NotFound("Data tidak ditemukan"), http.StatusNotFound},
		{apperr.Conflict("bentrok"), http.StatusConflict},
		{apperr.Unavailable("Database tidak dapat dihubungi", nil), http.StatusBadGateway},
		{apperr.Internal("rahasia", nil), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/db/1", nil)
		HandleError(rec, req, view.New(), tc.err)
		if rec.Code != tc.want {
			t.Fatalf("%v status=%d want %d", tc.err, rec.Code, tc.want)
		}
		if tc.want == http.StatusInternalServerError && strings.Contains(rec.Body.String(), "rahasia") {
			t.Fatalf("internal error leaked: %s", rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/secret", nil)
	HandleError(rec, req, view.New(), apperr.Unauthenticated("masuk"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("status=%d loc=%s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLogger_OmitsQueryAndCookie(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/db/1/migrations?token=secret", nil)
	req.Header.Set("Cookie", "dbmp_session=abc")
	req = req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: 9, Username: "alice"}))
	h.ServeHTTP(httptest.NewRecorder(), req)
	log := buf.String()
	if strings.Contains(log, "secret") || strings.Contains(log, "abc") || strings.Contains(log, "token=") {
		t.Fatalf("log leaked request data: %s", log)
	}
	if !strings.Contains(log, "/db/1/migrations") || !strings.Contains(log, "user_id=9") {
		t.Fatalf("log missing fields: %s", log)
	}
}

func TestRecover(t *testing.T) {
	h := Recover(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
}
