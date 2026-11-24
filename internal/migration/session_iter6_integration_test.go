//go:build integration

package migration_test

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

func TestIter6_ResumeDoesNotReuseAttemptNumbers(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Lanjut", "CREATE TABLE lanjut (id int)", "DROP TABLE lanjut", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusPartial)
	if _, err := fx.platform.Exec(context.Background(), `
		INSERT INTO execution_attempts (
			execution_id, attempt_no, worker_id, status, sql_executed, sqlstate, error_message, started_at, finished_at)
		VALUES ($1, 1, 'worker-batch', 'failed', 'UPDATE big_events SET payload = payload + 1', '57014', 'timeout batch pertama', now(), now())`, exec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.srv.Service.Resume(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-resume")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-resume", *job); err != nil {
		t.Fatal(err)
	}
	if !tableExists(t, target, "lanjut") {
		t.Fatal("resume tidak membuat tabel")
	}
	var successes, rows int
	if err := fx.platform.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE status = 'success'), count(*)
		FROM execution_attempts WHERE execution_id = $1`, exec.ID).Scan(&successes, &rows); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || rows != 2 {
		t.Fatalf("percobaan sukses=%d total=%d", successes, rows)
	}
	var status string
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != migration.StatusSuccess {
		t.Fatalf("status = %s", status)
	}
}

func TestIter6_CancelWhileRetryIsWaiting(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Batal", "CREATE TABLE batal (id int); "+deadlockSQL, "DROP TABLE IF EXISTS batal", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "worker-cancel")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), "worker-cancel", *job); err != nil {
		t.Fatal(err)
	}
	if err := fx.srv.Service.Cancel(context.Background(), fx.editor, exec.ID); err != nil {
		t.Fatalf("batal saat menunggu jeda = %v", err)
	}
	var execStatus, jobStatus string
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM executions WHERE id = $1`, exec.ID).Scan(&execStatus); err != nil {
		t.Fatal(err)
	}
	if err := fx.platform.QueryRow(context.Background(), `SELECT status FROM migration_jobs WHERE id = $1`, job.ID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if execStatus != migration.StatusCancelled || jobStatus != queue.StatusDone {
		t.Fatalf("exec=%s job=%s", execStatus, jobStatus)
	}
	next, err := q.Claim(context.Background(), "worker-cancel-2")
	if err != nil || next != nil {
		t.Fatalf("job yang dibatalkan diambil lagi: %+v %v", next, err)
	}
	if tableExists(t, target, "batal") {
		t.Fatal("DDL sebelum deadlock tetap ada")
	}
}
