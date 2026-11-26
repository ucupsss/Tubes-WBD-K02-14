package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
)

func TestIter1_TerminalExecutionDoesNotRunSQL(t *testing.T) {
	for _, status := range []string{"success", "failed", "rejected", "pending_review", "approved", "cancelled"} {
		jobs := &fakeJobs{}
		exec := &fakeExec{status: status}
		runner := &countingRunner{err: &queue.SQLError{SQLState: "42601", Message: "sintaks"}}
		if err := Process(context.Background(), jobs, exec, runner, "w", sampleJob(1)); err != nil {
			t.Fatal(status, err)
		}
		if runner.calls != 0 {
			t.Fatalf("%s menjalankan SQL %d kali", status, runner.calls)
		}
		if jobs.completed != queue.StatusDone || exec.final == "failed" {
			t.Fatalf("%s job=%s exec=%s", status, jobs.completed, exec.final)
		}
	}
}

func TestIter1_PartialMustNotBeReexecuted(t *testing.T) {
	for _, status := range []string{"partial", "partially_reverted"} {
		jobs := &fakeJobs{}
		exec := &fakeExec{status: status}
		runner := &countingRunner{}
		if err := Process(context.Background(), jobs, exec, runner, "w", sampleJob(2)); err != nil {
			t.Fatal(err)
		}
		if runner.calls != 0 {
			t.Fatalf("%s dijalankan ulang", status)
		}
		if exec.final == "failed" || exec.final == "success" {
			t.Fatalf("%s status execution berubah menjadi %s", status, exec.final)
		}
	}
}

func TestIter1_WrappedTransientThenSyntaxOnLastAttempt(t *testing.T) {
	jobs := &fakeJobs{}
	exec := &fakeExec{status: "queued"}
	wrapped := fmt.Errorf("target: %w", &queue.SQLError{SQLState: "08P01", Message: "putus"})
	if err := Process(context.Background(), jobs, exec, &countingRunner{err: wrapped}, "w", sampleJob(2)); err != nil {
		t.Fatal(err)
	}
	if !jobs.requeued || jobs.wait != 15*time.Second || exec.final != "queued" {
		t.Fatalf("retry kelas 08: requeued=%v wait=%s status=%s", jobs.requeued, jobs.wait, exec.final)
	}
	jobs.requeued = false
	exec.status = "queued"
	syntax := fmt.Errorf("jalankan: %w", &queue.SQLError{SQLState: "42601", Message: "sintaks"})
	if err := Process(context.Background(), jobs, exec, &countingRunner{err: syntax}, "w", sampleJob(3)); err != nil {
		t.Fatal(err)
	}
	if jobs.requeued || jobs.completed != queue.StatusDead || exec.final != "failed" {
		t.Fatalf("sintaks di attempt terakhir: requeued=%v job=%s exec=%s", jobs.requeued, jobs.completed, exec.final)
	}
}

func TestIter1_UncertainRequeuesWithoutMarkingFailed(t *testing.T) {
	jobs := &fakeJobs{}
	exec := &fakeExec{status: "running"}
	halt := &Halt{Disposition: DispositionUncertain, Message: "commit tidak pasti", AttemptStatus: "uncertain"}
	if err := Process(context.Background(), jobs, exec, &countingRunner{err: fmt.Errorf("bungkus: %w", halt)}, "w", sampleJob(1)); err != nil {
		t.Fatal(err)
	}
	if exec.final == "failed" || exec.final != "uncertain" || !jobs.requeued || jobs.wait != 15*time.Second {
		t.Fatalf("final=%s requeued=%v wait=%s", exec.final, jobs.requeued, jobs.wait)
	}
	if len(exec.attempts) != 1 {
		t.Fatalf("attempt tercatat = %d", len(exec.attempts))
	}
}

func TestIter1_LockBusyReturnsToQueue(t *testing.T) {
	jobs := &restoringJobs{}
	exec := &fakeExec{status: "running"}
	halt := &Halt{Disposition: DispositionRetry, Message: "database sibuk", Recorded: true}
	if err := Process(context.Background(), jobs, exec, &countingRunner{err: halt}, "w", sampleJob(2)); err != nil {
		t.Fatal(err)
	}
	if exec.final != "queued" || !jobs.requeued || !jobs.restored || jobs.completed == queue.StatusDead {
		t.Fatalf("final=%s requeued=%v restored=%v completed=%s", exec.final, jobs.requeued, jobs.restored, jobs.completed)
	}
}

func TestIter1_Class08RetriesButNearbyCodesDoNot(t *testing.T) {
	if !queue.Retryable("  08p01 ") || !queue.Retryable("08006") || !queue.Retryable("53300") {
		t.Fatal("kode sementara tidak dikenali")
	}
	// 55P03 (lock_timeout) sengaja di-retry sejak uji worker crash: lihat D35.
	for _, code := range []string{"57P02", "53400", "40000", "42601", "55P02", "0800"} {
		if code != "0800" && queue.Retryable(code) {
			t.Fatalf("%s tidak boleh di-retry", code)
		}
	}
	if !queue.Retryable("0800") {
		t.Fatal("kelas 08 harus di-retry")
	}
}

type countingRunner struct {
	err   error
	calls int
}

func (c *countingRunner) Run(context.Context, queue.Job) error {
	c.calls++
	return c.err
}

type restoringJobs struct {
	fakeJobs
	restored bool
}

func (r *restoringJobs) RestoreAttempt(context.Context, int64) error {
	r.restored = true
	return nil
}

func TestIter2_RecoveryStatusesStillRun(t *testing.T) {
	for _, status := range []string{"running", "uncertain"} {
		runner := &countingRunner{}
		exec := &fakeExec{status: status}
		if err := Process(context.Background(), &fakeJobs{}, exec, runner, "w", sampleJob(1)); err != nil {
			t.Fatal(status, err)
		}
		if runner.calls != 1 {
			t.Fatalf("%s tidak diperiksa ulang, calls=%d", status, runner.calls)
		}
	}
}

func TestIter1_PlainErrorIsNotRetried(t *testing.T) {
	jobs := &fakeJobs{}
	exec := &fakeExec{status: "queued"}
	err := Process(context.Background(), jobs, exec, &countingRunner{err: errors.New("koneksi reset tanpa sqlstate")}, "w", sampleJob(1))
	if err != nil {
		t.Fatal(err)
	}
	if jobs.requeued || exec.final != "failed" || jobs.completed != queue.StatusDead {
		t.Fatalf("requeued=%v final=%s job=%s", jobs.requeued, exec.final, jobs.completed)
	}
}
