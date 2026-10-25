//go:build integration

package dbconn_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

type getter struct{ db platformdb.DBTX }

func (g getter) GetByID(ctx context.Context, id int64) (dbconn.Connection, error) {
	c, err := databases.NewPgRepo().GetByID(ctx, g.db, id)
	if errors.Is(err, databases.ErrNotFound) {
		return dbconn.Connection{}, dbconn.ErrNotFound
	}
	return c, err
}

func TestPg_ManagerPool(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	targetName, _ := testutil.NewTargetDB(t, `CREATE TABLE t (id int); INSERT INTO t VALUES (1), (2);`)
	dbID := testutil.CreateConnection(t, pool, targetName)
	mgr := dbconn.NewManager(getter{pool}, testutil.SecretKey)
	t.Cleanup(mgr.CloseAll)
	ctx := context.Background()

	p1, err := mgr.Pool(ctx, dbID)
	if err != nil {
		t.Fatalf("koneksi ke DB hidup harus berhasil: %v", err)
	}
	var n int
	if err := p1.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("query target: n=%d err=%v", n, err)
	}
	p2, _ := mgr.Pool(ctx, dbID)
	if p1 != p2 {
		t.Fatal("pool harus di-cache per database")
	}
	mgr.Invalidate(dbID)
	if p3, err := mgr.Pool(ctx, dbID); err != nil || p3 == p1 {
		t.Fatalf("Invalidate harus membuat pool baru: %v", err)
	}

	if _, err := mgr.Pool(ctx, 999); !errors.Is(err, dbconn.ErrNotFound) {
		t.Fatalf("koneksi tidak terdaftar: err = %v, want ErrNotFound", err)
	}
}

func TestPg_ManagerUnavailable(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	in := testutil.TargetInput(t, "mati", "tidak_ada")
	in.Port = 1
	in.Password = "rahasia-jangan-bocor"
	id, err := databases.NewService(pool, databases.NewPgRepo(), nil, testutil.SecretKey).Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	mgr := dbconn.NewManager(getter{pool}, testutil.SecretKey)
	t.Cleanup(mgr.CloseAll)

	_, err = mgr.Pool(context.Background(), id)
	if !errors.Is(err, dbconn.ErrUnavailable) {
		t.Fatalf("port mati: err = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), in.Password) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("error tidak boleh memuat password/DSN: %v", err)
	}

	err = mgr.TestConnection(context.Background(), dbconn.Connection{
		Host: in.Host, Port: 1, DBName: "x", Username: in.Username, SSLMode: "disable",
	}, in.Password)
	if !errors.Is(err, dbconn.ErrUnavailable) || strings.Contains(err.Error(), in.Password) {
		t.Fatalf("TestConnection port mati: %v", err)
	}
}

func TestPg_TestConnectionOK(t *testing.T) {
	targetName, _ := testutil.NewTargetDB(t, "")
	in := testutil.TargetInput(t, "ok", targetName)
	mgr := dbconn.NewManager(nil, testutil.SecretKey)
	err := mgr.TestConnection(context.Background(), dbconn.Connection{
		Host: in.Host, Port: in.Port, DBName: in.DBName, Username: in.Username, SSLMode: "disable",
	}, in.Password)
	if err != nil {
		t.Fatalf("TestConnection ke DB hidup: %v", err)
	}
}

func TestPg_GrantRepo(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	t1, _ := testutil.NewTargetDB(t, "")
	t2, _ := testutil.NewTargetDB(t, "")
	db1 := testutil.CreateConnection(t, pool, t1)
	db2 := testutil.CreateConnection(t, pool, t2)
	bob := testutil.CreateUser(t, pool, "bob", false)
	carol := testutil.CreateUser(t, pool, "carol", false)
	viewers := testutil.CreateGroup(t, pool, "viewers")
	editors := testutil.CreateGroup(t, pool, "editors")
	testutil.AddMember(t, pool, viewers, bob)
	testutil.AddMember(t, pool, editors, bob)
	testutil.Grant(t, pool, viewers, db1, "view")
	testutil.Grant(t, pool, editors, db1, "edit")
	testutil.Grant(t, pool, viewers, db2, "view")

	repo := dbconn.NewGrantRepo(pool)
	levels, err := repo.LevelsForUser(ctx, bob, db1)
	if err != nil || len(levels) != 2 {
		t.Fatalf("LevelsForUser = %v, %v, want 2 level", levels, err)
	}
	if l, _ := repo.LevelsForUser(ctx, carol, db1); len(l) != 0 {
		t.Fatalf("user tanpa group: %v", l)
	}

	dbs, err := repo.AccessibleDatabases(ctx, bob)
	if err != nil || len(dbs) != 2 {
		t.Fatalf("AccessibleDatabases = %+v, %v", dbs, err)
	}
	got := map[int64]authz.Level{}
	for _, d := range dbs {
		got[d.DatabaseID] = d.Level
	}
	if got[db1] != authz.LevelEdit || got[db2] != authz.LevelView {
		t.Fatalf("akses efektif = %v, want db1=edit (max), db2=view", got)
	}
	if dbs, _ := repo.AccessibleDatabases(ctx, carol); len(dbs) != 0 {
		t.Fatalf("carol tanpa group harus kosong: %+v", dbs)
	}
}
