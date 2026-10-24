package groups_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

func newHandler(repo *stubRepo) (*groups.Handler, *stubBumper) {
	svc, b := newService(repo)
	return groups.NewHandler(svc, view.New()), b
}

func request(method, path string, form url.Values, pathValues map[string]string) *http.Request {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	return req
}

func TestHandler_ListAndNew(t *testing.T) {
	h, _ := newHandler(newStubRepo())
	for name, fn := range map[string]http.HandlerFunc{"list": h.List, "new": h.New} {
		rec := httptest.NewRecorder()
		fn(rec, request(http.MethodGet, "/admin/groups", nil, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", name, rec.Code)
		}
	}

	repo := newStubRepo()
	repo.err = errDB
	h, _ = newHandler(repo)
	rec := httptest.NewRecorder()
	h.List(rec, request(http.MethodGet, "/admin/groups", nil, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("db error: status = %d, want 500", rec.Code)
	}
}

func TestHandler_Create(t *testing.T) {
	tests := []struct {
		name       string
		form       url.Values
		wantStatus int
	}{
		{"sukses", url.Values{"name": {"developer"}}, http.StatusSeeOther},
		{"nama kosong", url.Values{"name": {""}}, http.StatusUnprocessableEntity},
		{"nama dipakai", url.Values{"name": {"analis"}}, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newHandler(newStubRepo())
			rec := httptest.NewRecorder()
			h.Create(rec, request(http.MethodPost, "/admin/groups", tt.form, nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusSeeOther && rec.Header().Get("Location") != "/admin/groups/101" {
				t.Fatalf("loc = %q, want ke detail group baru", rec.Header().Get("Location"))
			}
		})
	}
}

func TestHandler_Show(t *testing.T) {
	h, _ := newHandler(newStubRepo())
	tests := map[string]int{"5": http.StatusOK, "999": http.StatusNotFound, "abc": http.StatusNotFound}
	for id, want := range tests {
		rec := httptest.NewRecorder()
		h.Show(rec, request(http.MethodGet, "/admin/groups/"+id, nil, map[string]string{"id": id}))
		if rec.Code != want {
			t.Fatalf("id=%s: status = %d, want %d", id, rec.Code, want)
		}
	}
}

func TestHandler_Update(t *testing.T) {
	repo := newStubRepo()
	h, _ := newHandler(repo)

	rec := httptest.NewRecorder()
	h.Update(rec, request(http.MethodPost, "/admin/groups/5/edit", url.Values{"name": {""}}, map[string]string{"id": "5"}))
	if rec.Code != http.StatusUnprocessableEntity || repo.groups[groupID].Name != "analis" {
		t.Fatalf("nama kosong: status = %d, want 422 tanpa perubahan", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.Update(rec, request(http.MethodPost, "/admin/groups/5/edit", url.Values{"name": {"analis-data"}}, map[string]string{"id": "5"}))
	if rec.Code != http.StatusSeeOther || repo.groups[groupID].Name != "analis-data" {
		t.Fatalf("status = %d, name = %q", rec.Code, repo.groups[groupID].Name)
	}
}

func TestHandler_Delete(t *testing.T) {
	repo := newStubRepo()
	h, _ := newHandler(repo)

	rec := httptest.NewRecorder()
	h.Delete(rec, request(http.MethodPost, "/admin/groups/5/delete", url.Values{}, map[string]string{"id": "5"}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups" {
		t.Fatalf("status=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	if _, ok := repo.groups[groupID]; ok {
		t.Fatal("group harus terhapus")
	}

	rec = httptest.NewRecorder()
	h.Delete(rec, request(http.MethodPost, "/admin/groups/5/delete", url.Values{}, map[string]string{"id": "5"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("hapus dua kali: status = %d, want 404", rec.Code)
	}
}

func TestHandler_AddMember(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		wantStatus int
		wantMember bool
	}{
		{"sukses", "2", http.StatusSeeOther, true},
		{"user_id kosong", "", http.StatusSeeOther, false},
		{"user_id bukan angka", "abc", http.StatusSeeOther, false},
		{"user tidak ada", "999", http.StatusNotFound, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newStubRepo()
			h, bumper := newHandler(repo)
			rec := httptest.NewRecorder()
			h.AddMember(rec, request(http.MethodPost, "/admin/groups/5/members",
				url.Values{"user_id": {tt.userID}}, map[string]string{"id": "5"}))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := repo.members[membership{groupID, bobID}]; got != tt.wantMember {
				t.Fatalf("member = %v, want %v", got, tt.wantMember)
			}
			if tt.wantMember != (len(bumper.users) == 1) {
				t.Fatalf("bump = %v, want hanya saat anggota baru", bumper.users)
			}
		})
	}
}

func TestHandler_RemoveMember(t *testing.T) {
	repo := newStubRepo()
	repo.members[membership{groupID, bobID}] = true
	h, _ := newHandler(repo)

	rec := httptest.NewRecorder()
	h.RemoveMember(rec, request(http.MethodPost, "/admin/groups/5/members/2/delete", url.Values{},
		map[string]string{"id": "5", "userID": "2"}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups/5" {
		t.Fatalf("status=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	if repo.members[membership{groupID, bobID}] {
		t.Fatal("bob harus keluar")
	}

	rec = httptest.NewRecorder()
	h.RemoveMember(rec, request(http.MethodPost, "/admin/groups/5/members/x/delete", url.Values{},
		map[string]string{"id": "5", "userID": "x"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("userID invalid: status = %d, want 404", rec.Code)
	}
}
