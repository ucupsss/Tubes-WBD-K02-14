//go:build integration

package migration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/telemetry"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// observe memasang perekam span dan log JSON untuk satu test, lalu memulihkannya.
func observe(t *testing.T) (*tracetest.SpanRecorder, *bytes.Buffer) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	var buf bytes.Buffer
	prevLog := slog.Default()
	slog.SetDefault(logx.New(&buf, slog.LevelDebug))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		slog.SetDefault(prevLog)
	})
	return rec, &buf
}

func spanNamed(rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	for _, s := range rec.Ended() {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func logEvents(t *testing.T, buf *bytes.Buffer, event string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m["event"] == event {
			out = append(out, m)
		}
	}
	return out
}

// Enqueue lewat API membuka span queue.enqueue, dan worker melanjutkan trace yang sama
// sampai target.exec dan platform.update_history; commit dicatat sebagai event tersendiri.
func TestTracing_EnqueueToWorkerIsOneTraceWithCommitEvent(t *testing.T) {
	fx, _ := targetFixture(t)
	rec, buf := observe(t)
	m := create(t, fx, "Trace", "CREATE TABLE traced (id int)", "DROP TABLE traced", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)

	h := migration.NewHandler(fx.srv.Service, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/executions/"+strconv.FormatInt(exec.ID, 10)+"/enqueue", nil)
	req.SetPathValue("execID", strconv.FormatInt(exec.ID, 10))
	req = req.WithContext(authz.WithPrincipal(req.Context(), fx.editor))
	rw := httptest.NewRecorder()
	h.Enqueue(rw, req)
	if rw.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d %s", rw.Code, rw.Body.String())
	}
	runQueued(t, fx, "worker-trace")

	enq := spanNamed(rec, "queue.enqueue")
	if enq == nil {
		t.Fatal("span queue.enqueue tidak ada")
	}
	traceID := enq.SpanContext().TraceID()
	for _, name := range []string{"job.process", "revalidate", "lock.acquire", "target.exec", "platform.update_history"} {
		s := spanNamed(rec, name)
		if s == nil {
			t.Fatalf("span %s tidak ada", name)
		}
		if s.SpanContext().TraceID() != traceID {
			t.Fatalf("span %s di trace lain: %s vs %s", name, s.SpanContext().TraceID(), traceID)
		}
	}
	if job := spanNamed(rec, "job.process"); job.Parent().SpanID() != enq.SpanContext().SpanID() {
		t.Fatal("job.process harus anak dari queue.enqueue")
	}

	commits := logEvents(t, buf, "commit")
	if len(commits) != 1 || commits[0]["status"] != "ok" || commits[0]["trace_id"] != traceID.String() {
		t.Fatalf("event commit = %v", commits)
	}
	if len(logEvents(t, buf, "rollback")) != 0 {
		t.Fatal("tidak boleh ada event rollback untuk eksekusi sukses")
	}
}

func TestTracing_FailedStatementLogsRollbackAndMarksSpan(t *testing.T) {
	fx, _ := targetFixture(t)
	rec, buf := observe(t)
	m := create(t, fx, "Gagal", "SELECT 1 / 0", "SELECT 1", false, "")
	exec := submit(t, fx, m.ID)
	approve(t, fx, exec.ID)
	setExecStatus(t, fx, exec.ID, migration.StatusQueued)

	err := executor(t, fx).Run(context.Background(), jobOf(exec.ID, fx.dbID))
	if !isHalt(err, worker.DispositionDead) {
		t.Fatalf("run = %v", err)
	}
	span := spanNamed(rec, "target.exec")
	if span == nil || span.Status().Code != codes.Error {
		t.Fatalf("target.exec harus berstatus error: %+v", span)
	}
	rollbacks := logEvents(t, buf, "rollback")
	if len(rollbacks) != 1 || rollbacks[0]["sqlstate"] != "22012" {
		t.Fatalf("event rollback = %v", rollbacks)
	}
	if len(logEvents(t, buf, "commit")) != 0 {
		t.Fatal("tidak boleh ada event commit untuk eksekusi gagal")
	}
}

func TestTelemetryEnd_RecordsRedactedError(t *testing.T) {
	rec, _ := observe(t)
	_, span := telemetry.Tracer().Start(context.Background(), "x")
	telemetry.End(span, &queue.SQLError{SQLState: "08006", Message: "password=hunter2 gagal"})
	got := spanNamed(rec, "x")
	if got.Status().Code != codes.Error || strings.Contains(got.Status().Description, "hunter2") {
		t.Fatalf("status = %+v", got.Status())
	}
	for _, ev := range got.Events() {
		for _, a := range ev.Attributes {
			if strings.Contains(a.Value.Emit(), "hunter2") {
				t.Fatalf("event span membocorkan credential: %v", a)
			}
		}
	}
}
