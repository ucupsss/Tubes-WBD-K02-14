//go:build integration

package migration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

// runQueued mengambil satu job dari antrean dan memprosesnya seperti worker sungguhan.
func runQueued(t *testing.T, fx fixture, workerID string) {
	t.Helper()
	q := queue.New(fx.platform)
	job, err := q.Claim(context.Background(), workerID)
	if err != nil || job == nil {
		t.Fatalf("claim: job=%v err=%v", job, err)
	}
	repo := migration.NewPgRepo().Bind(fx.platform)
	if err := worker.Process(context.Background(), q, repo, executor(t, fx), workerID, *job); err != nil {
		t.Fatal(err)
	}
}

func execStatus(t *testing.T, fx fixture, id int64) migration.Execution {
	t.Helper()
	e, _, err := fx.srv.Service.GetExecution(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func sumPayload(t *testing.T, target *pgxpool.Pool) (changed int) {
	t.Helper()
	if err := target.QueryRow(context.Background(), `SELECT count(*) FROM big_events WHERE payload <> 0`).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	return changed
}

// partialBatch menyiapkan 25 baris dan migration batch (ukuran 10) yang gagal di batch ketiga,
// sehingga execution berhenti partial dengan 2 batch (20 baris) committed.
func partialBatch(t *testing.T) (fixture, *pgxpool.Pool, migration.Migration, migration.Execution) {
	t.Helper()
	fx, target := targetFixture(t)
	if _, err := target.Exec(context.Background(), `
		CREATE TABLE big_events (id BIGINT PRIMARY KEY, payload INT NOT NULL);
		INSERT INTO big_events (id, payload) SELECT g, 0 FROM generate_series(1, 25) AS g;
	`); err != nil {
		t.Fatal(err)
	}
	m, err := fx.srv.Service.Create(context.Background(), fx.editor, fx.dbID, migration.Input{
		Title: "Batch gagal di tengah", ExecMode: migration.ModeBatch, BatchTable: "big_events", BatchColumn: "id", BatchSize: 10,
		// Pembagian dengan nol (22012, bukan kegagalan sementara) hanya pada batch yang :to-nya > 20.
		UpSQL:   "UPDATE big_events SET payload = payload + 1 / (CASE WHEN :to > 20 THEN 0 ELSE 1 END) WHERE id > :from AND id <= :to",
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
	runQueued(t, fx, "worker-partial")
	got := execStatus(t, fx, exec.ID)
	if got.Status != migration.StatusPartial || got.BatchesCommitted != 2 || got.RowsCommitted != 20 {
		t.Fatalf("apply harus partial 2 batch/20 baris, got %+v", got)
	}
	if n := sumPayload(t, target); n != 20 {
		t.Fatalf("baris berubah = %d, want 20", n)
	}
	return fx, target, m, got
}

func TestRevertPartial_UndoesCommittedBatchesAndReleasesDatabase(t *testing.T) {
	fx, target, m, partial := partialBatch(t)
	ctx := context.Background()

	rv, err := fx.srv.Service.Rollback(ctx, fx.editor, fx.dbID, m.ID)
	if err != nil {
		t.Fatalf("revert partial harus bisa diajukan: %v", err)
	}
	if rv.Kind != migration.KindRollback || rv.RollsBackExecutionID == nil || *rv.RollsBackExecutionID != partial.ID {
		t.Fatalf("revert harus merujuk execution partial #%d: %+v", partial.ID, rv)
	}
	if rv.Status != migration.StatusPendingReview {
		t.Fatalf("revert tetap butuh approval, status = %s", rv.Status)
	}

	// Selama revert berjalan, partial tidak boleh di-resume dan revert kedua ditolak.
	if _, err := fx.srv.Service.Resume(ctx, fx.editor, partial.ID, nil); !errors.Is(err, migration.ErrReverted) {
		t.Fatalf("resume partial yang sedang di-revert: err = %v, want ErrReverted", err)
	}
	if _, err := fx.srv.Service.Rollback(ctx, fx.editor, fx.dbID, m.ID); !errors.Is(err, migration.ErrBlocked) {
		t.Fatalf("revert kedua: err = %v, want ErrBlocked", err)
	}

	approve(t, fx, rv.ID)
	if _, err := fx.srv.Service.Enqueue(ctx, fx.editor, rv.ID, nil); err != nil {
		t.Fatalf("partial yang dibatalkan tidak boleh menahan revert-nya sendiri: %v", err)
	}
	runQueued(t, fx, "worker-revert")

	got := execStatus(t, fx, rv.ID)
	if got.Status != migration.StatusSuccess || got.BatchesCommitted != 2 || got.RowsCommitted != 20 {
		t.Fatalf("revert = %+v, want success 2 batch/20 baris", got)
	}
	if got.VersionApplied != nil {
		t.Fatalf("revert partial tidak menyentuh posisi rantai, version_applied = %d", *got.VersionApplied)
	}
	if n := sumPayload(t, target); n != 0 {
		t.Fatalf("baris yang masih berubah setelah revert = %d", n)
	}

	// Urutan terbalik: batch 2 dibatalkan sebelum batch 1.
	batches, err := fx.srv.Service.ListBatches(ctx, rv.ID)
	if err != nil {
		t.Fatal(err)
	}
	reverted := map[int]time.Time{}
	for _, b := range batches {
		if b.Direction == "revert" {
			reverted[b.BatchNo] = b.CommittedAt
		}
	}
	if len(reverted) != 2 || !reverted[2].Before(reverted[1]) {
		t.Fatalf("revert harus membatalkan batch 2 sebelum batch 1: %v", reverted)
	}

	if _, err := fx.srv.Service.Resume(ctx, fx.editor, partial.ID, nil); !errors.Is(err, migration.ErrReverted) {
		t.Fatalf("resume setelah revert sukses: err = %v, want ErrReverted", err)
	}

	// Database tidak lagi tertahan: migration lain bisa diajukan dan dijalankan.
	next := create(t, fx, "Berikutnya", "CREATE TABLE sesudah_revert (id int)", "DROP TABLE sesudah_revert", false, "")
	exec := submit(t, fx, next.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(ctx, fx.editor, exec.ID, nil); err != nil {
		t.Fatalf("database masih tertahan partial yang sudah di-revert: %v", err)
	}

	// Hide rollback menyembunyikan pasangan partial + revert berdasarkan execution ID.
	list, _, err := fx.srv.Service.ListExecutions(ctx, fx.dbID, true, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if e.ID == partial.ID || e.ID == rv.ID {
			t.Fatalf("hide rollback masih menampilkan execution #%d", e.ID)
		}
	}

	// Migration yang dibatalkan boleh di-forward lagi.
	if _, err := fx.srv.Service.Forward(ctx, fx.editor, fx.dbID, m.ID); err != nil {
		t.Fatalf("forward setelah revert partial: %v", err)
	}
}

func TestRevertPartial_IrreversibleRejected(t *testing.T) {
	fx, target := targetFixture(t)
	if _, err := target.Exec(context.Background(), `
		CREATE TABLE big_events (id BIGINT PRIMARY KEY, payload INT NOT NULL);
		INSERT INTO big_events (id, payload) SELECT g, 0 FROM generate_series(1, 25) AS g;
	`); err != nil {
		t.Fatal(err)
	}
	m, err := fx.srv.Service.Create(context.Background(), fx.editor, fx.dbID, migration.Input{
		Title: "Hapus kolom batch", ExecMode: migration.ModeBatch, BatchTable: "big_events", BatchColumn: "id", BatchSize: 10,
		UpSQL:        "UPDATE big_events SET payload = payload + 1 / (CASE WHEN :to > 20 THEN 0 ELSE 1 END) WHERE id > :from AND id <= :to",
		Irreversible: true, IrreversibleReason: "nilai lama tidak disimpan",
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	if _, err := fx.srv.Service.Enqueue(context.Background(), fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	runQueued(t, fx, "worker-irrev")
	if got := execStatus(t, fx, exec.ID); got.Status != migration.StatusPartial {
		t.Fatalf("status = %s, want partial", got.Status)
	}
	if _, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, m.ID); !errors.Is(err, migration.ErrIrreversible) {
		t.Fatalf("revert irreversible: err = %v, want ErrIrreversible", err)
	}
}

// Migration yang pernah diajukan (lalu ditolak dan dibuatkan revisi baru yang belum diajukan) tidak boleh
// dihapus: revisi lamanya masih dirujuk riwayat execution. Dulu ini menghasilkan 500 karena foreign key.
func TestDelete_RejectedThenRevisedIsNotDeletable(t *testing.T) {
	fx := newFixture(t, "edit")
	ctx := context.Background()
	m := create(t, fx, "Ditolak", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	if _, err := fx.platform.Exec(ctx, `UPDATE executions SET status = 'rejected' WHERE id = $1`, exec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.srv.Service.Update(ctx, fx.editor, fx.dbID, m.ID, migration.Input{Title: "Ditolak", UpSQL: "SELECT 2", DownSQL: "SELECT 2"}); err != nil {
		t.Fatal(err)
	}
	if err := fx.srv.Service.Delete(ctx, fx.dbID, m.ID); !errors.Is(err, migration.ErrNotDeletable) {
		t.Fatalf("hapus migration yang pernah diajukan: err = %v, want ErrNotDeletable", err)
	}
}

// Eksekusi yang berjalan lebih lama dari batas stale (30 detik) tidak boleh diklaim lagi selama
// heartbeat-nya masih menyegarkan claimed_at. Dulu job seperti ini diklaim ulang, bahkan oleh worker
// yang sama, sehingga dua run berjalan bersamaan.
func TestClaim_TouchedLongRunningJobIsNotReclaimed(t *testing.T) {
	fx := newFixture(t, "edit")
	ctx := context.Background()
	m := create(t, fx, "Panjang", "SELECT 1", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusApproved)
	if _, err := fx.srv.Service.Enqueue(ctx, fx.editor, exec.ID, nil); err != nil {
		t.Fatal(err)
	}
	q := queue.New(fx.platform)
	first, err := q.Claim(ctx, "worker-a")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	// Run sudah berjalan 2 menit, tapi heartbeat barusan menyegarkan claimed_at.
	if _, err := fx.platform.Exec(ctx, `UPDATE migration_jobs SET claimed_at = now() - interval '2 minutes' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := queue.Touch(ctx, fx.platform, first.ID, "worker-a"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"worker-b", "worker-a"} {
		if again, err := q.Claim(ctx, w); err != nil || again != nil {
			t.Fatalf("%s mengklaim job yang masih berjalan: %+v %v", w, again, err)
		}
	}
	// Touch dari worker lain tidak boleh menyegarkan klaim yang bukan miliknya.
	if _, err := fx.platform.Exec(ctx, `UPDATE migration_jobs SET claimed_at = now() - interval '2 minutes' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := queue.Touch(ctx, fx.platform, first.ID, "worker-x"); err != nil {
		t.Fatal(err)
	}
	if taken, err := q.Claim(ctx, "worker-b"); err != nil || taken == nil {
		t.Fatalf("job yang benar-benar ditinggalkan harus bisa diambil alih: %+v %v", taken, err)
	}
}
