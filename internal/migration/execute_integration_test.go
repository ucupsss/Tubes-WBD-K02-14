//go:build integration

package migration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/lock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

func TestExecute_AppliesOnceAndSkipsWhenProofExists(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Buat", "CREATE TABLE ran (id int)", "DROP TABLE ran", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)

	e := executor(t, fx)
	if err := e.Run(context.Background(), jobOf(exec.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatalf("run = %v", err)
	}
	if !tableExists(t, target, "ran") {
		t.Fatal("tabel ran harus ada")
	}
	var version int32
	var proof int
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, m.ID).Scan(&version); err != nil || version != 1 {
		t.Fatalf("version = %d %v", version, err)
	}
	if err := target.QueryRow(context.Background(), `SELECT count(*) FROM _dbmp.executions WHERE execution_id = $1`, exec.ID).Scan(&proof); err != nil || proof != 1 {
		t.Fatalf("bukti = %d %v", proof, err)
	}

	setExecStatus(t, fx, exec.ID, migration.StatusRunning)
	if _, err := fx.platform.Exec(context.Background(), `UPDATE migrations SET version = NULL WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), jobOf(exec.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatalf("run kedua = %v", err)
	}
	if !tableExists(t, target, "ran") {
		t.Fatal("bukti harus mencegah SQL dijalankan lagi")
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, m.ID).Scan(&version); err != nil || version != 1 {
		t.Fatalf("version kedua = %d", version)
	}
}

func TestExecute_RollbackClearsVersion(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Buat", "CREATE TABLE ran (id int)", "DROP TABLE ran", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	e := executor(t, fx)
	if err := e.Run(context.Background(), jobOf(exec.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	rb, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve(t, fx, rb.ID)
	setExecStatus(t, fx, rb.ID, migration.StatusQueued)
	if err := e.Run(context.Background(), jobOf(rb.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatalf("rollback = %v", err)
	}
	if tableExists(t, target, "ran") {
		t.Fatal("rollback harus menghapus tabel")
	}
	var version *int32
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, m.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != nil {
		t.Fatalf("version = %d, want null", *version)
	}
}

func TestExecute_HashMismatchDoesNotRun(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Ubah", "CREATE TABLE secret (id int)", "DROP TABLE secret", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.platform.Exec(context.Background(), `
		UPDATE migration_revisions SET up_sql = up_sql || ' '
		WHERE id = (SELECT revision_id FROM executions WHERE id = $1)`, exec.ID); err != nil {
		t.Fatal(err)
	}
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	err := executor(t, fx).Run(context.Background(), jobOf(exec.ID, fx.dbID))
	if !isHalt(err, worker.DispositionDead) {
		t.Fatalf("err = %v", err)
	}
	if tableExists(t, target, "secret") {
		t.Fatal("SQL yang hash-nya berubah tidak boleh dijalankan")
	}
	var code *string
	if err := fx.platform.QueryRow(context.Background(), `SELECT error_code FROM executions WHERE id = $1`, exec.ID).Scan(&code); err != nil || code == nil || *code != "hash_mismatch" {
		t.Fatalf("code = %v", code)
	}
}

func TestExecute_StaleApprovalReturnsToReview(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Tunggu", "CREATE TABLE nope (id int)", "DROP TABLE nope", false, "")
	exec := submit(t, fx, m.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	err := executor(t, fx).Run(context.Background(), jobOf(exec.ID, fx.dbID))
	if !isHalt(err, worker.DispositionDone) {
		t.Fatalf("err = %v", err)
	}
	got, _, err := fx.srv.Service.GetExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != migration.StatusPendingReview || got.ApprovalRound < 2 {
		t.Fatalf("status = %s round = %d", got.Status, got.ApprovalRound)
	}
	if tableExists(t, target, "nope") {
		t.Fatal("approval tidak lengkap tidak boleh menjalankan SQL")
	}
}

func TestExecute_PartialBlocksNext(t *testing.T) {
	fx, target := targetFixture(t)
	first := create(t, fx, "Pertama", "SELECT 1", "SELECT 1", false, "")
	second := create(t, fx, "Kedua", "CREATE TABLE blocked (id int)", "DROP TABLE blocked", false, "")
	a := submit(t, fx, first.ID)
	b := submit(t, fx, second.ID)
	setExecStatus(t, fx, a.ID, migration.StatusPartial)
	approve(t, fx, b.ID)
	setExecStatus(t, fx, b.ID, migration.StatusQueued)
	err := executor(t, fx).Run(context.Background(), jobOf(b.ID, fx.dbID))
	if !isHalt(err, worker.DispositionDead) {
		t.Fatalf("err = %v", err)
	}
	var code *string
	if err := fx.platform.QueryRow(context.Background(), `SELECT error_code FROM executions WHERE id = $1`, b.ID).Scan(&code); err != nil || code == nil || *code != "blocked" {
		t.Fatalf("code = %v", code)
	}
	if tableExists(t, target, "blocked") {
		t.Fatal("execution partial harus menahan migration berikutnya")
	}
}

func TestExecute_UncertainKeepsLock(t *testing.T) {
	fx, _ := targetFixture(t)
	m := create(t, fx, "Ragu", "CREATE TABLE maybe (id int)", "DROP TABLE maybe", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	e := executor(t, fx)
	e.SetProof(func(context.Context, *pgxpool.Pool, int64) (bool, error) {
		return false, errors.New("koneksi putus")
	})
	err := e.Run(context.Background(), jobOf(exec.ID, fx.dbID))
	if !isHalt(err, worker.DispositionUncertain) {
		t.Fatalf("err = %v", err)
	}
	held, err := lock.New(fx.platform).Held(context.Background(), fx.dbID, exec.ID)
	if err != nil || !held {
		t.Fatalf("kunci harus tetap dipegang, held=%v err=%v", held, err)
	}
	var status string
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&status); err != nil || status != migration.StatusUncertain {
		t.Fatalf("status = %s %v", status, err)
	}
	_ = m
}

func TestClaim_StaleRunningJobCanBeTakenOver(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Lama", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusApproved)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	first, err := q.Claim(context.Background(), "worker-a")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	if _, err := fx.platform.Exec(context.Background(), `UPDATE migration_jobs SET claimed_at = now() - interval '2 minutes' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := q.Claim(context.Background(), "worker-b")
	if err != nil || second == nil || second.ID != first.ID || second.ClaimedBy != "worker-b" {
		t.Fatalf("pengambilalihan = %+v %v", second, err)
	}
	if second.Attempts != first.Attempts {
		t.Fatalf("attempts %d menjadi %d", first.Attempts, second.Attempts)
	}
}

func targetFixture(t *testing.T) (fixture, *pgxpool.Pool) {
	t.Helper()
	fx := newFixture(t, "edit")
	name, target := testutil.NewTargetDB(t, "")
	if _, err := fx.platform.Exec(context.Background(), `UPDATE db_connections SET db_name = $1 WHERE id = $2`, name, fx.dbID); err != nil {
		t.Fatal(err)
	}
	return fx, target
}

func approve(t *testing.T, fx fixture, execID int64) {
	t.Helper()
	if _, err := fx.platform.Exec(context.Background(), `UPDATE groups SET leader_id = $1 WHERE name = 'dev'`, fx.editor.UserID); err != nil {
		t.Fatal(err)
	}
	tag, err := fx.platform.Exec(context.Background(), `
		UPDATE execution_approvals
		SET decision = 'approved', leader_user_id = $2, decided_by = $2, decided_at = now()
		WHERE execution_id = $1 AND decision = 'pending'`, execID, fx.editor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatal("tidak ada baris approval")
	}
	setExecStatus(t, fx, execID, migration.StatusApproved)
}

func executor(t *testing.T, fx fixture) *migration.Executor {
	t.Helper()
	repo := migration.NewPgRepo().Bind(fx.platform)
	conns := dbconn.NewManager(connGetter{db: fx.platform, repo: databases.NewPgRepo()}, testutil.SecretKey)
	t.Cleanup(conns.CloseAll)
	approvals := approval.NewService(fx.platform, approval.PoolTx{Pool: fx.platform}, approval.NewPgRepo(), repo)
	return migration.NewExecutor(fx.platform, repo, conns, lock.New(fx.platform), migration.WrapApproval(approvals), authz.NewAuthorizer(dbconn.NewGrantRepo(fx.platform)))
}

func jobOf(execID, dbID int64) queue.Job {
	return queue.Job{ID: 1, ExecutionID: execID, DatabaseID: dbID, Attempts: 1, ClaimedBy: "worker-test", Status: queue.StatusRunning}
}

func isHalt(err error, disposition string) bool {
	var halt *worker.Halt
	return errors.As(err, &halt) && halt.Disposition == disposition
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1)`, name).Scan(&ok)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

type connGetter struct {
	db   platformdb.DBTX
	repo *databases.PgRepo
}

func (g connGetter) GetByID(ctx context.Context, id int64) (dbconn.Connection, error) {
	c, err := g.repo.GetByID(ctx, g.db, id)
	if err != nil {
		return dbconn.Connection{}, err
	}
	return c, nil
}
