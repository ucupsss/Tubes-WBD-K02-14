//go:build integration

package migration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

func TestIter5_PlatformSuccessWriteIsAtomicAndRetried(t *testing.T) {
	fx, target := targetFixture(t)
	if _, err := fx.platform.Exec(context.Background(), `
		CREATE FUNCTION fail_exec_success() RETURNS trigger AS $$
		BEGIN
		  IF NEW.status = 'success' AND OLD.status IS DISTINCT FROM 'success' THEN
		    RAISE EXCEPTION 'platform write failed';
		  END IF;
		  RETURN NEW;
		END $$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_exec_success BEFORE UPDATE ON executions
		FOR EACH ROW EXECUTE FUNCTION fail_exec_success()`); err != nil {
		t.Fatal(err)
	}
	m := create(t, fx, "Catat", "CREATE TABLE tercatat (id int)", "DROP TABLE tercatat", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-platform")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-platform", *job); err != nil {
		t.Fatalf("process = %v", err)
	}
	if !tableExists(t, target, "tercatat") {
		t.Fatal("commit target harus tetap ada")
	}
	var version *int32
	var execStatus, jobStatus string
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, m.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&execStatus); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM migration_jobs WHERE id = $1`, job.ID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if version != nil || execStatus == migration.StatusFailed || jobStatus == queue.StatusDead {
		t.Fatalf("catatan platform terbelah: version=%v exec=%s job=%s", version, execStatus, jobStatus)
	}
	if _, err := fx.platform.Exec(context.Background(), `DROP TRIGGER fail_exec_success ON executions`); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.platform.Exec(context.Background(), `UPDATE migration_jobs SET run_after = now() WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	again, err := q.Claim(context.Background(), "worker-platform-2")
	if err != nil || again == nil {
		t.Fatalf("claim ulang = %+v %v", again, err)
	}
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-platform-2", *again); err != nil {
		t.Fatal(err)
	}
	var versionN int32
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&execStatus); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, m.ID).Scan(&versionN); err != nil {
		t.Fatal(err)
	}
	if execStatus != migration.StatusSuccess || versionN != 1 {
		t.Fatalf("pemulihan status=%s version=%d", execStatus, versionN)
	}
	var proofs int
	if err := target.QueryRow(context.Background(), `SELECT count(*) FROM _dbmp.executions WHERE execution_id = $1`, exec.ID).Scan(&proofs); err != nil || proofs != 1 {
		t.Fatalf("bukti = %d %v", proofs, err)
	}
}

func TestIter5_RollbackFollowsTheRevisionThatActuallyRan(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Bisa", "CREATE TABLE ran (id int)", "DROP TABLE ran", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	if err := executor(t, fx).Run(context.Background(), jobOf(exec.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	if _, err := fx.srv.Service.Update(context.Background(), fx.editor, fx.dbID, m.ID, migration.Input{
		Title: "Bisa", UpSQL: "CREATE TABLE ran (id int)", Irreversible: true, IrreversibleReason: "arsip tidak boleh dihapus",
	}); err != nil {
		t.Fatal(err)
	}
	rb, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, m.ID)
	if err != nil {
		t.Fatalf("rollback revisi yang sempat jalan = %v", err)
	}
	approve(t, fx, rb.ID)
	setExecStatus(t, fx, rb.ID, migration.StatusQueued)
	if err := executor(t, fx).Run(context.Background(), jobOf(rb.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	if tableExists(t, target, "ran") {
		t.Fatal("SQL down revisi yang dieksekusi tidak jalan")
	}

	stuck := create(t, fx, "Tetap", "CREATE TABLE tetap (id int)", "DROP TABLE tetap", true, "tidak boleh dihapus")
	applied := submit(t, fx, stuck.ID)
	approve(t, fx, applied.ID)
	setExecStatus(t, fx, applied.ID, migration.StatusQueued)
	if err := executor(t, fx).Run(context.Background(), jobOf(applied.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	if _, err := fx.srv.Service.Update(context.Background(), fx.editor, fx.dbID, stuck.ID, migration.Input{
		Title: "Tetap", UpSQL: "CREATE TABLE tetap (id int)", DownSQL: "DROP TABLE tetap",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, stuck.ID); !errors.Is(err, migration.ErrIrreversible) {
		t.Fatalf("draft baru tidak boleh membuka rollback untuk SQL yang memang tidak bisa di-undo: %v", err)
	}
	if !tableExists(t, target, "tetap") {
		t.Fatal("tabel irreversible ikut terhapus")
	}
}

func TestIter5_SerializationFailureOnSecondAttemptWaitsLonger(t *testing.T) {
	fx, _ := targetFixture(t)
	sqlText := `DO $$ BEGIN RAISE EXCEPTION 'could not serialize access' USING ERRCODE = '40001'; END $$`
	m := create(t, fx, "Serial", sqlText, "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.platform.Exec(context.Background(), `UPDATE migration_jobs SET attempts = 1 WHERE execution_id = $1`, exec.ID); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-serial")
	if err != nil || job == nil || job.Attempts != 2 {
		t.Fatalf("claim = %+v %v", job, err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-serial", *job); err != nil {
		t.Fatal(err)
	}
	var status, sqlstate string
	var runAfter time.Time
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT run_after FROM migration_jobs WHERE id = $1`, job.ID).Scan(&runAfter); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT COALESCE(max(sqlstate), '') FROM execution_attempts WHERE execution_id = $1`, exec.ID).Scan(&sqlstate); err != nil {
		t.Fatal(err)
	}
	if status != migration.StatusQueued || sqlstate != "40001" || !runAfter.After(time.Now().Add(10*time.Second)) {
		t.Fatalf("status=%s sqlstate=%s run_after=%s", status, sqlstate, runAfter.Format(time.RFC3339))
	}
	if strings.Contains(sqlstate, "uncertain") {
		t.Fatal("serialization failure tercatat sebagai commit yang tidak pasti")
	}
}
