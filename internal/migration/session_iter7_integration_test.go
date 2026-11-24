//go:build integration

package migration_test

import (
	"context"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/lock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

func TestIter7_FunctionBodyAndCommentDoNotHideTheNextStatement(t *testing.T) {
	fx, target := targetFixture(t)
	sqlText := `
		/* CREATE TABLE hidden (id int); */
		CREATE FUNCTION add_one(n int) RETURNS int AS $fn$
		BEGIN
		  RETURN n + 1;
		END
		$fn$ LANGUAGE plpgsql;
		CREATE TABLE kept (id int);
		INSERT INTO kept VALUES (1);`
	m := create(t, fx, "Fungsi", sqlText, "DROP TABLE kept; DROP FUNCTION add_one(int)", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	if err := executor(t, fx).Run(context.Background(), jobOf(exec.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	if tableExists(t, target, "hidden") {
		t.Fatal("tabel di dalam komentar ikut terbuat")
	}
	if !tableExists(t, target, "kept") {
		t.Fatal("statement sesudah function tidak jalan")
	}
	var n int
	if err := target.QueryRow(context.Background(), `SELECT add_one(id) FROM kept`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("add_one = %d %v", n, err)
	}
}

func TestIter7_BatchModeFailsClosed(t *testing.T) {
	fx, target := targetFixture(t)
	m, err := fx.srv.Service.Create(context.Background(), fx.editor, fx.dbID, migration.Input{
		Title: "Batch", ExecMode: migration.ModeBatch, BatchTable: "big_events", BatchColumn: "id", BatchSize: 1000,
		UpSQL:   "UPDATE big_events SET payload = payload + 1 WHERE id > :from AND id <= :to",
		DownSQL: "UPDATE big_events SET payload = payload - 1 WHERE id > :from AND id <= :to",
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-batch")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	ex := executor(t, fx)
	ex.SetBatchRunner(nil)
	if err := worker.Process(context.Background(), q, repo, ex, "worker-batch", *job); err != nil {
		t.Fatal(err)
	}
	var execStatus, jobStatus, code string
	if err := fx.platform.QueryRow(context.Background(), `SELECT status, COALESCE(error_code, '') FROM executions WHERE id = $1`, exec.ID).Scan(&execStatus, &code); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM migration_jobs WHERE id = $1`, job.ID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if execStatus != migration.StatusFailed || code != "batch_unavailable" || jobStatus != queue.StatusDead {
		t.Fatalf("exec=%s code=%s job=%s", execStatus, code, jobStatus)
	}
	if tableExists(t, target, "big_events") {
		t.Fatal("mode batch membuat objek di target")
	}
	held, err := lock.New(fx.platform).Held(context.Background(), fx.dbID, exec.ID)
	if err != nil || held {
		t.Fatalf("kunci tertahan: %v held=%v", err, held)
	}
}

func TestIter7_BatchModeEndToEndApplyAndRollback(t *testing.T) {
	fx, target := targetFixture(t)
	if _, err := target.Exec(context.Background(), `
		CREATE TABLE big_events (id BIGINT PRIMARY KEY, payload INT NOT NULL);
		INSERT INTO big_events (id, payload) SELECT g, 0 FROM generate_series(1, 25) AS g;
	`); err != nil {
		t.Fatal(err)
	}
	m, err := fx.srv.Service.Create(context.Background(), fx.editor, fx.dbID, migration.Input{
		Title: "Batch Update", ExecMode: migration.ModeBatch, BatchTable: "big_events", BatchColumn: "id", BatchSize: 10,
		UpSQL:   "UPDATE big_events SET payload = payload + 1 WHERE id > :from AND id <= :to",
		DownSQL: "UPDATE big_events SET payload = payload - 1 WHERE id > :from AND id <= :to",
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-batch-e2e")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-batch-e2e", *job); err != nil {
		t.Fatal(err)
	}

	got, _, err := fx.srv.Service.GetExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != migration.StatusSuccess || got.BatchesCommitted != 3 || got.RowsCommitted != 25 {
		t.Fatalf("batch apply = %+v", got)
	}
	batches, err := fx.srv.Service.ListBatches(context.Background(), exec.ID)
	if err != nil || len(batches) != 3 {
		t.Fatalf("batches mirror = %d %v", len(batches), err)
	}

	// Rollback batch dalam urutan terbalik
	rb, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve(t, fx, rb.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, rb.ID, nil); err != nil {
		t.Fatal(err)
	}
	rbJob, err := q.Claim(context.Background(), "worker-batch-e2e")
	if err != nil || rbJob == nil {
		t.Fatal(err)
	}
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-batch-e2e", *rbJob); err != nil {
		t.Fatal(err)
	}
	gotRB, _, err := fx.srv.Service.GetExecution(context.Background(), rb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRB.Status != migration.StatusSuccess || gotRB.BatchesCommitted != 3 || gotRB.RowsCommitted != 25 {
		t.Fatalf("batch rollback = %+v", gotRB)
	}
	var nonZero int
	if err := target.QueryRow(context.Background(), `SELECT count(*) FROM big_events WHERE payload <> 0`).Scan(&nonZero); err != nil || nonZero != 0 {
		t.Fatalf("nonZero setelah rollback batch = %d %v", nonZero, err)
	}
}

func TestIter7_OtherDatabaseStaysRunnable(t *testing.T) {
	fx := newFixture(t, "edit")
	other := testutil.CreateConnection(t, fx.platform, "target-lain")
	var groupID int64
	if err := fx.platform.QueryRow(context.Background(), `SELECT group_id FROM group_db_access WHERE database_id = $1`, fx.dbID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	testutil.Grant(t, fx.platform, groupID, other, "edit")
	busy := create(t, fx, "Sibuk", "SELECT 1", "SELECT 1", false, "")
	hold := submit(t, fx, busy.ID)
	setExecStatus(t, fx, hold.ID, migration.StatusUncertain)
	free, err := fx.srv.Service.Create(context.Background(), fx.editor, other, migration.Input{
		Title: "Bebas", UpSQL: "SELECT 1", DownSQL: "SELECT 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	exec, err := fx.srv.Service.Submit(context.Background(), fx.editor, other, free.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatalf("database lain ikut tertahan: %v", err)
	}
}

func TestIter7_TooManyConnectionsIsRetried(t *testing.T) {
	fx, _ := targetFixture(t)
	sqlText := `DO $$ BEGIN RAISE EXCEPTION 'too many connections' USING ERRCODE = '53300'; END $$`
	m := create(t, fx, "Penuh", sqlText, "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-full")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-full", *job); err != nil {
		t.Fatal(err)
	}
	var status, sqlstate, jobStatus string
	var runAfter time.Time
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT status, run_after FROM migration_jobs WHERE id = $1`, job.ID).Scan(&jobStatus, &runAfter); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT COALESCE(max(sqlstate), '') FROM execution_attempts WHERE execution_id = $1`, exec.ID).Scan(&sqlstate); err != nil {
		t.Fatal(err)
	}
	if status != migration.StatusQueued || jobStatus != queue.StatusQueued || sqlstate != "53300" || !runAfter.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("status=%s job=%s sqlstate=%s run_after=%s", status, jobStatus, sqlstate, runAfter.Format(time.RFC3339))
	}
	held, err := lock.New(fx.platform).Held(context.Background(), fx.dbID, exec.ID)
	if err != nil || held {
		t.Fatalf("53300 menahan kunci: %v held=%v", err, held)
	}
}
