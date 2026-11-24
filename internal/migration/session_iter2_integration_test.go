//go:build integration

package migration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

func TestIter2_FailedStatementRollsBackEarlierDDL(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Dua", "CREATE TABLE keep_me (id int); INSERT INTO tidak_ada VALUES (1)", "DROP TABLE keep_me", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	err := executor(t, fx).Run(context.Background(), jobOf(exec.ID, fx.dbID))
	if isHalt(err, worker.DispositionSuccess) {
		t.Fatal("statement kedua gagal tetapi execution sukses")
	}
	if tableExists(t, target, "keep_me") {
		t.Fatal("CREATE TABLE harus ikut hilang saat statement berikutnya gagal")
	}
}

func TestIter2_RollbackUsesExecutedRevisionNotTheNewerOne(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Revisi", "CREATE TABLE ran (id int)", "DROP TABLE ran", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	e := executor(t, fx)
	if err := e.Run(context.Background(), jobOf(exec.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	if _, err := fx.platform.Exec(context.Background(), `UPDATE executions SET status = 'rejected' WHERE migration_id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.srv.Service.Update(context.Background(), fx.editor, fx.dbID, m.ID, migration.Input{
		Title: "Revisi", UpSQL: "CREATE TABLE ran (id int)", DownSQL: "DROP TABLE tabel_salah",
	}); err != nil {
		t.Fatal(err)
	}
	// Kembalikan execution lama ke sukses supaya rollback memakai revisi yang benar-benar jalan.
	setExecStatus(t, fx, exec.ID, migration.StatusSuccess)
	rb, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve(t, fx, rb.ID)
	setExecStatus(t, fx, rb.ID, migration.StatusQueued)
	if err := e.Run(context.Background(), jobOf(rb.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatalf("rollback revisi lama = %v", err)
	}
	if tableExists(t, target, "ran") {
		t.Fatal("SQL down revisi yang dieksekusi tidak jalan")
	}
}

func TestIter2_EnqueueBlockedByPartialSibling(t *testing.T) {
	fx := newFixture(t, "edit")
	first := create(t, fx, "Tahan", "SELECT 1", "SELECT 1", false, "")
	second := create(t, fx, "Menyusul", "SELECT 2", "SELECT 2", false, "")
	a := submit(t, fx, first.ID)
	b := submit(t, fx, second.ID)
	setExecStatus(t, fx, a.ID, migration.StatusPartial)
	approve(t, fx, b.ID)
	_, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, b.ID, nil)
	if !errors.Is(err, migration.ErrBlocked) {
		t.Fatalf("enqueue di samping partial = %v", err)
	}
}

func TestIter2_DuplicateEnqueueDoesNotResetRetry(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Ulang", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), "w")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if err := q.Requeue(context.Background(), job.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	stored, err := q.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Attempts != job.Attempts {
		t.Fatalf("attempts %d direset menjadi %d", job.Attempts, stored.Attempts)
	}
	if !stored.RunAfter.After(time.Now().Add(30 * time.Minute)) {
		t.Fatalf("jeda retry ditarik menjadi %s", stored.RunAfter)
	}
}

func TestIter2_CancelWhileWorkerHoldsJob(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Jalan", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.New(fx.platform).Claim(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	if err := fx.srv.Service.Cancel(context.Background(), fx.editor, exec.ID); !errors.Is(err, migration.ErrJobRunning) {
		t.Fatalf("batal saat berjalan = %v", err)
	}
}
