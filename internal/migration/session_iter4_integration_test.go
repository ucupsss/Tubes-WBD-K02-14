//go:build integration

package migration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/lock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

const deadlockSQL = `DO $$ BEGIN RAISE EXCEPTION 'deadlock detected' USING ERRCODE = '40P01'; END $$`

func TestIter4_DeadlockIsAFailedAttemptNotAnAmbiguousCommit(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Kunci", "CREATE TABLE survived (id int); "+deadlockSQL, "DROP TABLE IF EXISTS survived", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	jobID, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, map[string]string{"correlation_id": "deadlock-1"})
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-deadlock")
	if err != nil || job == nil || job.ID != jobID || job.Attempts != 1 {
		t.Fatalf("claim = %+v %v", job, err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-deadlock", *job); err != nil {
		t.Fatalf("process = %v", err)
	}
	if tableExists(t, target, "survived") {
		t.Fatal("CREATE TABLE sebelum deadlock harus ikut di-rollback")
	}
	held, err := lock.New(fx.platform).Held(context.Background(), fx.dbID, exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("deadlock sudah pasti di-rollback, kunci database tidak boleh ditahan")
	}
	var execStatus, jobStatus, sqlstate, sqlText string
	var attempts int
	var runAfter time.Time
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&execStatus); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT status, attempts, run_after FROM migration_jobs WHERE id = $1`, job.ID).Scan(&jobStatus, &attempts, &runAfter); err != nil {
		t.Fatal(err)
	}
	if execStatus != migration.StatusQueued || jobStatus != queue.StatusQueued || attempts != 1 {
		t.Fatalf("exec=%s job=%s attempts=%d", execStatus, jobStatus, attempts)
	}
	if !runAfter.After(time.Now().Add(2 * time.Second)) {
		t.Fatalf("run_after = %s, jeda retry hilang", runAfter)
	}
	var n int
	if err := fx.platform.QueryRow(context.Background(), `
		SELECT count(*), COALESCE(max(sqlstate), ''), COALESCE(string_agg(sql_executed, ' '), '')
		FROM execution_attempts WHERE execution_id = $1`, exec.ID).Scan(&n, &sqlstate, &sqlText); err != nil {
		t.Fatal(err)
	}
	if n != 1 || sqlstate != "40P01" || !strings.Contains(sqlText, "40P01") {
		t.Fatalf("attempts=%d sqlstate=%s sql=%q", n, sqlstate, sqlText)
	}
}

func TestIter4_ThirdDeadlockClosesTheMigration(t *testing.T) {
	fx, _ := targetFixture(t)
	m := create(t, fx, "Habis", deadlockSQL, "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.platform.Exec(context.Background(), `UPDATE migration_jobs SET attempts = 2 WHERE execution_id = $1`, exec.ID); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-last")
	if err != nil || job == nil || job.Attempts != 3 {
		t.Fatalf("claim = %+v %v", job, err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-last", *job); err != nil {
		t.Fatalf("process = %v", err)
	}
	var execStatus, jobStatus string
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&execStatus); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM migration_jobs WHERE id = $1`, job.ID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if execStatus != migration.StatusFailed || jobStatus != queue.StatusDead {
		t.Fatalf("percobaan terakhir exec=%s job=%s", execStatus, jobStatus)
	}
	held, err := lock.New(fx.platform).Held(context.Background(), fx.dbID, exec.ID)
	if err != nil || held {
		t.Fatalf("kunci masih dipegang setelah gagal pasti: %v held=%v", err, held)
	}
}

func TestIter4_ConnectionLossStaysUncertain(t *testing.T) {
	fx, _ := targetFixture(t)
	m := create(t, fx, "Putus", `DO $$ BEGIN RAISE EXCEPTION 'connection failure' USING ERRCODE = '08006'; END $$`, "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-conn")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-conn", *job); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != migration.StatusUncertain {
		t.Fatalf("status = %s", status)
	}
	held, err := lock.New(fx.platform).Held(context.Background(), fx.dbID, exec.ID)
	if err != nil || !held {
		t.Fatalf("kunci harus ditahan saat hasil commit belum pasti: %v held=%v", err, held)
	}
}

func TestIter4_QueuedProofReconcilesWithoutRerunningDDL(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Sudah", "CREATE TABLE ran (id int)", "DROP TABLE ran", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	if err := executor(t, fx).Run(context.Background(), jobOf(exec.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-proof")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-proof", *job); err != nil {
		t.Fatal(err)
	}
	if !tableExists(t, target, "ran") {
		t.Fatal("tabel hilang")
	}
	var status string
	var version int32
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, m.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if status != migration.StatusSuccess || version != 1 {
		t.Fatalf("status=%s version=%d", status, version)
	}
	var proofs int
	if err := target.QueryRow(context.Background(), `SELECT count(*) FROM _dbmp.executions WHERE execution_id = $1`, exec.ID).Scan(&proofs); err != nil || proofs != 1 {
		t.Fatalf("bukti = %d %v", proofs, err)
	}
}

func TestIter4_RollbackThenNextMigrationReusesTheFreedVersion(t *testing.T) {
	fx, target := targetFixture(t)
	e := executor(t, fx)
	first := create(t, fx, "Satu", "CREATE TABLE satu (id int)", "DROP TABLE satu", false, "")
	second := create(t, fx, "Dua", "CREATE TABLE dua (id int)", "DROP TABLE dua", false, "")
	a := submit(t, fx, first.ID)
	approve(t, fx, a.ID)
	setExecStatus(t, fx, a.ID, migration.StatusQueued)
	if err := e.Run(context.Background(), jobOf(a.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	b := submit(t, fx, second.ID)
	approve(t, fx, b.ID)
	setExecStatus(t, fx, b.ID, migration.StatusQueued)
	if err := e.Run(context.Background(), jobOf(b.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	rb, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve(t, fx, rb.ID)
	setExecStatus(t, fx, rb.ID, migration.StatusQueued)
	if err := e.Run(context.Background(), jobOf(rb.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	third := create(t, fx, "Tiga", "CREATE TABLE tiga (id int)", "DROP TABLE tiga", false, "")
	c := submit(t, fx, third.ID)
	approve(t, fx, c.ID)
	setExecStatus(t, fx, c.ID, migration.StatusQueued)
	if err := e.Run(context.Background(), jobOf(c.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	var v1, v3 int32
	var v2 *int32
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, first.ID).Scan(&v1); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, second.ID).Scan(&v2); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT version FROM migrations WHERE id = $1`, third.ID).Scan(&v3); err != nil {
		t.Fatal(err)
	}
	if v1 != 1 || v2 != nil || v3 != 2 {
		t.Fatalf("rantai = %d, %v, %d", v1, v2, v3)
	}
	if !tableExists(t, target, "satu") || tableExists(t, target, "dua") || !tableExists(t, target, "tiga") {
		t.Fatal("isi target tidak mengikuti rantai")
	}
}
