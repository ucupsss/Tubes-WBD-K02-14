package migration

import (
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

func TestIter1_BatchPlaceholdersOnlyInCommentOrString(t *testing.T) {
	in := Input{
		Title:       "Naikkan payload",
		ExecMode:    ModeBatch,
		BatchTable:  "big_events",
		BatchColumn: "id",
		BatchSize:   50_000,
		UpSQL:       "UPDATE big_events SET payload = payload + 1; -- rentang :from :to tidak berlaku",
		DownSQL:     "UPDATE big_events SET payload = payload - 1 WHERE note = ':from :to'",
	}
	fe, ok := apperr.FieldErrors(in.Validate())
	if !ok {
		t.Fatal("placeholder yang hanya ada di komentar atau string harus ditolak")
	}
	if fe["up_sql"] == "" || fe["down_sql"] == "" {
		t.Fatalf("fields = %v", fe)
	}
}

func TestIter1_FunctionBodyMayContainCommitButScriptMayNot(t *testing.T) {
	ok := Input{
		Title:   "Fungsi",
		UpSQL:   "CREATE FUNCTION f() RETURNS void AS $$ BEGIN COMMIT; END $$ LANGUAGE plpgsql",
		DownSQL: "DROP FUNCTION f()",
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.DownSQL = "DROP FUNCTION f(); COMMIT"
	if _, ok := apperr.FieldErrors(bad.Validate()); !ok {
		t.Fatal("COMMIT di luar badan fungsi harus ditolak")
	}
}

func TestIter1_IrreversibleBatchIgnoresDownAndStillNeedsRange(t *testing.T) {
	in := Input{
		Title:              "  Hapus bertahap  ",
		Irreversible:       true,
		IrreversibleReason: "  data lama tidak bisa dikembalikan ",
		ExecMode:           " batch ",
		BatchTable:         "big_events",
		BatchColumn:        "id",
		BatchSize:          1000,
		UpSQL:              "DELETE FROM big_events WHERE id > :from AND id <= :to",
		DownSQL:            "BEGIN",
	}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	norm := in.Normalized()
	if norm.DownSQL != "" || norm.ExecMode != ModeBatch || norm.IrreversibleReason != "data lama tidak bisa dikembalikan" {
		t.Fatalf("normalized = %+v", norm)
	}
	in.UpSQL = "DELETE FROM big_events WHERE id > 0"
	if _, ok := apperr.FieldErrors(in.Validate()); !ok {
		t.Fatal("up SQL batch tanpa rentang harus ditolak walau irreversible")
	}
}

func TestIter1_ChainAfterRollbackWhileForwardIsPending(t *testing.T) {
	v := int32(2)
	top := int32(3)
	// B sudah di-rollback, lalu forward-nya masih menunggu approval.
	pendingForward := head{Frozen: true, HasRolledBack: true, HasFailed: true, Open: true}
	if err := pendingForward.canForward(); err != ErrBlocked {
		t.Fatalf("forward saat masih menunggu = %v", err)
	}
	if err := pendingForward.canSubmit(); err != ErrBlocked {
		t.Fatalf("submit saat masih menunggu = %v", err)
	}
	if err := pendingForward.canRollback(); err != ErrBlocked {
		t.Fatalf("rollback saat forward masih terbuka = %v", err)
	}

	// C masih aktif di puncak, B tidak boleh di-rollback dulu. C juga irreversible.
	activeTop := head{Version: &top, Top: &top, Frozen: true, ActiveSuccess: true, Irreversible: true}
	if err := activeTop.canRollback(); err != ErrIrreversible {
		t.Fatalf("puncak irreversible = %v", err)
	}
	notTop := activeTop
	notTop.Version = &v
	if err := notTop.canRollback(); err != ErrNotTop {
		t.Fatalf("bukan puncak = %v", err)
	}
	if err := activeTop.canForward(); err != ErrAlreadyActive {
		t.Fatalf("forward yang masih aktif = %v", err)
	}
}

func TestIter1_HideRollbackMixedHistory(t *testing.T) {
	// E1 sukses, E2 dibatalkan E3, E4 forward sukses, E5 gagal, E6 rollback E4 yang gagal.
	rows := []struct {
		name   string
		kind   string
		status string
		undone bool
		hide   bool
	}{
		{"E1", KindApply, StatusSuccess, false, false},
		{"E2", KindApply, StatusSuccess, true, true},
		{"E3", KindRollback, StatusSuccess, false, true},
		{"E4", KindForward, StatusSuccess, false, false},
		{"E5", KindApply, StatusFailed, false, false},
		{"E6", KindRollback, StatusFailed, false, false},
		{"E7 dibatalkan walau rollback-nya gagal dulu", KindForward, StatusSuccess, true, true},
	}
	for _, row := range rows {
		if got := hiddenByRollback(row.kind, row.status, row.undone); got != row.hide {
			t.Fatalf("%s hidden=%v want %v", row.name, got, row.hide)
		}
	}
}

func TestIter1_HashIncludesBatchShapeNotJustSQL(t *testing.T) {
	up := "UPDATE big_events SET payload = payload + 1 WHERE id > :from AND id <= :to"
	down := "UPDATE big_events SET payload = payload - 1 WHERE id > :from AND id <= :to"
	a := HashRevision(up, down, ModeBatch, "big_events", "id", 50_000)
	b := HashRevision(up, down, ModeBatch, "big_events", "id", 10_000)
	c := HashRevision(up, "", ModeBatch, "big_events", "id", 50_000)
	if a == b || a == c || len(a) != 64 {
		t.Fatalf("hash tidak membedakan konfigurasi: %s %s %s", a, b, c)
	}
}
