package batch

import (
	"errors"
	"testing"
)

func TestExpandBounds(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		from    int64
		to      int64
		want    string
		wantErr error
	}{
		{
			name: "standard update",
			sql:  "UPDATE big_events SET v = v + 1 WHERE id > :from AND id <= :to",
			from: 100,
			to:   200,
			want: "UPDATE big_events SET v = v + 1 WHERE id > 100 AND id <= 200",
		},
		{
			name: "negative bounds and multiple occurrences",
			sql:  "UPDATE t SET a = 1 WHERE id > :from AND id <= :to AND parent_id > :from",
			from: -10,
			to:   50,
			want: "UPDATE t SET a = 1 WHERE id > -10 AND id <= 50 AND parent_id > -10",
		},
		{
			name: "ignores strings comments and type casts",
			sql:  "UPDATE t SET note = ':from to :to', x = '1'::text WHERE /* :from */ id > :from -- :to\n AND id <= :to",
			from: 0,
			to:   1000,
			want: "UPDATE t SET note = ':from to :to', x = '1'::text WHERE /* :from */ id > 0 -- :to\n AND id <= 1000",
		},
		{
			name: "ignores prefix identifiers like :from_id or :total",
			sql:  "SELECT ':from_id' FROM t WHERE id > :from AND id <= :to",
			from: 10,
			to:   20,
			want: "SELECT ':from_id' FROM t WHERE id > 10 AND id <= 20",
		},
		{
			name:    "missing :to rejected",
			sql:     "UPDATE t SET v = 1 WHERE id > :from",
			from:    0,
			to:      10,
			wantErr: ErrMissingBounds,
		},
		{
			name:    "missing :from rejected",
			sql:     "UPDATE t SET v = 1 WHERE id <= :to",
			from:    0,
			to:      10,
			wantErr: ErrMissingBounds,
		},
		{
			name:    "placeholders only inside string literal rejected",
			sql:     "UPDATE t SET note = ':from :to'",
			from:    0,
			to:      10,
			wantErr: ErrMissingBounds,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandBounds(tc.sql, tc.from, tc.to)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestValidateSpec(t *testing.T) {
	valid := Spec{
		ExecutionID: 1,
		Table:       "big_events",
		Column:      "id",
		Size:        500,
		UpSQL:       "UPDATE big_events SET v = v + 1 WHERE id > :from AND id <= :to",
		DownSQL:     "UPDATE big_events SET v = v - 1 WHERE id > :from AND id <= :to",
	}
	if err := ValidateSpec(valid, DirectionApply); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	if err := ValidateSpec(valid, DirectionRevert); err != nil {
		t.Fatalf("valid revert spec rejected: %v", err)
	}

	noTable := valid
	noTable.Table = ""
	if err := ValidateSpec(noTable, DirectionApply); err == nil {
		t.Fatal("empty table must fail")
	}

	badSize := valid
	badSize.Size = 0
	if err := ValidateSpec(badSize, DirectionApply); err == nil {
		t.Fatal("zero size must fail")
	}
}
