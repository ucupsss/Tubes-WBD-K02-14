package migration

import (
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

func TestHashRevision_ChangesWithSQL(t *testing.T) {
	t.Parallel()
	a := HashRevision("SELECT 1", "SELECT 2", ModeNormal, "", "", 0)
	b := HashRevision("SELECT 9", "SELECT 2", ModeNormal, "", "", 0)
	if a == b || len(a) != 64 {
		t.Fatalf("hash = %s vs %s", a, b)
	}
	if HashRevision("SELECT 1", "SELECT 2", ModeBatch, "t", "id", 10) == a {
		t.Fatal("mode batch harus mengubah hash")
	}
}

func TestInput_Validate(t *testing.T) {
	t.Parallel()
	ok := Input{Title: "Buat", UpSQL: "CREATE TABLE t (id int)", DownSQL: "DROP TABLE t"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Title = " "
	fe, isField := apperr.FieldErrors(bad.Validate())
	if !isField || fe["title"] == "" {
		t.Fatalf("err fields = %v", fe)
	}
	bad = ok
	bad.UpSQL = "BEGIN"
	if _, isField = apperr.FieldErrors(bad.Validate()); !isField {
		t.Fatal("BEGIN harus ditolak di up_sql")
	}
	irrev := Input{Title: "Hapus", UpSQL: "DROP TABLE t", Irreversible: true}
	fe, isField = apperr.FieldErrors(irrev.Validate())
	if !isField || fe["irreversible_reason"] == "" {
		t.Fatalf("alasan kosong: %v", fe)
	}
	irrev.IrreversibleReason = "tidak ada cadangan"
	if err := irrev.Validate(); err != nil {
		t.Fatal(err)
	}
	batch := ok
	batch.ExecMode = ModeBatch
	batch.UpSQL = "UPDATE t SET v = 1 WHERE id > :from AND id <= :to"
	batch.DownSQL = "UPDATE t SET v = 0 WHERE id > :from AND id <= :to"
	batch.BatchTable = "t"
	batch.BatchColumn = "id"
	batch.BatchSize = 100
	if err := batch.Validate(); err != nil {
		t.Fatal(err)
	}
	batch.BatchSize = 0
	if _, isField = apperr.FieldErrors(batch.Validate()); !isField {
		t.Fatal("batch_size 0 harus ditolak")
	}
}

func TestRules_SubmitForwardRollback(t *testing.T) {
	t.Parallel()
	v1, v2 := int32(1), int32(2)
	draft := head{}
	if err := draft.canSubmit(); err != nil {
		t.Fatal(err)
	}
	if err := draft.canForward(); err != ErrNotForwardable {
		t.Fatalf("forward draft = %v", err)
	}
	if err := draft.canRollback(); err != ErrNotActive {
		t.Fatalf("rollback draft = %v", err)
	}

	frozenFail := head{Frozen: true, HasFailed: true}
	if err := frozenFail.canSubmit(); err != ErrNotSubmittable {
		t.Fatalf("submit frozen = %v", err)
	}
	if err := frozenFail.canForward(); err != nil {
		t.Fatal(err)
	}

	active := head{Version: &v2, Top: &v2, Frozen: true, ActiveSuccess: true}
	if err := active.canRollback(); err != nil {
		t.Fatal(err)
	}
	notTop := active
	notTop.Top = &v1
	if err := notTop.canRollback(); err != ErrNotTop {
		t.Fatalf("bukan teratas = %v", err)
	}
	irrev := active
	irrev.Irreversible = true
	if err := irrev.canRollback(); err != ErrIrreversible {
		t.Fatalf("irreversible = %v", err)
	}
	if err := active.canForward(); err != ErrAlreadyActive {
		t.Fatalf("forward aktif = %v", err)
	}
	open := active
	open.Open = true
	if err := open.canRollback(); err != ErrBlocked {
		t.Fatalf("terbuka = %v", err)
	}
}

func TestHiddenByRollback_SpecExample(t *testing.T) {
	t.Parallel()
	type row struct {
		name   string
		kind   string
		status string
		undone bool
		hidden bool
	}
	rows := []row{
		{"E1 apply success", KindApply, StatusSuccess, false, false},
		{"E2 apply success dibatalkan", KindApply, StatusSuccess, true, true},
		{"E3 rollback success", KindRollback, StatusSuccess, false, true},
		{"E4 forward success", KindForward, StatusSuccess, false, false},
		{"E5 apply success", KindApply, StatusSuccess, false, false},
		{"rollback gagal", KindRollback, StatusFailed, false, false},
		{"apply yang rollback-nya gagal", KindApply, StatusSuccess, false, false},
	}
	var visible []string
	for _, r := range rows {
		got := hiddenByRollback(r.kind, r.status, r.undone)
		if got != r.hidden {
			t.Fatalf("%s hidden = %v, want %v", r.name, got, r.hidden)
		}
		if !got {
			visible = append(visible, r.name)
		}
	}
	joined := strings.Join(visible, ",")
	for _, keep := range []string{"E1", "E4", "E5", "rollback gagal", "rollback-nya gagal"} {
		if !strings.Contains(joined, keep) {
			t.Fatalf("yang tersisa = %s, hilang %s", joined, keep)
		}
	}
}

func TestHead_CanRollbackRevertsPartialBatch(t *testing.T) {
	t.Parallel()
	partial := int64(7)
	tests := []struct {
		name string
		h    head
		want error
	}{
		{"partial tanpa execution lain boleh di-revert", head{Frozen: true, Open: true, PartialID: &partial}, nil},
		{"revert yang sedang diajukan menahan permintaan kedua", head{Frozen: true, Open: true, OpenOther: true, PartialID: &partial}, ErrBlocked},
		{"revisi irreversible menolak revert", head{Frozen: true, Open: true, PartialID: &partial, PartialIrreversible: true}, ErrIrreversible},
		{"tanpa partial dan tidak aktif tetap ditolak", head{Frozen: true}, ErrNotActive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.h.canRollback(); got != tt.want {
				t.Fatalf("canRollback() = %v, want %v", got, tt.want)
			}
		})
	}
}
