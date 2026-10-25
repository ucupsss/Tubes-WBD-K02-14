//go:build integration

package explain_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explain"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

const seed = `
	CREATE TABLE orders (id SERIAL PRIMARY KEY, customer TEXT NOT NULL, note TEXT, total INT NOT NULL);
	INSERT INTO orders (customer, note, total)
	SELECT 'c' || (g % 500), 'n' || g, g % 1000 FROM generate_series(1, 20000) g;
	CREATE INDEX idx_orders_customer ON orders (customer);
	ANALYZE orders;`

type getter struct{ db platformdb.DBTX }

func (g getter) GetByID(ctx context.Context, id int64) (dbconn.Connection, error) {
	c, err := databases.NewPgRepo().GetByID(ctx, g.db, id)
	if errors.Is(err, databases.ErrNotFound) {
		return dbconn.Connection{}, dbconn.ErrNotFound
	}
	return c, err
}

func setup(t *testing.T) (*explain.Analyzer, int64, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	targetName, target := testutil.NewTargetDB(t, seed)
	dbID := testutil.CreateConnection(t, pool, targetName)
	mgr := dbconn.NewManager(getter{pool}, testutil.SecretKey)
	t.Cleanup(mgr.CloseAll)
	return explain.NewAnalyzer(mgr), dbID, target
}

func scanTypes(n explain.Node) []string {
	out := []string{n.NodeType}
	for _, c := range n.Children {
		out = append(out, scanTypes(c)...)
	}
	return out
}

func hasNode(p *explain.Plan, prefix string) bool {
	for _, t := range scanTypes(p.Root) {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

func TestPg_IndexVsSeqScan(t *testing.T) {
	a, dbID, _ := setup(t)
	r, err := a.Analyze(context.Background(), dbID,
		`SELECT * FROM orders WHERE customer = 'c42'; SELECT * FROM orders WHERE note = 'n42';`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Statements) != 2 || r.Failed != 0 {
		t.Fatalf("report = %+v", r)
	}
	indexed, plain := r.Statements[0].Plan, r.Statements[1].Plan
	if indexed == nil || !(hasNode(indexed, "Index Scan") || hasNode(indexed, "Bitmap Index Scan") || hasNode(indexed, "Index Only Scan")) {
		t.Fatalf("filter kolom ber-index harus memakai index, got %v", scanTypes(indexed.Root))
	}
	if plain == nil || !hasNode(plain, "Seq Scan") {
		t.Fatalf("filter tanpa index harus Seq Scan, got %v", scanTypes(plain.Root))
	}
	if indexed.Analyzed || indexed.Root.TotalCost <= 0 {
		t.Fatalf("tanpa ANALYZE: analyzed=%v cost=%v", indexed.Analyzed, indexed.Root.TotalCost)
	}
}

func TestPg_DDLThenInsertIsRolledBack(t *testing.T) {
	a, dbID, target := setup(t)
	ctx := context.Background()
	r, err := a.Analyze(ctx, dbID, `
		CREATE TABLE t (id INT PRIMARY KEY, name TEXT);
		INSERT INTO t VALUES (1, 'a'), (2, 'b');
		SELECT * FROM t WHERE id = 1;`, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Failed != 0 || len(r.Statements) != 3 {
		t.Fatalf("report = %+v", r)
	}
	ddl, ins, sel := r.Statements[0], r.Statements[1], r.Statements[2]
	if ddl.Plan != nil || ddl.Note == "" || ddl.Kind != "ddl" {
		t.Fatalf("DDL tidak punya plan dan harus diberi catatan: %+v", ddl)
	}
	if ins.Plan == nil || ins.Plan.Root.NodeType != "ModifyTable" || ins.Plan.Root.Operation != "Insert" || !ins.Plan.Analyzed {
		t.Fatalf("INSERT harus berhasil di-EXPLAIN ANALYZE: %+v", ins)
	}
	if ins.Plan.Root.ActualLoops == 0 || ins.Plan.ExecutionMS <= 0 {
		t.Fatalf("ANALYZE harus berisi angka aktual: %+v", ins.Plan.Root)
	}
	if sel.Plan == nil || sel.Plan.Root.ActualRows != 1 {
		t.Fatalf("SELECT setelah INSERT (dalam transaction yang sama) harus melihat 1 baris: %+v", sel.Plan)
	}

	var exists bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('public.t') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("table t tidak boleh ada setelah analisis (harus di-rollback)")
	}
}

func TestPg_AnalyzeDoesNotPersistDML(t *testing.T) {
	a, dbID, target := setup(t)
	ctx := context.Background()
	r, err := a.Analyze(ctx, dbID, `DELETE FROM orders WHERE total < 500; UPDATE orders SET total = 0;`, true)
	if err != nil || r.Failed != 0 {
		t.Fatalf("err=%v report=%+v", err, r)
	}
	if r.Statements[0].Plan.Root.ActualLoops == 0 {
		t.Fatal("DELETE harus benar-benar dieksekusi oleh ANALYZE")
	}
	var n, zero int
	if err := target.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE total = 0) FROM orders`).Scan(&n, &zero); err != nil {
		t.Fatal(err)
	}
	if n != 20000 || zero != 20 {
		t.Fatalf("data target berubah: count=%d total0=%d, want 20000 & 20", n, zero)
	}
}

func TestPg_ErrorPerStatement(t *testing.T) {
	a, dbID, target := setup(t)
	ctx := context.Background()
	r, err := a.Analyze(ctx, dbID, `
		CREATE TABLE ok_table (id INT);
		INSERT INTO tidak_ada VALUES (1);
		SELECT * FROM ok_table;`, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Failed != 1 || len(r.Statements) != 3 {
		t.Fatalf("report = %+v", r)
	}
	if !strings.Contains(r.Statements[1].Error, "tidak_ada") {
		t.Fatalf("error statement 2 harus ditampilkan: %q", r.Statements[1].Error)
	}
	if r.Statements[2].Plan == nil || r.Statements[2].Error != "" {
		t.Fatalf("statement setelah error tetap dianalisis: %+v", r.Statements[2])
	}
	var exists bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('public.ok_table') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("ok_table tidak boleh tersisa (exists=%v err=%v)", exists, err)
	}
}

func TestPg_RejectedAndUnavailable(t *testing.T) {
	a, dbID, _ := setup(t)
	ctx := context.Background()
	for _, sql := range []string{"COMMIT", "BEGIN; SELECT 1", "VACUUM orders", "CREATE DATABASE x"} {
		if _, err := a.Analyze(ctx, dbID, sql, true); err == nil {
			t.Fatalf("%q harus ditolak (D13)", sql)
		}
	}
	// Literal berisi $1 dan dollar-quote dikirim apa adanya.
	r, err := a.Analyze(ctx, dbID, `SELECT '$1' AS a, $$x;y$$ AS b`, false)
	if err != nil || r.Failed != 0 || len(r.Statements) != 1 {
		t.Fatalf("literal $1 / dollar-quote: err=%v report=%+v", err, r)
	}

	pool := testutil.NewPlatformDB(t)
	in := testutil.TargetInput(t, "mati", "x")
	in.Port = 1
	id, err := databases.NewService(pool, databases.NewPgRepo(), nil, testutil.SecretKey).Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	mgr := dbconn.NewManager(getter{pool}, testutil.SecretKey)
	t.Cleanup(mgr.CloseAll)
	if _, err := explain.NewAnalyzer(mgr).Analyze(ctx, id, "SELECT 1", false); apperr.KindOf(err) != apperr.KindUnavailable {
		t.Fatalf("target mati: err = %v, want KindUnavailable", err)
	}
}
