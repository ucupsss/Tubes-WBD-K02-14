package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("bukan JSON: %v\n%s", err, buf.String())
	}
	return m
}

func TestNew_EventAndContextAttrs(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	ctx := WithCorrelation(WithActor(WithDatabase(context.Background(), "3"), "alice"), "corr-1")
	l.InfoContext(ctx, "migration.submitted", "execution_id", 7)

	m := decode(t, &buf)
	if m["event"] != "migration.submitted" {
		t.Fatalf("event = %v", m["event"])
	}
	if m["msg"] != nil {
		t.Fatalf("field msg seharusnya diganti event")
	}
	for k, want := range map[string]any{"actor": "alice", "database": "3", "correlation_id": "corr-1", "level": "INFO"} {
		if m[k] != want {
			t.Fatalf("%s = %v, want %v", k, m[k], want)
		}
	}
	if _, ok := m["time"]; !ok {
		t.Fatal("time hilang")
	}
}

func TestNew_Redaction(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	l.Info("x",
		"password", "hunter2",
		"NewPassword", "hunter3",
		"x-csrf-token", "abc",
		"session_id", "zzz",
		"dsn", "postgres://u:pw@h/db",
		"note", "konek ke postgres://user:secretpw@host:5432/db gagal",
		"ok", "aman",
	)
	out := buf.String()
	for _, leak := range []string{"hunter2", "hunter3", "abc", "zzz", "secretpw", "pw@h"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log membocorkan %q: %s", leak, out)
		}
	}
	m := decode(t, &buf)
	if m["ok"] != "aman" {
		t.Fatalf("nilai biasa berubah: %v", m["ok"])
	}
	if !strings.Contains(m["note"].(string), Redacted) {
		t.Fatalf("DSN di dalam teks tidak disamarkan: %v", m["note"])
	}
}

func TestNew_ErrorValueRedacted(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	l.Error("gagal", "err", errors.New("dial postgres://u:topsecret@h/db: refused"))
	if strings.Contains(buf.String(), "topsecret") {
		t.Fatalf("error membocorkan password: %s", buf.String())
	}
}

func TestTraceExtractor(t *testing.T) {
	SetTraceExtractor(func(context.Context) (string, string) { return "tid", "sid" })
	defer SetTraceExtractor(nil)

	var buf bytes.Buffer
	New(&buf, slog.LevelInfo).InfoContext(context.Background(), "x")
	m := decode(t, &buf)
	if m["trace_id"] != "tid" || m["span_id"] != "sid" {
		t.Fatalf("trace attrs = %v %v", m["trace_id"], m["span_id"])
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate(strings.Repeat("a", 300), 256); len([]rune(got)) != 257 {
		t.Fatalf("len = %d", len([]rune(got)))
	}
	if Truncate("pendek", 256) != "pendek" {
		t.Fatal("string pendek berubah")
	}
}

func TestCorrelationMiddleware(t *testing.T) {
	var seen string
	h := CorrelationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = CorrelationFrom(r.Context())
	}))

	t.Run("header valid dipakai", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(CorrelationHeader, "abc-123")
		h.ServeHTTP(rec, req)
		if seen != "abc-123" || rec.Header().Get(CorrelationHeader) != "abc-123" {
			t.Fatalf("seen=%q header=%q", seen, rec.Header().Get(CorrelationHeader))
		}
	})
	t.Run("header berbahaya diganti", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(CorrelationHeader, "x\",\"event\":\"audit.fake")
		h.ServeHTTP(rec, req)
		if strings.ContainsAny(seen, "\",") || seen == "" {
			t.Fatalf("id tidak disaring: %q", seen)
		}
	})
	t.Run("tanpa header dibuat baru", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if len(seen) != 32 {
			t.Fatalf("id baru = %q", seen)
		}
	})
}

func TestSQL_Event(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(New(&buf, slog.LevelInfo))
	defer slog.SetDefault(old)

	SQL(context.Background(), SQLEvent{
		Query:       "UPDATE t SET x = $1\n WHERE id > $2",
		Params:      []any{1, 10},
		Duration:    412 * time.Millisecond,
		Rows:        10000,
		ExecutionID: 34,
		Attempt:     1,
		BatchNo:     3,
	})
	m := decode(t, &buf)
	if m["event"] != "sql.exec" || m["status"] != "ok" || m["rows"].(float64) != 10000 || m["duration_ms"].(float64) != 412 {
		t.Fatalf("event = %v", m)
	}
	if m["query"] != "UPDATE t SET x = $1 WHERE id > $2" {
		t.Fatalf("query = %v", m["query"])
	}
	if m["execution_id"].(float64) != 34 || m["batch_no"].(float64) != 3 {
		t.Fatalf("id = %v", m)
	}
}

func TestSQL_ErrorHasSQLState(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(New(&buf, slog.LevelInfo))
	defer slog.SetDefault(old)

	SQL(context.Background(), SQLEvent{
		Query: "SELECT 1/0", Rows: -1,
		Err: &pgconn.PgError{Code: "22012", Message: "division by zero"},
	})
	m := decode(t, &buf)
	if m["status"] != "error" || m["sqlstate"] != "22012" || m["level"] != "WARN" {
		t.Fatalf("event = %v", m)
	}
	if _, has := m["rows"]; has {
		t.Fatal("rows tidak diketahui seharusnya tidak ditulis")
	}
}

func TestRedactParams_SensitiveTables(t *testing.T) {
	got := RedactParams("SELECT * FROM users WHERE username = $1", []any{"alice"})
	if got[0] != Redacted {
		t.Fatalf("param users tidak disamarkan: %v", got)
	}
	got = RedactParams("SELECT * FROM big_events WHERE id = $1", []any{42})
	if got[0] != "42" {
		t.Fatalf("param biasa = %v", got)
	}
	long := RedactParams("SELECT $1", []any{strings.Repeat("z", 500)})
	if len([]rune(long[0])) > 257 {
		t.Fatalf("param tidak dipotong: %d", len([]rune(long[0])))
	}
}

func TestAudit_WritesEvent(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(New(&buf, slog.LevelInfo))
	defer slog.SetDefault(old)

	ctx := WithActor(context.Background(), "leader1")
	Audit(ctx, "approve", slog.Int64("execution_id", 7))
	m := decode(t, &buf)
	if m["event"] != "audit.approve" || m["actor"] != "leader1" || m["execution_id"].(float64) != 7 {
		t.Fatalf("event = %v", m)
	}
}

func TestRedactString_KeyValueSecrets(t *testing.T) {
	cases := map[string]string{
		"host=db user=app password=hunter2 dbname=x": "host=db user=app password=[REDACTED] dbname=x",
		"gagal: PASSWORD = 'rahasia sekali'":         "gagal: PASSWORD = [REDACTED]",
		"token: abc123, lanjut":                      "token: [REDACTED], lanjut",
		"kolom password_hash tidak ada":              "kolom password_hash tidak ada",
	}
	for in, want := range cases {
		if got := RedactString(in); got != want {
			t.Errorf("RedactString(%q) = %q, want %q", in, got, want)
		}
	}
}
