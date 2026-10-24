package groups_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

var errDB = errors.New("platform db down")

const (
	groupID = int64(5)
	bobID   = int64(2)
	carolID = int64(3)
)

type membership struct{ group, user int64 }

// stubRepo menyimpan group & keanggotaan di memori, dengan aturan yang sama seperti PgRepo
// (nama unik, user harus ada, insert ganda tidak menambah anggota).
type stubRepo struct {
	groups  map[int64]groups.Group
	users   map[int64]string
	members map[membership]bool
	nextID  int64
	err     error
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		groups:  map[int64]groups.Group{groupID: {ID: groupID, Name: "analis"}},
		users:   map[int64]string{bobID: "bob", carolID: "carol"},
		members: map[membership]bool{},
		nextID:  100,
	}
}

func (r *stubRepo) nameTaken(name string, except int64) bool {
	for id, g := range r.groups {
		if id != except && g.Name == name {
			return true
		}
	}
	return false
}

func (r *stubRepo) List(ctx context.Context, db platformdb.DBTX) ([]groups.Group, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []groups.Group
	for _, g := range r.groups {
		out = append(out, g)
	}
	return out, nil
}

func (r *stubRepo) Get(ctx context.Context, db platformdb.DBTX, id int64) (groups.Group, error) {
	if r.err != nil {
		return groups.Group{}, r.err
	}
	g, ok := r.groups[id]
	if !ok {
		return groups.Group{}, groups.ErrNotFound
	}
	return g, nil
}

func (r *stubRepo) Create(ctx context.Context, db platformdb.DBTX, in groups.Input) (int64, error) {
	if r.nameTaken(in.Name, 0) {
		return 0, groups.ErrNameTaken
	}
	r.nextID++
	r.groups[r.nextID] = groups.Group{ID: r.nextID, Name: in.Name, Description: in.Description}
	return r.nextID, nil
}

func (r *stubRepo) Update(ctx context.Context, db platformdb.DBTX, id int64, in groups.Input) error {
	g, ok := r.groups[id]
	if !ok {
		return groups.ErrNotFound
	}
	if r.nameTaken(in.Name, id) {
		return groups.ErrNameTaken
	}
	g.Name, g.Description = in.Name, in.Description
	r.groups[id] = g
	return nil
}

func (r *stubRepo) Delete(ctx context.Context, db platformdb.DBTX, id int64) error {
	if _, ok := r.groups[id]; !ok {
		return groups.ErrNotFound
	}
	delete(r.groups, id)
	for m := range r.members {
		if m.group == id {
			delete(r.members, m)
		}
	}
	return nil
}

func (r *stubRepo) ListMembers(ctx context.Context, db platformdb.DBTX, gid int64) ([]groups.Member, error) {
	var out []groups.Member
	for m := range r.members {
		if m.group == gid {
			out = append(out, groups.Member{UserID: m.user, Username: r.users[m.user], IsActive: true})
		}
	}
	return out, nil
}

func (r *stubRepo) ListCandidates(ctx context.Context, db platformdb.DBTX, gid int64) ([]groups.UserOption, error) {
	var out []groups.UserOption
	for id, name := range r.users {
		if !r.members[membership{gid, id}] {
			out = append(out, groups.UserOption{ID: id, Username: name})
		}
	}
	return out, nil
}

func (r *stubRepo) AddMember(ctx context.Context, db platformdb.DBTX, gid, uid int64) (bool, error) {
	if _, ok := r.users[uid]; !ok {
		return false, groups.ErrUserNotFound
	}
	m := membership{gid, uid}
	if r.members[m] {
		return false, nil
	}
	r.members[m] = true
	return true, nil
}

func (r *stubRepo) RemoveMember(ctx context.Context, db platformdb.DBTX, gid, uid int64) error {
	delete(r.members, membership{gid, uid})
	return nil
}

type stubBumper struct {
	users  []int64
	groups []int64
	err    error
}

func (b *stubBumper) BumpPrivilege(ctx context.Context, userIDs ...int64) error {
	if b.err != nil {
		return b.err
	}
	b.users = append(b.users, userIDs...)
	return nil
}

func (b *stubBumper) BumpGroupMembers(ctx context.Context, gid int64) error {
	b.groups = append(b.groups, gid)
	return nil
}

var _ auth.PrivilegeBumper = (*stubBumper)(nil)

func newService(repo *stubRepo) (*groups.Service, *stubBumper) {
	b := &stubBumper{}
	return groups.NewService(nil, repo, b), b
}

func TestInput_Validate(t *testing.T) {
	tests := []struct {
		name      string
		in        groups.Input
		badFields []string
	}{
		{"valid", groups.Input{Name: "analis", Description: "tim analis"}, nil},
		{"nama kosong", groups.Input{Name: ""}, []string{"name"}},
		{"nama hanya spasi", groups.Input{Name: "   "}, []string{"name"}},
		{"nama 64 karakter", groups.Input{Name: strings.Repeat("a", 64)}, nil},
		{"nama 65 karakter", groups.Input{Name: strings.Repeat("a", 65)}, []string{"name"}},
		{"nama multibyte 64 rune", groups.Input{Name: strings.Repeat("é", 64)}, nil},
		{"deskripsi 501", groups.Input{Name: "x", Description: strings.Repeat("d", 501)}, []string{"description"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Validate()
			fe, _ := apperr.FieldErrors(err)
			if len(tt.badFields) == 0 {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			for _, f := range tt.badFields {
				if fe[f] == "" {
					t.Fatalf("field %q harus error, got %v", f, fe)
				}
			}
		})
	}
}

func TestService_CreateUpdate(t *testing.T) {
	repo := newStubRepo()
	svc, _ := newService(repo)
	ctx := context.Background()

	id, err := svc.Create(ctx, groups.Input{Name: "  developer  ", Description: " dev "})
	if err != nil {
		t.Fatal(err)
	}
	if g := repo.groups[id]; g.Name != "developer" || g.Description != "dev" {
		t.Fatalf("input harus di-trim, got %+v", g)
	}
	if _, err := svc.Create(ctx, groups.Input{Name: "analis"}); !errors.Is(err, groups.ErrNameTaken) {
		t.Fatalf("nama duplikat: err = %v", err)
	}
	if _, err := svc.Create(ctx, groups.Input{Name: ""}); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("nama kosong: err = %v", err)
	}

	if err := svc.Update(ctx, id, groups.Input{Name: "analis"}); !errors.Is(err, groups.ErrNameTaken) {
		t.Fatalf("rename ke nama lain yang dipakai: err = %v", err)
	}
	if err := svc.Update(ctx, groupID, groups.Input{Name: "analis", Description: "baru"}); err != nil {
		t.Fatalf("update dengan nama sendiri harus boleh: %v", err)
	}
	if err := svc.Update(ctx, 999, groups.Input{Name: "x"}); !errors.Is(err, groups.ErrNotFound) {
		t.Fatalf("group tidak ada: err = %v", err)
	}
}

func TestService_AddMember_BumpsPrivilege(t *testing.T) {
	repo := newStubRepo()
	svc, bumper := newService(repo)
	ctx := context.Background()

	if err := svc.AddMember(ctx, groupID, bobID); err != nil {
		t.Fatal(err)
	}
	if !repo.members[membership{groupID, bobID}] {
		t.Fatal("bob harus menjadi anggota")
	}
	if len(bumper.users) != 1 || bumper.users[0] != bobID {
		t.Fatalf("masuk group harus bump privilege (D6), got %v", bumper.users)
	}

	// Sudah anggota → tidak ada perubahan, tidak bump lagi.
	if err := svc.AddMember(ctx, groupID, bobID); err != nil {
		t.Fatal(err)
	}
	if len(bumper.users) != 1 {
		t.Fatalf("anggota yang sudah ada tidak boleh di-bump lagi, got %v", bumper.users)
	}
}

func TestService_AddMember_Errors(t *testing.T) {
	ctx := context.Background()

	svc, bumper := newService(newStubRepo())
	if err := svc.AddMember(ctx, 999, bobID); !errors.Is(err, groups.ErrNotFound) {
		t.Fatalf("group tidak ada: err = %v", err)
	}
	if err := svc.AddMember(ctx, groupID, 999); !errors.Is(err, groups.ErrUserNotFound) {
		t.Fatalf("user tidak ada: err = %v", err)
	}
	if len(bumper.users) != 0 {
		t.Fatal("gagal tambah anggota tidak boleh bump")
	}

	repo := newStubRepo()
	svc, bumper = newService(repo)
	bumper.err = errDB
	if err := svc.AddMember(ctx, groupID, bobID); !errors.Is(err, errDB) {
		t.Fatalf("bump gagal harus dilaporkan, err = %v", err)
	}
}

func TestService_RemoveMemberAndDelete_NoBump(t *testing.T) {
	repo := newStubRepo()
	repo.members[membership{groupID, bobID}] = true
	svc, bumper := newService(repo)
	ctx := context.Background()

	if err := svc.RemoveMember(ctx, groupID, bobID); err != nil {
		t.Fatal(err)
	}
	if repo.members[membership{groupID, bobID}] {
		t.Fatal("bob harus keluar dari group")
	}
	if err := svc.RemoveMember(ctx, 999, bobID); !errors.Is(err, groups.ErrNotFound) {
		t.Fatalf("group tidak ada: err = %v", err)
	}

	repo.members[membership{groupID, carolID}] = true
	if err := svc.Delete(ctx, groupID); err != nil {
		t.Fatal(err)
	}
	if len(repo.members) != 0 {
		t.Fatal("keanggotaan harus ikut terhapus")
	}
	if err := svc.Delete(ctx, groupID); !errors.Is(err, groups.ErrNotFound) {
		t.Fatalf("hapus dua kali: err = %v", err)
	}
	if len(bumper.users) != 0 || len(bumper.groups) != 0 {
		t.Fatal("privilege turun tidak perlu bump")
	}
}

func TestService_Detail(t *testing.T) {
	repo := newStubRepo()
	repo.members[membership{groupID, bobID}] = true
	svc, _ := newService(repo)

	d, err := svc.Detail(context.Background(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Members) != 1 || d.Members[0].UserID != bobID {
		t.Fatalf("members salah: %+v", d.Members)
	}
	if len(d.Candidates) != 1 || d.Candidates[0].ID != carolID {
		t.Fatalf("kandidat hanya user yang belum jadi anggota, got %+v", d.Candidates)
	}
	if _, err := svc.Detail(context.Background(), 999); !errors.Is(err, groups.ErrNotFound) {
		t.Fatalf("group tidak ada: err = %v", err)
	}
}
