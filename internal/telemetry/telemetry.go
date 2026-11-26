// Package telemetry menyediakan OpenTelemetry distributed tracing, propagasi konteks
// lintas queue (Inject/Extract), HTTP middleware, dan pgx.QueryTracer yang mencatat
// setiap eksekusi SQL sebagai span sekaligus event log terstruktur lewat logx.SQL.
//
// Pemilik: B (Daniel).
package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/ucupsss/Tubes-WBD-K02-14"

// Config adalah konfigurasi inisialisasi OpenTelemetry SDK.
type Config struct {
	ServiceName string
	// Writer adalah tujuan exporter console/stdout. Bila nil dan Disabled=false, menggunakan os.Stdout.
	Writer io.Writer
	// OTLPEndpoint adalah alamat OpenTelemetry Collector HTTP (mis. "http://localhost:4318").
	// Bila kosong, dibaca dari env OTEL_EXPORTER_OTLP_ENDPOINT.
	OTLPEndpoint string
	// Disabled menonaktifkan penulisan span ke stdout (mis. di unit test yang hanya butuh trace_id/span_id di memori).
	Disabled bool
	// Sync mengekspor span secara sinkron (SimpleSpanProcessor), berguna untuk pengujian deterministik.
	Sync bool
}

func init() {
	// Daftarkan extractor sejak awal supaya walaupun Init belum dipanggil (mis. dengan custom TracerProvider),
	// logx selalu menyertakan trace_id dan span_id dari span aktif di context.
	logx.SetTraceExtractor(ExtractTraceIDs)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// ExtractTraceIDs mengambil trace_id dan span_id dari span aktif pada context.
func ExtractTraceIDs(ctx context.Context) (traceID, spanID string) {
	if ctx == nil {
		return "", ""
	}
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}

// Init menginisialisasi OpenTelemetry TracerProvider (exporter console/stdout), propagator W3C TraceContext,
// dan mengaitkan trace extractor ke paket logx.
func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	logx.SetTraceExtractor(ExtractTraceIDs)

	svcName := cfg.ServiceName
	if svcName == "" {
		svcName = "dbmp-server"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(svcName)),
	)
	if err != nil {
		return nil, err
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	}

	w := cfg.Writer
	if w == nil {
		if cfg.Disabled {
			w = io.Discard
		} else {
			w = os.Stdout
		}
	}

	exp, err := stdouttrace.New(stdouttrace.WithWriter(w))
	if err != nil {
		return nil, err
	}

	if cfg.Sync {
		opts = append(opts, sdktrace.WithSyncer(exp))
	} else {
		opts = append(opts, sdktrace.WithBatcher(exp))
	}

	otlpEP := cfg.OTLPEndpoint
	if otlpEP == "" {
		otlpEP = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if otlpEP != "" {
		otlpExp := newOTLPHTTPExporter(otlpEP, svcName)
		if cfg.Sync {
			opts = append(opts, sdktrace.WithSyncer(otlpExp))
		} else {
			opts = append(opts, sdktrace.WithBatcher(otlpExp))
		}
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// LogOutput mengembalikan io.Writer untuk logx.New yang menulis ke base (mis. os.Stdout)
// dan, apabila OTEL_EXPORTER_OTLP_ENDPOINT atau LOKI_PUSH_URL diset, juga meneruskan log JSON
// secara asinkron ke OpenTelemetry Collector / Loki untuk korelasi log-trace.
func LogOutput(base io.Writer, serviceName string) (io.Writer, func() error) {
	otlpEP := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	lokiURL := os.Getenv("LOKI_PUSH_URL")
	if otlpEP == "" && lokiURL == "" {
		return base, func() error { return nil }
	}
	fwd := NewLogForwarder(serviceName, otlpEP, lokiURL)
	return io.MultiWriter(base, fwd), fwd.Close
}

// Tracer mengembalikan tracer utama aplikasi.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// Inject menyalin trace context (W3C traceparent/tracestate) dan correlation_id dari ctx ke map
// untuk disimpan di migration_jobs.trace_context saat enqueue.
func Inject(ctx context.Context) map[string]string {
	carrier := make(map[string]string)
	if ctx == nil {
		return carrier
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(carrier))
	if cid := logx.CorrelationFrom(ctx); cid != "" {
		carrier["correlation_id"] = cid
	}
	return carrier
}

// Extract membaca trace context dan correlation_id dari map (migration_jobs.trace_context)
// ke dalam ctx supaya span worker menjadi child dari span API yang melakukan enqueue.
func Extract(ctx context.Context, carrier map[string]string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(carrier) == 0 {
		return ctx
	}
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(carrier))
	if cid := carrier["correlation_id"]; cid != "" && logx.CorrelationFrom(ctx) == "" {
		ctx = logx.WithCorrelation(ctx, cid)
	}
	return ctx
}

// HTTPMiddleware memulai root/child span untuk setiap request HTTP yang masuk,
// mencatat status code dan error bila status >= 500.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		spanName := r.Method + " " + r.URL.Path
		ctx, span := Tracer().Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.path", r.URL.Path),
			),
		)
		defer span.End()

		if cid := logx.CorrelationFrom(ctx); cid != "" {
			span.SetAttributes(attribute.String("correlation_id", cid))
		}

		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.status_code", rw.status))
		if rw.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rw.status))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(b)
}

// End menutup span dan, bila err tidak nil, mencatat error serta status Error pada span.
// Pesan error disamarkan dulu supaya credential tidak bocor ke backend tracing.
func End(span trace.Span, err error) {
	if err != nil {
		msg := logx.RedactString(err.Error())
		span.RecordError(errors.New(msg))
		span.SetStatus(codes.Error, msg)
	}
	span.End()
}
