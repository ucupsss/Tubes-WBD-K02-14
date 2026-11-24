package worker

import (
	"context"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
)

func TestProcess_SyntaxErrorIsNotRetried(t *testing.T) {
	jobs := &fakeJobs{}
	exec := &fakeExec{status: "queued"}
	err := Process(context.Background(), jobs, exec, failRunner("42601"), "w1", sampleJob(1))
	if err != nil {
		t.Fatal(err)
	}
	if jobs.requeued || jobs.completed != queue.StatusDead {
		t.Fatalf("requeued=%v status=%s", jobs.requeued, jobs.completed)
	}
	if exec.final != "failed" || len(exec.attempts) != 1 {
		t.Fatalf("exec=%+v attempts=%v", exec, exec.attempts)
	}
}

func TestProcess_TransientErrorRequeuesUntilThirdAttempt(t *testing.T) {
	jobs := &fakeJobs{}
	exec := &fakeExec{status: "queued"}
	runner := failRunner("08006")
	if err := Process(context.Background(), jobs, exec, runner, "w1", sampleJob(1)); err != nil {
		t.Fatal(err)
	}
	if !jobs.requeued || jobs.wait != 5*time.Second || exec.final != "queued" {
		t.Fatalf("attempt 1: requeued=%v wait=%s status=%s", jobs.requeued, jobs.wait, exec.final)
	}
	jobs.requeued = false
	if err := Process(context.Background(), jobs, exec, runner, "w1", sampleJob(2)); err != nil {
		t.Fatal(err)
	}
	if !jobs.requeued || jobs.wait != 15*time.Second {
		t.Fatalf("attempt 2: requeued=%v wait=%s", jobs.requeued, jobs.wait)
	}
	jobs.requeued = false
	if err := Process(context.Background(), jobs, exec, runner, "w1", sampleJob(3)); err != nil {
		t.Fatal(err)
	}
	if jobs.requeued || jobs.completed != queue.StatusDead || exec.final != "failed" {
		t.Fatalf("attempt 3: requeued=%v status=%s exec=%s", jobs.requeued, jobs.completed, exec.final)
	}
}

func TestProcess_CancelDoesNotRun(t *testing.T) {
	jobs := &fakeJobs{}
	exec := &fakeExec{status: "cancelled"}
	err := Process(context.Background(), jobs, exec, failRunner("42601"), "w1", sampleJob(1))
	if err != nil {
		t.Fatal(err)
	}
	if exec.ran || jobs.completed != queue.StatusDone {
		t.Fatalf("ran=%v status=%s", exec.ran, jobs.completed)
	}
}

type failRunner string

func (f failRunner) Run(context.Context, queue.Job) error {
	return &queue.SQLError{SQLState: string(f), Message: "gagal"}
}

func sampleJob(attempt int) queue.Job {
	return queue.Job{ID: 9, ExecutionID: 4, DatabaseID: 1, Attempts: attempt, Status: queue.StatusRunning}
}

type fakeJobs struct {
	requeued  bool
	wait      time.Duration
	completed string
}

func (f *fakeJobs) Claim(context.Context, string) (*queue.Job, error) { return nil, nil }
func (f *fakeJobs) Complete(_ context.Context, _ int64, status string) error {
	f.completed = status
	return nil
}
func (f *fakeJobs) Requeue(_ context.Context, _ int64, after time.Duration) error {
	f.requeued = true
	f.wait = after
	return nil
}

type fakeExec struct {
	status   string
	final    string
	ran      bool
	attempts []int
}

func (f *fakeExec) ExecutionStatus(context.Context, int64) (string, error) { return f.status, nil }
func (f *fakeExec) MarkRunning(context.Context, int64) (bool, error) {
	f.ran = true
	f.status = "running"
	f.final = "running"
	return true, nil
}
func (f *fakeExec) MarkQueued(context.Context, int64) error {
	f.final = "queued"
	f.status = "queued"
	return nil
}
func (f *fakeExec) MarkFailed(context.Context, int64, string) error {
	f.final = "failed"
	return nil
}
func (f *fakeExec) MarkSucceeded(context.Context, int64) error {
	f.final = "success"
	return nil
}
func (f *fakeExec) InsertAttempt(_ context.Context, _ int64, attemptNo int, _, _, _, _, _ string) error {
	f.attempts = append(f.attempts, attemptNo)
	return nil
}
func (f *fakeExec) MarkUncertain(context.Context, int64, string) error {
	f.final = "uncertain"
	return nil
}
