package telemetry

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
)

func TestInjectExtractAndLogCorrelation(t *testing.T) {
	var spanBuf, logBuf bytes.Buffer
	shutdown, err := Init(context.Background(), Config{
		ServiceName: "test-svc",
		Writer:      &spanBuf,
		Sync:        true,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	logger := logx.New(&logBuf, slog.LevelInfo)

	ctx := logx.WithCorrelation(context.Background(), "corr-abc-123")
	ctx, parentSpan := Tracer().Start(ctx, "queue.enqueue")
	parentTraceID := parentSpan.SpanContext().TraceID().String()
	parentSpanID := parentSpan.SpanContext().SpanID().String()

	if parentTraceID == "" || parentSpanID == "" {
		t.Fatal("trace_id atau span_id kosong pada parent span")
	}

	carrier := Inject(ctx)
	parentSpan.End()

	if carrier["traceparent"] == "" {
		t.Fatalf("carrier harus memuat traceparent: %v", carrier)
	}
	if carrier["correlation_id"] != "corr-abc-123" {
		t.Fatalf("correlation_id = %q, want corr-abc-123", carrier["correlation_id"])
	}

	// Di sisi worker: Extract memulihkan parent trace dan correlation_id.
	workerCtx := Extract(context.Background(), carrier)
	if got := logx.CorrelationFrom(workerCtx); got != "corr-abc-123" {
		t.Fatalf("CorrelationFrom(workerCtx) = %q, want corr-abc-123", got)
	}

	workerCtx, childSpan := Tracer().Start(workerCtx, "job.process")
	childTraceID := childSpan.SpanContext().TraceID().String()
	childSpanID := childSpan.SpanContext().SpanID().String()

	if childTraceID != parentTraceID {
		t.Fatalf("child trace_id = %s, want sama dengan parent %s", childTraceID, parentTraceID)
	}
	if childSpanID == parentSpanID {
		t.Fatal("child span_id harus berbeda dari parent span_id")
	}

	// Log di dalam span harus otomatis berisi trace_id, span_id, dan correlation_id.
	logger.InfoContext(workerCtx, "job.claimed", "job_id", 42)
	childSpan.End()

	var logEntry map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &logEntry); err != nil {
		t.Fatalf("unmarshal log: %v\nraw: %s", err, logBuf.String())
	}
	if logEntry["trace_id"] != parentTraceID {
		t.Errorf("log trace_id = %v, want %s", logEntry["trace_id"], parentTraceID)
	}
	if logEntry["span_id"] != childSpanID {
		t.Errorf("log span_id = %v, want %s", logEntry["span_id"], childSpanID)
	}
	if logEntry["correlation_id"] != "corr-abc-123" {
		t.Errorf("log correlation_id = %v, want corr-abc-123", logEntry["correlation_id"])
	}
}

func TestHTTPMiddleware(t *testing.T) {
	var spanBuf bytes.Buffer
	shutdown, err := Init(context.Background(), Config{
		ServiceName: "test-http",
		Writer:      &spanBuf,
		Sync:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	var capturedTraceID, capturedSpanID string
	h := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTraceID, capturedSpanID = ExtractTraceIDs(r.Context())
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"job_id":1}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/executions/10/enqueue", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if capturedTraceID == "" || capturedSpanID == "" {
		t.Fatalf("trace_id=%q span_id=%q harus terisi di dalam handler", capturedTraceID, capturedSpanID)
	}
	if !strings.Contains(spanBuf.String(), "POST /api/executions/10/enqueue") {
		t.Fatalf("span output tidak memuat nama span HTTP: %s", spanBuf.String())
	}
}

func TestPgxTracer_LogsQueryAndSQLStateWithRedaction(t *testing.T) {
	var spanBuf, logBuf bytes.Buffer
	shutdown, err := Init(context.Background(), Config{
		ServiceName: "test-pgx",
		Writer:      &spanBuf,
		Sync:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	oldDefault := slog.Default()
	slog.SetDefault(logx.New(&logBuf, slog.LevelInfo))
	defer slog.SetDefault(oldDefault)

	tracer := PgxTracer()
	ctx := logx.WithActor(logx.WithDatabase(context.Background(), "7"), "daniel")
	ctx = WithSQLMeta(ctx, SQLMeta{
		MigrationID: 11,
		ExecutionID: 22,
		JobID:       33,
		Attempt:     1,
		BatchNo:     2,
	})

	// 1. Query sukses
	qCtx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{
		SQL:  "UPDATE big_events SET v = v + 1 WHERE id > 100 AND id <= 200",
		Args: []any{100, 200},
	})
	tracer.TraceQueryEnd(qCtx, nil, pgx.TraceQueryEndData{
		CommandTag: pgconn.NewCommandTag("UPDATE 100"),
	})

	// 2. Query gagal dengan SQLSTATE dan menyentuh tabel sensitif users
	errCtx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{
		SQL:  "INSERT INTO users (username, password_hash) VALUES ($1, $2)",
		Args: []any{"alice", "rahasia-super-penting"},
	})
	pgErr := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	tracer.TraceQueryEnd(errCtx, nil, pgx.TraceQueryEndData{
		Err: pgErr,
	})

	// 3. Query gagal dengan error biasa berisi DSN
	dsnCtx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{
		SQL: "SELECT 1",
	})
	tracer.TraceQueryEnd(dsnCtx, nil, pgx.TraceQueryEndData{
		Err: errors.New("connect failed to postgres://user:secretpass123@localhost:5432/db"),
	})

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("jumlah baris log = %d, want 3:\n%s", len(lines), logBuf.String())
	}

	var first, second, third map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["event"] != "sql.exec" || first["status"] != "ok" || first["rows"] != float64(100) {
		t.Fatalf("first log = %v", first)
	}
	if first["execution_id"] != float64(22) || first["batch_no"] != float64(2) || first["trace_id"] == "" {
		t.Fatalf("metadata atau trace_id hilang di first log: %v", first)
	}

	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second["status"] != "error" || second["sqlstate"] != "23505" {
		t.Fatalf("second log harus mencatat sqlstate 23505: %v", second)
	}
	if strings.Contains(lines[1], "rahasia-super-penting") {
		t.Fatalf("parameter tabel users bocor di log: %s", lines[1])
	}

	if err := json.Unmarshal([]byte(lines[2]), &third); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(lines[2], "secretpass123") {
		t.Fatalf("password DSN bocor di log: %s", lines[2])
	}
}
