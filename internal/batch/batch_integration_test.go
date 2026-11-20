//go:build integration

package batch_test

import (
	"context"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/batch"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestBatchRunner_ApplyAllCommitted(t *testing.T) {
	ctx := context.Background()
	platformPool := testutil.NewPlatformDB(t)
	_, targetPool := testutil.NewTargetDB(t, `
		CREATE TABLE items (
			id  INT PRIMARY KEY,
			val INT NOT NULL DEFAULT 0
		);
		INSERT INTO items (id, val)
		SELECT g, 10 FROM generate_series(1, 25) AS g;
	`)

	mirror := batch.NewMirrorRepo(platformPool)
	runner := batch.NewRunner()

	var events []batch.Checkpoint
	sink := batch.ProgressFunc(func(ctx context.Context, dir string, batchNo int, from, to, rows int64) error {
		cp := batch.Checkpoint{
			ExecutionID:  101,
			Direction:    dir,
			BatchNo:      batchNo,
			FromValue:    from,
			ToValue:      to,
			RowsAffected: rows,
		}
		events = append(events, cp)
		return mirror.RecordBatch(ctx, cp)
	})

	spec := batch.Spec{
		ExecutionID: 101,
		Table:       "items",
		Column:      "id",
		Size:        10,
		UpSQL:       "UPDATE items SET val = val + 5 WHERE id > :from AND id <= :to",
		DownSQL:     "UPDATE items SET val = val - 5 WHERE id > :from AND id <= :to",
	}

	res, err := runner.Apply(ctx, targetPool, spec, sink)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Status != batch.StatusDone {
		t.Fatalf("Status = %s, want done", res.Status)
	}
	if res.BatchesCommitted != 3 || res.RowsCommitted != 25 {
		t.Fatalf("Result = %+v, want 3 batches and 25 rows", res)
	}
	if len(events) != 3 {
		t.Fatalf("sink events = %d, want 3", len(events))
	}

	// Cek rentang tiap batch: (0,10], (10,20], (20,25].
	wantBounds := [][3]int64{{0, 10, 10}, {10, 20, 10}, {20, 25, 5}}
	for i, w := range wantBounds {
		if events[i].FromValue != w[0] || events[i].ToValue != w[1] || events[i].RowsAffected != w[2] {
			t.Fatalf("batch %d = %+v, want from=%d to=%d rows=%d", i+1, events[i], w[0], w[1], w[2])
		}
	}

	// Pastikan semua baris di target sudah bernilai 15.
	var countUpdated int
	if err := targetPool.QueryRow(ctx, `SELECT count(*) FROM items WHERE val = 15`).Scan(&countUpdated); err != nil {
		t.Fatal(err)
	}
	if countUpdated != 25 {
		t.Fatalf("countUpdated = %d, want 25", countUpdated)
	}

	// Pastikan cermin di database platform tercatat 3 baris.
	mirrored, err := mirror.ListByExecution(ctx, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len(mirrored) != 3 {
		t.Fatalf("mirrored = %d, want 3", len(mirrored))
	}
}

func TestBatchRunner_PartialFailureAndResume(t *testing.T) {
	ctx := context.Background()
	_, targetPool := testutil.NewTargetDB(t, `
		CREATE TABLE accounts (
			id      INT PRIMARY KEY,
			balance INT NOT NULL,
			divisor INT NOT NULL DEFAULT 1
		);
		INSERT INTO accounts (id, balance, divisor)
		SELECT g, 100, CASE WHEN g = 15 THEN 0 ELSE 1 END
		FROM generate_series(1, 25) AS g;
	`)

	runner := batch.NewRunner()
	spec := batch.Spec{
		ExecutionID: 202,
		Table:       "accounts",
		Column:      "id",
		Size:        10,
		// Batch 1 (1..10) sukses; Batch 2 (11..20) gagal karena id=15 memiliki divisor=0 (division by zero).
		UpSQL:   "UPDATE accounts SET balance = balance + (10 / divisor) WHERE id > :from AND id <= :to",
		DownSQL: "UPDATE accounts SET balance = balance - 10 WHERE id > :from AND id <= :to",
	}

	res, err := runner.Apply(ctx, targetPool, spec, nil)
	if err == nil {
		t.Fatal("Apply seharusnya gagal pada batch ke-2 (division by zero)")
	}
	if res.Status != batch.StatusPartial {
		t.Fatalf("Status = %s, want partial", res.Status)
	}
	if res.BatchesCommitted != 1 || res.RowsCommitted != 10 {
		t.Fatalf("Result setelah gagal = %+v, want 1 batch & 10 rows committed", res)
	}

	// Batch 1 (1..10) harus tetap committed (balance = 110), sedangkan 11..25 masih 100.
	var committedRows, untouchedRows int
	_ = targetPool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE balance = 110`).Scan(&committedRows)
	_ = targetPool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE balance = 100`).Scan(&untouchedRows)
	if committedRows != 10 || untouchedRows != 15 {
		t.Fatalf("committed=%d untouched=%d, want 10 and 15", committedRows, untouchedRows)
	}

	// Perbaiki data penyebab gagal di target, lalu RESUME dengan ExecutionID yang sama.
	if _, err := targetPool.Exec(ctx, `UPDATE accounts SET divisor = 1 WHERE id = 15`); err != nil {
		t.Fatal(err)
	}

	var resumedBatches []int
	res2, err := runner.Apply(ctx, targetPool, spec, batch.ProgressFunc(func(ctx context.Context, dir string, batchNo int, from, to, rows int64) error {
		resumedBatches = append(resumedBatches, batchNo)
		return nil
	}))
	if err != nil {
		t.Fatalf("Resume Apply: %v", err)
	}
	if res2.Status != batch.StatusDone {
		t.Fatalf("Status setelah resume = %s, want done", res2.Status)
	}
	if res2.BatchesCommitted != 3 || res2.RowsCommitted != 25 {
		t.Fatalf("Result setelah resume = %+v, want total 3 batches & 25 rows", res2)
	}
	// Resume hanya boleh menjalankan batch 2 dan 3, TIDAK mengulang batch 1.
	if len(resumedBatches) != 2 || resumedBatches[0] != 2 || resumedBatches[1] != 3 {
		t.Fatalf("resumedBatches = %v, want [2 3]", resumedBatches)
	}

	// Pastikan batch 1 tidak ter-update dua kali (semua 25 baris harus balance = 110, bukan 120).
	var count110 int
	_ = targetPool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE balance = 110`).Scan(&count110)
	if count110 != 25 {
		t.Fatalf("count110 = %d, want 25 (batch 1 tidak boleh dijalankan ulang)", count110)
	}
}

func TestBatchRunner_RevertReverseOrderAndPartiallyRevertedResume(t *testing.T) {
	ctx := context.Background()
	_, targetPool := testutil.NewTargetDB(t, `
		CREATE TABLE ledger (
			id   INT PRIMARY KEY,
			amount INT NOT NULL,
			revert_guard INT NOT NULL DEFAULT 1
		);
		INSERT INTO ledger (id, amount)
		SELECT g, 50 FROM generate_series(1, 25) AS g;
	`)

	runner := batch.NewRunner()
	spec := batch.Spec{
		ExecutionID: 303,
		Table:       "ledger",
		Column:      "id",
		Size:        10,
		UpSQL:       "UPDATE ledger SET amount = amount + 20 WHERE id > :from AND id <= :to",
		DownSQL:     "UPDATE ledger SET amount = amount - (20 / revert_guard) WHERE id > :from AND id <= :to",
	}

	// 1. Apply 3 batch sampai selesai.
	res, err := runner.Apply(ctx, targetPool, spec, nil)
	if err != nil || res.Status != batch.StatusDone {
		t.Fatalf("Apply: res=%+v err=%v", res, err)
	}

	// 2. Pasang jebakan di id=12 (batch 2) agar revert batch 3 (21..25) sukses, lalu batch 2 (11..20) gagal.
	if _, err := targetPool.Exec(ctx, `UPDATE ledger SET revert_guard = 0 WHERE id = 12`); err != nil {
		t.Fatal(err)
	}

	var revertOrder []int
	revRes, err := runner.Revert(ctx, targetPool, spec, batch.ProgressFunc(func(ctx context.Context, dir string, batchNo int, from, to, rows int64) error {
		revertOrder = append(revertOrder, batchNo)
		return nil
	}))
	if err == nil {
		t.Fatal("Revert seharusnya gagal pada batch 2")
	}
	if revRes.Status != batch.StatusPartiallyReverted {
		t.Fatalf("Status = %s, want partially_reverted", revRes.Status)
	}
	if len(revertOrder) != 1 || revertOrder[0] != 3 {
		t.Fatalf("revertOrder sebelum gagal = %v, want [3] (mulai dari batch terbesar)", revertOrder)
	}

	// Batch 3 (21..25) sudah kembali ke 50; Batch 1 & 2 (1..20) masih 70.
	var revertedCount, remainingCount int
	_ = targetPool.QueryRow(ctx, `SELECT count(*) FROM ledger WHERE amount = 50`).Scan(&revertedCount)
	_ = targetPool.QueryRow(ctx, `SELECT count(*) FROM ledger WHERE amount = 70`).Scan(&remainingCount)
	if revertedCount != 5 || remainingCount != 20 {
		t.Fatalf("reverted=%d remaining=%d, want 5 and 20", revertedCount, remainingCount)
	}

	// 3. Perbaiki guard lalu resume Revert: harus melanjutkan batch 2 lalu batch 1 dalam urutan terbalik.
	if _, err := targetPool.Exec(ctx, `UPDATE ledger SET revert_guard = 1 WHERE id = 12`); err != nil {
		t.Fatal(err)
	}

	revRes2, err := runner.Revert(ctx, targetPool, spec, batch.ProgressFunc(func(ctx context.Context, dir string, batchNo int, from, to, rows int64) error {
		revertOrder = append(revertOrder, batchNo)
		return nil
	}))
	if err != nil {
		t.Fatalf("Resume Revert: %v", err)
	}
	if revRes2.Status != batch.StatusDone {
		t.Fatalf("Status = %s, want done", revRes2.Status)
	}
	if len(revertOrder) != 3 || revertOrder[0] != 3 || revertOrder[1] != 2 || revertOrder[2] != 1 {
		t.Fatalf("urutan revert penuh = %v, want [3 2 1]", revertOrder)
	}

	_ = targetPool.QueryRow(ctx, `SELECT count(*) FROM ledger WHERE amount = 50`).Scan(&revertedCount)
	if revertedCount != 25 {
		t.Fatalf("seluruh 25 baris harus kembali ke 50, got %d", revertedCount)
	}
}

func TestBatchRunner_InvalidTableOrNonIntegerColumnRejected(t *testing.T) {
	ctx := context.Background()
	_, targetPool := testutil.NewTargetDB(t, `
		CREATE TABLE bad_table (
			code TEXT PRIMARY KEY,
			v    INT
		);
	`)
	runner := batch.NewRunner()

	// Kolom bertipe TEXT harus ditolak (D39: batch_column harus integer).
	res, err := runner.Apply(ctx, targetPool, batch.Spec{
		ExecutionID: 404,
		Table:       "bad_table",
		Column:      "code",
		Size:        10,
		UpSQL:       "UPDATE bad_table SET v = 1 WHERE code > :from AND code <= :to",
	}, nil)
	if err == nil || res.Status != batch.StatusFailed {
		t.Fatalf("kolom non-integer harus gagal, got res=%+v err=%v", res, err)
	}
}
