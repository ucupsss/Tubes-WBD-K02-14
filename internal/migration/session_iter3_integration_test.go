//go:build integration

package migration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

func TestIter3_UncertainSiblingBlocksEnqueue(t *testing.T) {
	fx := newFixture(t, "edit")
	hold := create(t, fx, "Tahan", "SELECT 1", "SELECT 1", false, "")
	next := create(t, fx, "Lanjut", "SELECT 2", "SELECT 2", false, "")
	a := submit(t, fx, hold.ID)
	b := submit(t, fx, next.ID)
	setExecStatus(t, fx, a.ID, migration.StatusUncertain)
	approve(t, fx, b.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, b.ID, nil); !errors.Is(err, migration.ErrBlocked) {
		t.Fatalf("enqueue saat ada uncertain = %v", err)
	}
}

func TestIter3_ResumeFailedAndCancelApproved(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Sekali", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusFailed)
	if _, err := fx.srv.Service.Resume(context.Background(), fx.editor, exec.ID, nil); !errors.Is(err, migration.ErrNotResumable) {
		t.Fatalf("resume gagal = %v", err)
	}
	setExecStatus(t, fx, exec.ID, migration.StatusApproved)
	if err := fx.srv.Service.Cancel(context.Background(), fx.editor, exec.ID); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if err := fx.platform.QueryRow(context.Background(), `SELECT count(*) FROM migration_jobs WHERE execution_id = $1`, exec.ID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("job tercipta saat batal sebelum enqueue: %d", jobs)
	}
}

func TestIter3_DownSQLTamperBlocksRollback(t *testing.T) {
	fx, target := targetFixture(t)
	m := create(t, fx, "Asli", "CREATE TABLE ran (id int)", "DROP TABLE ran", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)
	e := executor(t, fx)
	if err := e.Run(context.Background(), jobOf(exec.ID, fx.dbID)); !isHalt(err, worker.DispositionSuccess) {
		t.Fatal(err)
	}
	if _, err := fx.platform.Exec(context.Background(), `
		UPDATE migration_revisions SET down_sql = 'DROP TABLE ran_lain'
		WHERE id = (SELECT revision_id FROM executions WHERE id = $1)`, exec.ID); err != nil {
		t.Fatal(err)
	}
	rb, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	approve(t, fx, rb.ID)
	setExecStatus(t, fx, rb.ID, migration.StatusQueued)
	if err := e.Run(context.Background(), jobOf(rb.ID, fx.dbID)); !isHalt(err, worker.DispositionDead) {
		t.Fatalf("rollback SQL yang diubah = %v", err)
	}
	if !tableExists(t, target, "ran") {
		t.Fatal("tabel ikut terhapus walau hash down SQL berubah")
	}
}
