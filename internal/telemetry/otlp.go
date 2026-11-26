package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newOTLPTraceExporter membuat exporter trace OTLP/HTTP resmi (protobuf) menuju OpenTelemetry Collector.
// Menerima endpoint dengan atau tanpa skema, mis. "http://localhost:4318" atau "localhost:4318".
func newOTLPTraceExporter(ctx context.Context, rawEndpoint string) (sdktrace.SpanExporter, error) {
	ep := strings.TrimSpace(rawEndpoint)
	if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		ep = "http://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil {
		return nil, err
	}
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(u.Host),
	}
	if u.Scheme == "http" {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" {
		if !strings.HasSuffix(p, "/v1/traces") {
			p += "/v1/traces"
		}
		opts = append(opts, otlptracehttp.WithURLPath(p))
	}
	return otlptracehttp.New(ctx, opts...)
}

// LogForwarder meneruskan setiap baris JSON log yang ditulis oleh logx ke OpenTelemetry Collector
// (/v1/logs) secara asinkron tanpa menghambat stdout. Seluruh log masuk melalui Collector
// (bukan langsung ke Loki) agar jalur observability tunggal dan tidak menggandakan log.
type LogForwarder struct {
	otlpLogsURL string
	serviceName string
	client      *http.Client
	ch          chan []byte
	mu          sync.RWMutex
	closed      bool
	wg          sync.WaitGroup
	once        sync.Once
}

// NewLogForwarder membuat penulis log asinkron ke OTel Collector (otlpEndpoint/v1/logs).
func NewLogForwarder(serviceName, otlpEndpoint string) *LogForwarder {
	otlpLogs := strings.TrimRight(strings.TrimSpace(otlpEndpoint), "/")
	if otlpLogs != "" {
		if !strings.HasSuffix(otlpLogs, "/v1/logs") {
			otlpLogs += "/v1/logs"
		}
		if !strings.HasPrefix(otlpLogs, "http://") && !strings.HasPrefix(otlpLogs, "https://") {
			otlpLogs = "http://" + otlpLogs
		}
	}
	if serviceName == "" {
		serviceName = "dbmp-server"
	}
	f := &LogForwarder{
		otlpLogsURL: otlpLogs,
		serviceName: serviceName,
		client:      &http.Client{Timeout: 3 * time.Second},
		ch:          make(chan []byte, 512),
	}
	f.wg.Add(1)
	go f.loop()
	return f
}

// Write memenuhi io.Writer sehingga dapat digabungkan dengan io.MultiWriter(os.Stdout, forwarder).
func (f *LogForwarder) Write(p []byte) (int, error) {
	if f == nil || f.otlpLogsURL == "" {
		return len(p), nil
	}
	cp := bytes.TrimSpace(append([]byte(nil), p...))
	if len(cp) == 0 {
		return len(p), nil
	}
	f.mu.RLock()
	if !f.closed {
		select {
		case f.ch <- cp:
		default:
			// Jangan memblokir aplikasi utama bila buffer penuh.
		}
	}
	f.mu.RUnlock()
	return len(p), nil
}

// Close menguras antrean log yang tersisa dan menutup worker pengirim.
func (f *LogForwarder) Close() error {
	if f == nil {
		return nil
	}
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		close(f.ch)
		f.mu.Unlock()
		f.wg.Wait()
	})
	return nil
}

func (f *LogForwarder) loop() {
	defer f.wg.Done()
	for line := range f.ch {
		f.ship(line)
	}
}

func (f *LogForwarder) ship(line []byte) {
	if f.otlpLogsURL == "" {
		return
	}
	var parsed map[string]any
	_ = json.Unmarshal(line, &parsed)

	level, _ := parsed["level"].(string)
	if level == "" {
		level = "INFO"
	}
	event, _ := parsed["event"].(string)
	if event == "" {
		event = "app"
	}
	traceID, _ := parsed["trace_id"].(string)
	spanID, _ := parsed["span_id"].(string)
	corrID, _ := parsed["correlation_id"].(string)
	nowNano := strconv.FormatInt(time.Now().UnixNano(), 10)

	attrs := []map[string]any{
		{"key": "event", "value": map[string]any{"stringValue": event}},
	}
	if corrID != "" {
		attrs = append(attrs, map[string]any{
			"key":   "correlation_id",
			"value": map[string]any{"stringValue": corrID},
		})
	}

	rec := map[string]any{
		"timeUnixNano":         nowNano,
		"observedTimeUnixNano": nowNano,
		"severityNumber":       severityNumber(level),
		"severityText":         level,
		"body":                 map[string]any{"stringValue": string(line)},
		"attributes":           attrs,
	}
	if len(traceID) == 32 {
		rec["traceId"] = traceID
	}
	if len(spanID) == 16 {
		rec["spanId"] = spanID
	}

	payload := map[string]any{
		"resourceLogs": []map[string]any{
			{
				"resource": map[string]any{
					"attributes": []map[string]any{
						{"key": "service.name", "value": map[string]any{"stringValue": f.serviceName}},
					},
				},
				"scopeLogs": []map[string]any{
					{
						"scope":      map[string]any{"name": tracerName},
						"logRecords": []map[string]any{rec},
					},
				},
			},
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, f.otlpLogsURL, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := f.client.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func severityNumber(level string) int {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return 5
	case "INFO":
		return 9
	case "WARN", "WARNING":
		return 13
	case "ERROR":
		return 17
	default:
		return 9
	}
}
