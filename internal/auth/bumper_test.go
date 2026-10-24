package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
)

// execDB mencatat panggilan Exec; Query/QueryRow tidak dipakai bumper.
type execDB struct {
	sql  []string
	args [][]any
	err  error
}

func (d *execDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.sql = append(d.sql, sql)
	d.args = append(d.args, args)
	return pgconn.CommandTag{}, d.err
}

func (d *execDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, errors.New("not used")
}

func (d *execDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }

func TestPrivilegeBumper_BumpPrivilege(t *testing.T) {
	db := &execDB{}
	b := auth.NewPrivilegeBumper(db)

	if err := b.BumpPrivilege(context.Background()); err != nil || len(db.sql) != 0 {
		t.Fatalf("tanpa user: tidak boleh query, got err=%v calls=%d", err, len(db.sql))
	}

	if err := b.BumpPrivilege(context.Background(), 3, 5); err != nil {
		t.Fatal(err)
	}
	if len(db.sql) != 1 || !strings.Contains(db.sql[0], "privilege_version = privilege_version + 1") {
		t.Fatalf("query salah: %v", db.sql)
	}
	ids, ok := db.args[0][0].([]int64)
	if !ok || len(ids) != 2 || ids[0] != 3 || ids[1] != 5 {
		t.Fatalf("argumen salah: %#v", db.args[0])
	}
}

func TestPrivilegeBumper_BumpGroupMembers(t *testing.T) {
	db := &execDB{}
	if err := auth.NewPrivilegeBumper(db).BumpGroupMembers(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(db.sql) != 1 || !strings.Contains(db.sql[0], "group_members") || db.args[0][0] != int64(7) {
		t.Fatalf("query/argumen salah: %v %v", db.sql, db.args)
	}
}

func TestPrivilegeBumper_Error(t *testing.T) {
	db := &execDB{err: errDB}
	b := auth.NewPrivilegeBumper(db)
	if err := b.BumpPrivilege(context.Background(), 1); !errors.Is(err, errDB) {
		t.Fatalf("err = %v, want dibungkus errDB", err)
	}
	if err := b.BumpGroupMembers(context.Background(), 1); !errors.Is(err, errDB) {
		t.Fatalf("err = %v, want dibungkus errDB", err)
	}
}
