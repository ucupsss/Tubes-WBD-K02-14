package telemetry

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type pgxCtxKey int

const (
	queryTraceKey pgxCtxKey = iota
	batchTraceKey
	sqlMetaKey
)

// SQLMeta menyimpan identitas eksekusi migrasi pada context sehingga setiap logx.SQL dan span
// db.query yang dipicu oleh PgxTracer otomatis mencantumkan migration_id, execution_id, job_id,
// attempt, dan batch_no.
type SQLMeta struct {
	MigrationID int64
	ExecutionID int64
	JobID       int64
	Attempt     int
	BatchNo     int
}

// WithSQLMeta menyematkan metadata eksekusi SQL ke dalam context.
func WithSQLMeta(ctx context.Context, meta SQLMeta) context.Context {
	return context.WithValue(ctx, sqlMetaKey, meta)
}

// SQLMetaFrom mengambil metadata eksekusi SQL dari context.
func SQLMetaFrom(ctx context.Context) SQLMeta {
	m, _ := ctx.Value(sqlMetaKey).(SQLMeta)
	return m
}

type queryTraceState struct {
	span  trace.Span
	start time.Time
	sql   string
	args  []any
}

type pgxTracer struct{}

// PgxTracer mengembalikan pgx.QueryTracer (sekaligus pgx.BatchTracer) yang dipasang di semua
// connection pool (platform maupun target). Setiap query membuat span "db.query" dan menulis
// satu baris log terstruktur "sql.exec" lewat logx.SQL dengan redaksi data sensitif.
func PgxTracer() pgx.QueryTracer {
	return &pgxTracer{}
}

func (t *pgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	meta := SQLMetaFrom(ctx)
	attrs := []attribute.KeyValue{
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", logx.Truncate(logx.RedactString(data.SQL), 1000)),
	}
	if db := logx.DatabaseFrom(ctx); db != "" {
		attrs = append(attrs, attribute.String("db.target", db))
	}
	if meta.MigrationID != 0 {
		attrs = append(attrs, attribute.Int64("migration_id", meta.MigrationID))
	}
	if meta.ExecutionID != 0 {
		attrs = append(attrs, attribute.Int64("execution_id", meta.ExecutionID))
	}
	if meta.JobID != 0 {
		attrs = append(attrs, attribute.Int64("job_id", meta.JobID))
	}
	if meta.Attempt != 0 {
		attrs = append(attrs, attribute.Int("attempt", meta.Attempt))
	}
	if meta.BatchNo != 0 {
		attrs = append(attrs, attribute.Int("batch_no", meta.BatchNo))
	}

	ctx, span := Tracer().Start(ctx, "db.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	state := &queryTraceState{
		span:  span,
		start: time.Now(),
		sql:   data.SQL,
		args:  data.Args,
	}
	return context.WithValue(ctx, queryTraceKey, state)
}

func (t *pgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	state, ok := ctx.Value(queryTraceKey).(*queryTraceState)
	if !ok || state == nil {
		return
	}
	dur := time.Since(state.start)
	rows := data.CommandTag.RowsAffected()

	state.span.SetAttributes(
		attribute.Int64("db.duration_ms", dur.Milliseconds()),
		attribute.Int64("db.rows_affected", rows),
	)
	if data.Err != nil {
		redacted := logx.RedactString(data.Err.Error())
		state.span.RecordError(errors.New(redacted))
		state.span.SetStatus(codes.Error, redacted)
		var pgErr *pgconn.PgError
		if errors.As(data.Err, &pgErr) {
			state.span.SetAttributes(attribute.String("db.sqlstate", pgErr.Code))
		}
	}
	state.span.End()

	meta := SQLMetaFrom(ctx)
	logx.SQL(ctx, logx.SQLEvent{
		Query:       state.sql,
		Params:      state.args,
		Duration:    dur,
		Rows:        rows,
		Err:         data.Err,
		MigrationID: meta.MigrationID,
		ExecutionID: meta.ExecutionID,
		JobID:       meta.JobID,
		Attempt:     meta.Attempt,
		BatchNo:     meta.BatchNo,
	})
}

type batchTraceState struct {
	span  trace.Span
	start time.Time
}

func (t *pgxTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	n := 0
	if data.Batch != nil {
		n = data.Batch.Len()
	}
	ctx, span := Tracer().Start(ctx, "db.batch",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.Int("db.batch_size", n),
		),
	)
	return context.WithValue(ctx, batchTraceKey, &batchTraceState{span: span, start: time.Now()})
}

func (t *pgxTracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	meta := SQLMetaFrom(ctx)
	logx.SQL(ctx, logx.SQLEvent{
		Query:       data.SQL,
		Params:      data.Args,
		Duration:    0,
		Rows:        data.CommandTag.RowsAffected(),
		Err:         data.Err,
		MigrationID: meta.MigrationID,
		ExecutionID: meta.ExecutionID,
		JobID:       meta.JobID,
		Attempt:     meta.Attempt,
		BatchNo:     meta.BatchNo,
	})
}

func (t *pgxTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	state, ok := ctx.Value(batchTraceKey).(*batchTraceState)
	if !ok || state == nil {
		return
	}
	state.span.SetAttributes(attribute.Int64("db.duration_ms", time.Since(state.start).Milliseconds()))
	if data.Err != nil {
		redacted := logx.RedactString(data.Err.Error())
		state.span.RecordError(errors.New(redacted))
		state.span.SetStatus(codes.Error, redacted)
	}
	state.span.End()
}
