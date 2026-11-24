package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// otlpHTTPExporter mengirim span ke OpenTelemetry Collector melalui protokol OTLP/HTTP JSON (/v1/traces).
// Diimplementasikan menggunakan net/http standar agar tidak menambah ketergantungan gRPC/protobuf eksternal.
type otlpHTTPExporter struct {
	endpoint    string
	serviceName string
	client      *http.Client
}

func newOTLPHTTPExporter(endpoint, serviceName string) *otlpHTTPExporter {
	ep := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if !strings.HasSuffix(ep, "/v1/traces") {
		ep += "/v1/traces"
	}
	if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		ep = "http://" + ep
	}
	return &otlpHTTPExporter{
		endpoint:    ep,
		serviceName: serviceName,
		client:      &http.Client{Timeout: 5 * time.Second},
	}
}

func (e *otlpHTTPExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	payload := buildOTLPTracePayload(e.serviceName, spans)
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("otlp trace export status %d", resp.StatusCode)
	}
	return nil
}

func (e *otlpHTTPExporter) Shutdown(context.Context) error {
	return nil
}

func buildOTLPTracePayload(serviceName string, spans []sdktrace.ReadOnlySpan) map[string]any {
	otlpSpans := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		sc := s.SpanContext()
		parentID := ""
		if s.Parent().IsValid() {
			parentID = s.Parent().SpanID().String()
		}
		attrs := make([]map[string]any, 0, len(s.Attributes()))
		for _, kv := range s.Attributes() {
			attrs = append(attrs, map[string]any{
				"key":   string(kv.Key),
				"value": map[string]any{"stringValue": kv.Value.Emit()},
			})
		}
		otlpSpans = append(otlpSpans, map[string]any{
			"traceId":           sc.TraceID().String(),
			"spanId":            sc.SpanID().String(),
			"parentSpanId":      parentID,
			"name":              s.Name(),
			"kind":              int(s.SpanKind()),
			"startTimeUnixNano": strconv.FormatInt(s.StartTime().UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(s.EndTime().UnixNano(), 10),
			"attributes":        attrs,
			"status": map[string]any{
				"code":    int(s.Status().Code),
				"message": s.Status().Description,
			},
		})
	}
	return map[string]any{
		"resourceSpans": []map[string]any{
			{
				"resource": map[string]any{
					"attributes": []map[string]any{
						{
							"key":   "service.name",
							"value": map[string]any{"stringValue": serviceName},
						},
					},
				},
				"scopeSpans": []map[string]any{
					{
						"scope": map[string]any{"name": tracerName},
						"spans": otlpSpans,
					},
				},
			},
		},
	}
}

// LogForwarder meneruskan setiap baris JSON log yang ditulis oleh logx ke OpenTelemetry Collector
// (/v1/logs) atau Loki (/loki/api/v1/push) secara asinkron tanpa menghambat stdout.
type LogForwarder struct {
	otlpLogsURL string
	lokiPushURL string
	serviceName string
	client      *http.Client
	ch          chan []byte
	wg          sync.WaitGroup
	once        sync.Once
}

// NewLogForwarder membuat penulis log asinkron ke OTel Collector (otlpEndpoint/v1/logs) dan/atau Loki.
func NewLogForwarder(serviceName, otlpEndpoint, lokiURL string) *LogForwarder {
	otlpLogs := strings.TrimRight(strings.TrimSpace(otlpEndpoint), "/")
	if otlpLogs != "" {
		if !strings.HasSuffix(otlpLogs, "/v1/logs") {
			otlpLogs += "/v1/logs"
		}
		if !strings.HasPrefix(otlpLogs, "http://") && !strings.HasPrefix(otlpLogs, "https://") {
			otlpLogs = "http://" + otlpLogs
		}
	}
	lokiPush := strings.TrimRight(strings.TrimSpace(lokiURL), "/")
	if lokiPush != "" {
		if !strings.HasSuffix(lokiPush, "/loki/api/v1/push") {
			lokiPush += "/loki/api/v1/push"
		}
		if !strings.HasPrefix(lokiPush, "http://") && !strings.HasPrefix(lokiPush, "https://") {
			lokiPush = "http://" + lokiPush
		}
	}
	if serviceName == "" {
		serviceName = "dbmp-server"
	}
	f := &LogForwarder{
		otlpLogsURL: otlpLogs,
		lokiPushURL: lokiPush,
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
	if f == nil || (f.otlpLogsURL == "" && f.lokiPushURL == "") {
		return len(p), nil
	}
	cp := bytes.TrimSpace(append([]byte(nil), p...))
	if len(cp) == 0 {
		return len(p), nil
	}
	select {
	case f.ch <- cp:
	default:
		// Jangan memblokir aplikasi utama bila buffer penuh.
	}
	return len(p), nil
}

// Close menguras antrean log yang tersisa dan menutup worker pengirim.
func (f *LogForwarder) Close() error {
	if f == nil {
		return nil
	}
	f.once.Do(func() {
		close(f.ch)
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

	if f.otlpLogsURL != "" {
		rec := map[string]any{
			"timeUnixNano": nowNano,
			"severityText": level,
			"body":         map[string]any{"stringValue": string(line)},
			"attributes": []map[string]any{
				{"key": "event", "value": map[string]any{"stringValue": event}},
				{"key": "correlation_id", "value": map[string]any{"stringValue": corrID}},
			},
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
		if raw, err := json.Marshal(payload); err == nil {
			req, err := http.NewRequest(http.MethodPost, f.otlpLogsURL, bytes.NewReader(raw))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				if resp, err := f.client.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}
		}
	}

	if f.lokiPushURL != "" {
		// Label Loki dibatasi ketat (service, level) tanpa user_id, job_id, atau SQL sebagai label
		// sesuai panduan bagian 6 untuk mencegah ledakan kardinalitas.
		lokiPayload := map[string]any{
			"streams": []map[string]any{
				{
					"stream": map[string]string{
						"service": f.serviceName,
						"level":   level,
					},
					"values": [][]string{
						{nowNano, string(line)},
					},
				},
			},
		}
		if raw, err := json.Marshal(lokiPayload); err == nil {
			req, err := http.NewRequest(http.MethodPost, f.lokiPushURL, bytes.NewReader(raw))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				if resp, err := f.client.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}
		}
	}
}
