package migration

import (
	"errors"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

func TestSplitStatements(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		sql   string
		kinds []StatementKind
		sqls  []string
	}{
		{name: "empty", sql: "", kinds: nil},
		{name: "whitespace", sql: "  \n\t", kinds: nil},
		{name: "only semicolons", sql: ";;;", kinds: nil},
		{
			name:  "semicolon in string",
			sql:   "SELECT 'a;b'",
			kinds: []StatementKind{KindDML},
			sqls:  []string{"SELECT 'a;b'"},
		},
		{
			name:  "semicolon in dollar body",
			sql:   "CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END $$ LANGUAGE plpgsql",
			kinds: []StatementKind{KindDDL},
			sqls:  []string{"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END $$ LANGUAGE plpgsql"},
		},
		{
			name:  "semicolon in line comment",
			sql:   "SELECT 1; -- still a comment ;\nSELECT 2",
			kinds: []StatementKind{KindDML, KindDML},
			sqls:  []string{"SELECT 1", "-- still a comment ;\nSELECT 2"},
		},
		{
			name:  "semicolon in block comment",
			sql:   "SELECT 1 /* ; */ ; SELECT 2;",
			kinds: []StatementKind{KindDML, KindDML},
			sqls:  []string{"SELECT 1 /* ; */", "SELECT 2"},
		},
		{
			name:  "missing final semicolon",
			sql:   "SELECT 1",
			kinds: []StatementKind{KindDML},
			sqls:  []string{"SELECT 1"},
		},
		{
			name:  "with is dml",
			sql:   "WITH c AS (SELECT 1) SELECT * FROM c",
			kinds: []StatementKind{KindDML},
		},
		{
			name:  "ddl then dml",
			sql:   "CREATE TABLE t (id int); INSERT INTO t (id) VALUES (1)",
			kinds: []StatementKind{KindDDL, KindDML},
		},
		{
			name:  "tagged dollar quote",
			sql:   "CREATE FUNCTION f() RETURNS void AS $body$ BEGIN PERFORM 1; END $body$ LANGUAGE plpgsql",
			kinds: []StatementKind{KindDDL},
		},
		{
			name:  "semicolon in quoted identifier",
			sql:   `SELECT "a;b"`,
			kinds: []StatementKind{KindDML},
			sqls:  []string{`SELECT "a;b"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := SplitStatements(tt.sql)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.kinds) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.kinds), got)
			}
			for i := range got {
				if got[i].Index != i {
					t.Fatalf("index = %d, want %d", got[i].Index, i)
				}
				if got[i].Kind != tt.kinds[i] {
					t.Fatalf("kind = %s, want %s", got[i].Kind, tt.kinds[i])
				}
				if tt.sqls != nil && got[i].SQL != tt.sqls[i] {
					t.Fatalf("sql = %q, want %q", got[i].SQL, tt.sqls[i])
				}
			}
		})
	}
}

func TestValidateSQL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		sql     string
		wantErr error
	}{
		{name: "begin", sql: "BEGIN", wantErr: ErrRejectedSQL},
		{name: "commit", sql: "COMMIT", wantErr: ErrRejectedSQL},
		{name: "rollback", sql: "ROLLBACK", wantErr: ErrRejectedSQL},
		{name: "savepoint", sql: "SAVEPOINT s", wantErr: ErrRejectedSQL},
		{name: "concurrently", sql: "CREATE UNIQUE INDEX CONCURRENTLY idx ON t (id)", wantErr: ErrRejectedSQL},
		{name: "vacuum", sql: "VACUUM ANALYZE t", wantErr: ErrRejectedSQL},
		{name: "create database", sql: "CREATE DATABASE other", wantErr: ErrRejectedSQL},
		{name: "begin in string", sql: "SELECT 'BEGIN'"},
		{name: "commit in line comment", sql: "SELECT 1 -- COMMIT"},
		{name: "commit in block comment", sql: "SELECT 1 /* COMMIT; VACUUM */"},
		{name: "concurrently in string", sql: "SELECT 'CREATE INDEX CONCURRENTLY'"},
		{name: "plain index", sql: "CREATE INDEX idx ON t (id)"},
		{name: "create table", sql: "CREATE TABLE t (id int)"},
		{name: "function body commit", sql: "CREATE FUNCTION f() RETURNS void AS $$ BEGIN COMMIT; END $$ LANGUAGE plpgsql"},
		{name: "second statement begin", sql: "SELECT 1; BEGIN", wantErr: ErrRejectedSQL},
		{name: "vacuum in string", sql: "SELECT 'VACUUM'"},
		{name: "create database in string", sql: "SELECT 'CREATE DATABASE other'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateSQL(tt.sql)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSplitStatements_Unclosed(t *testing.T) {
	t.Parallel()
	for _, sql := range []string{
		"SELECT 'ab",
		`SELECT "ab`,
		"SELECT 1 /* never",
		"SELECT $tag$ never",
	} {
		t.Run(sql, func(t *testing.T) {
			t.Parallel()
			if _, err := SplitStatements(sql); err == nil {
				t.Fatal("expected an unclosed quote or comment error")
			}
			err := validateSQL(sql)
			fe, ok := apperr.FieldErrors(err)
			if !ok || fe["sql_text"] == "" {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestValidateSQL_SizeLimit(t *testing.T) {
	t.Parallel()
	exact := "SELECT 1" + strings.Repeat(" ", maxSQLBytes-len("SELECT 1"))
	if len(exact) != maxSQLBytes {
		t.Fatalf("len = %d", len(exact))
	}
	if err := validateSQL(exact); err != nil {
		t.Fatal(err)
	}
	err := validateSQL(exact + " ")
	fe, ok := apperr.FieldErrors(err)
	if !ok || fe["sql_text"] == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestNextVersion(t *testing.T) {
	t.Parallel()
	if got := NextVersion(0); got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
	if got := NextVersion(4); got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
}
