package users_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

func newHandler(repo *stubRepo) (*users.Handler, *stubAccounts, *stubRevoker) {
	svc, acc, rev := newService(repo)
	return users.NewHandler(svc, view.New()), acc, rev
}

func asAdmin(req *http.Request) *http.Request {
	return req.WithContext(authz.WithPrincipal(req.Context(), authz.Principal{UserID: adminID, Username: "admin", IsAdmin: true}))
}

func postForm(path string, form url.Values, id int64) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if id != 0 {
		req.SetPathValue("id", strconv.FormatInt(id, 10))
	}
	return asAdmin(req)
}

func TestHandler_List(t *testing.T) {
	h, _, _ := newHandler(newStubRepo(fixtures()...))
	rec := httptest.NewRecorder()
	h.List(rec, asAdmin(httptest.NewRequest(http.MethodGet, "/admin/users", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	repo := newStubRepo()
	repo.err = errDB
	h, _, _ = newHandler(repo)
	rec = httptest.NewRecorder()
	h.List(rec, asAdmin(httptest.NewRequest(http.MethodGet, "/admin/users", nil)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("db error: status = %d, want 500", rec.Code)
	}
}

func TestHandler_Create(t *testing.T) {
	tests := []struct {
		name       string
		form       url.Values
		accErr     error
		wantStatus int
	}{
		{"sukses", url.Values{"username": {"carol"}, "password": {"password123"}, "is_admin": {"1"}}, nil, http.StatusSeeOther},
		{"validasi gagal", url.Values{"username": {"x"}, "password": {"pendek"}}, nil, http.StatusUnprocessableEntity},
		{"username dipakai", url.Values{"username": {"bob"}, "password": {"password123"}}, auth.ErrUsernameTaken, http.StatusConflict},
		{"db error", url.Values{"username": {"carol"}, "password": {"password123"}}, errDB, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, acc, _ := newHandler(newStubRepo(fixtures()...))
			acc.err = tt.accErr
			rec := httptest.NewRecorder()
			h.Create(rec, postForm("/admin/users", tt.form, 0))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusSeeOther {
				if rec.Header().Get("Location") != "/admin/users" {
					t.Fatalf("loc = %q", rec.Header().Get("Location"))
				}
				if len(acc.created) != 1 || !acc.created[0].IsAdmin || acc.created[0].Username != "carol" {
					t.Fatalf("input salah: %+v", acc.created)
				}
			}
			if strings.Contains(rec.Body.String(), "password123") {
				t.Fatal("password tidak boleh dikirim balik")
			}
		})
	}
}

func TestHandler_Edit(t *testing.T) {
	h, _, _ := newHandler(newStubRepo(fixtures()...))

	req := asAdmin(httptest.NewRequest(http.MethodGet, "/admin/users/2/edit", nil))
	req.SetPathValue("id", "2")
	rec := httptest.NewRecorder()
	h.Edit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	for _, id := range []string{"999", "abc"} {
		req := asAdmin(httptest.NewRequest(http.MethodGet, "/admin/users/"+id+"/edit", nil))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		h.Edit(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("id=%s: status = %d, want 404", id, rec.Code)
		}
	}
}

func TestHandler_Update(t *testing.T) {
	repo := newStubRepo(fixtures()...)
	h, _, rev := newHandler(repo)

	// Checkbox is_active tidak dikirim → user dinonaktifkan.
	rec := httptest.NewRecorder()
	h.Update(rec, postForm("/admin/users/2/edit", url.Values{}, bobID))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/users/2/edit" {
		t.Fatalf("status=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	if repo.users[bobID].IsActive || len(rev.revoked) != 1 {
		t.Fatalf("bob harus nonaktif & session dicabut, got %+v revoked=%v", repo.users[bobID], rev.revoked)
	}
}

func TestHandler_Update_SelfLockout(t *testing.T) {
	repo := newStubRepo(fixtures()...)
	h, _, _ := newHandler(repo)

	rec := httptest.NewRecorder()
	h.Update(rec, postForm("/admin/users/1/edit", url.Values{"is_active": {"1"}}, adminID))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/users/1/edit" {
		t.Fatalf("status=%d loc=%q, want redirect kembali ke edit", rec.Code, rec.Header().Get("Location"))
	}
	if !repo.users[adminID].IsAdmin || len(repo.flags) != 0 {
		t.Fatal("admin tidak boleh mencabut dirinya sendiri")
	}
}

func TestHandler_ResetPassword(t *testing.T) {
	repo := newStubRepo(fixtures()...)
	h, _, _ := newHandler(repo)

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, postForm("/admin/users/2/password", url.Values{"password": {"pendek"}}, bobID))
	if rec.Code != http.StatusUnprocessableEntity || len(repo.passwords) != 0 {
		t.Fatalf("password pendek: status = %d, want 422 tanpa perubahan", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ResetPassword(rec, postForm("/admin/users/2/password", url.Values{"password": {"passwordbaru1"}}, bobID))
	if rec.Code != http.StatusSeeOther || repo.passwords[bobID] == "" {
		t.Fatalf("status = %d, want 303 dan password tersimpan", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/users/2/edit" {
		t.Fatalf("loc = %q, want /admin/users/2/edit (tanpa password di URL)", loc)
	}
}
