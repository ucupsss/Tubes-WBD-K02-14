//go:build integration

package migration_test

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

func TestEnqueue_DuplicateIsOneJobAndSurvivesRestart(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Antre", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusApproved)

	first, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, map[string]string{"correlation_id": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("job = %d lalu %d", first, second)
	}
	var n int
	if err := fx.platform.QueryRow(context.Background(), `SELECT count(*) FROM migration_jobs WHERE execution_id = $1`, exec.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("jobs = %d", n)
	}

	restarted := queue.New(fx.platform)
	job, err := restarted.Claim(context.Background(), "worker-baru")
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.ID != first || job.Attempts != 1 {
		t.Fatalf("claim setelah restart = %+v", job)
	}
}

func TestCancel_QueuedJobIsNotClaimed(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Batal", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusApproved)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := fx.srv.Service.Cancel(context.Background(), fx.editor, exec.ID); err != nil {
		t.Fatal(err)
	}
	job, err := queue.New(fx.platform).Claim(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Fatalf("job terbatal tetap di-claim: %+v", job)
	}
	status, _, err := fx.srv.Service.GetExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != migration.StatusCancelled {
		t.Fatalf("status = %s", status.Status)
	}
}

func TestResume_DoesNotOpenApprovalRound(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Lanjut", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	var before int
	if err := fx.platform.QueryRow(context.Background(), `SELECT count(*) FROM execution_approvals WHERE execution_id = $1`, exec.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	setExecStatus(t, fx, exec.ID, migration.StatusPartial)
	if _, err := fx.srv.Service.Resume(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := fx.platform.QueryRow(context.Background(), `SELECT count(*) FROM execution_approvals WHERE execution_id = $1`, exec.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("approval %d menjadi %d", before, after)
	}
	got, _, err := fx.srv.Service.GetExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != migration.StatusQueued {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestWorker_UnavailableMarksFailedWithoutRetry(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Stub", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusApproved)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "w")
	if err != nil || job == nil {
		t.Fatalf("claim = %+v %v", job, err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, worker.Unavailable{}, "w", *job); err != nil {
		t.Fatal(err)
	}
	got, _, err := fx.srv.Service.GetExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != migration.StatusFailed || got.ErrorMessage == nil || *got.ErrorMessage == "" {
		t.Fatalf("execution = %+v", got)
	}
	stored, err := q.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != queue.StatusDead {
		t.Fatalf("job status = %s", stored.Status)
	}
}

func setExecStatus(t *testing.T, fx fixture, execID int64, status string) {
	t.Helper()
	if _, err := fx.platform.Exec(context.Background(), `UPDATE executions SET status = $2 WHERE id = $1`, execID, status); err != nil {
		t.Fatal(err)
	}
}
