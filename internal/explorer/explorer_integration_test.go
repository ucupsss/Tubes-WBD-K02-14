//go:build integration

package explorer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explorer"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

const seed = `
	CREATE TABLE authors (id SERIAL PRIMARY KEY, name TEXT NOT NULL UNIQUE);
	CREATE TABLE books (
		id        SERIAL PRIMARY KEY,
		author_id INT NOT NULL REFERENCES authors(id),
		title     TEXT NOT NULL,
		pages     INT CHECK (pages > 0)
	);
	CREATE INDEX idx_books_title ON books (title);
	CREATE VIEW long_books AS SELECT * FROM books WHERE pages > 300;
	INSERT INTO authors (name) VALUES ('Pram');
	INSERT INTO books (author_id, title, pages) SELECT 1, 'Buku ' || g, g * 10 FROM generate_series(1, 25) g;`

type getter struct{ db platformdb.DBTX }

func (g getter) GetByID(ctx context.Context, id int64) (dbconn.Connection, error) {
	c, err := databases.NewPgRepo().GetByID(ctx, g.db, id)
	if errors.Is(err, databases.ErrNotFound) {
		return dbconn.Connection{}, dbconn.ErrNotFound
	}
	return c, err
}

func setup(t *testing.T) (*explorer.Service, int64) {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	targetName, _ := testutil.NewTargetDB(t, seed)
	dbID := testutil.CreateConnection(t, pool, targetName)
	mgr := dbconn.NewManager(getter{pool}, testutil.SecretKey)
	t.Cleanup(mgr.CloseAll)
	svc := explorer.NewService(mgr, authz.NewAuthorizer(dbconn.NewGrantRepo(pool)), explorer.NewPgRepo(), getter{pool})
	return svc, dbID
}

func TestPg_ListTables(t *testing.T) {
	svc, dbID := setup(t)
	tables, err := svc.ListTables(context.Background(), dbID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, tb := range tables {
		found[tb.Name] = tb.Type
	}
	if found["authors"] == "" || found["books"] == "" || found["long_books"] == "" {
		t.Fatalf("table/view tidak lengkap: %+v", tables)
	}
}

func TestPg_TableMetadata(t *testing.T) {
	svc, dbID := setup(t)
	md, err := svc.GetTableMetadata(context.Background(), dbID, "public", "books")
	if err != nil {
		t.Fatal(err)
	}

	cols := map[string]bool{}
	for _, c := range md.Columns {
		cols[c.Name] = true
	}
	for _, want := range []string{"id", "author_id", "title", "pages"} {
		if !cols[want] {
			t.Fatalf("kolom %q tidak ada: %+v", want, md.Columns)
		}
	}

	types := map[string]bool{}
	for _, c := range md.Constraints {
		types[c.Type] = true
	}
	for _, want := range []string{"PRIMARY KEY", "FOREIGN KEY", "CHECK"} {
		if !types[want] {
			t.Fatalf("constraint %q tidak ada: %+v", want, md.Constraints)
		}
	}

	idx := map[string]bool{}
	for _, i := range md.Indexes {
		idx[i.Name] = true
	}
	if !idx["idx_books_title"] || !idx["books_pkey"] {
		t.Fatalf("index tidak lengkap: %+v", md.Indexes)
	}
}

func TestPg_TableDataPagination(t *testing.T) {
	svc, dbID := setup(t)
	ctx := context.Background()

	p1, err := svc.GetTableData(ctx, dbID, "public", "books", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p1.TotalRows != 25 || p1.TotalPages != 3 || len(p1.Rows) != 10 || !p1.HasNext || p1.HasPrev {
		t.Fatalf("halaman 1 salah: total=%d pages=%d rows=%d next=%v prev=%v", p1.TotalRows, p1.TotalPages, len(p1.Rows), p1.HasNext, p1.HasPrev)
	}
	p3, err := svc.GetTableData(ctx, dbID, "public", "books", 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p3.Rows) != 5 || p3.HasNext || !p3.HasPrev {
		t.Fatalf("halaman terakhir salah: rows=%d next=%v", len(p3.Rows), p3.HasNext)
	}
	if p1.Rows[0][0] == p3.Rows[0][0] {
		t.Fatal("halaman berbeda harus berisi baris berbeda")
	}
}

func TestPg_TableNotFoundAndInjection(t *testing.T) {
	svc, dbID := setup(t)
	ctx := context.Background()
	for _, table := range []string{"tidak_ada", `books"; DROP TABLE books; --`, "books; DROP TABLE books"} {
		if _, err := svc.GetTableMetadata(ctx, dbID, "public", table); !errors.Is(err, explorer.ErrTableNotFound) {
			t.Fatalf("metadata %q: err = %v, want ErrTableNotFound", table, err)
		}
		if _, err := svc.GetTableData(ctx, dbID, "public", table, 1, 10); !errors.Is(err, explorer.ErrTableNotFound) {
			t.Fatalf("data %q: err = %v, want ErrTableNotFound", table, err)
		}
	}
	// Table books harus tetap utuh.
	if p, err := svc.GetTableData(ctx, dbID, "public", "books", 1, 10); err != nil || p.TotalRows != 25 {
		t.Fatalf("books harus utuh: %v", err)
	}
}

func TestPg_UnavailableTarget(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	in := testutil.TargetInput(t, "mati", "x")
	in.Port = 1
	id, err := databases.NewService(pool, databases.NewPgRepo(), nil, testutil.SecretKey).Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	mgr := dbconn.NewManager(getter{pool}, testutil.SecretKey)
	t.Cleanup(mgr.CloseAll)
	svc := explorer.NewService(mgr, authz.NewAuthorizer(dbconn.NewGrantRepo(pool)), explorer.NewPgRepo(), getter{pool})

	if _, err := svc.ListTables(context.Background(), id); apperr.KindOf(err) != apperr.KindUnavailable {
		t.Fatalf("target mati: err = %v, want KindUnavailable (502)", err)
	}
}

func TestPg_InternalSchemasRejected(t *testing.T) {
	svc, dbID := setup(t)
	ctx := context.Background()
	for _, pair := range [][2]string{
		{"pg_catalog", "pg_class"},
		{"information_schema", "tables"},
		{"_dbmp", "applied_migrations"},
	} {
		if _, err := svc.GetTableMetadata(ctx, dbID, pair[0], pair[1]); !errors.Is(err, explorer.ErrTableNotFound) {
			t.Fatalf("metadata %s.%s: err = %v, want ErrTableNotFound", pair[0], pair[1], err)
		}
		if _, err := svc.GetTableData(ctx, dbID, pair[0], pair[1], 1, 10); !errors.Is(err, explorer.ErrTableNotFound) {
			t.Fatalf("data %s.%s: err = %v, want ErrTableNotFound", pair[0], pair[1], err)
		}
	}
}
