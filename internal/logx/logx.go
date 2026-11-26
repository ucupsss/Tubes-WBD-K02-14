// Package logx menyediakan logger JSON terstruktur untuk seluruh aplikasi (Milestone 2).
//
// Keluaran satu baris JSON per event ke stdout/stderr. Nama event ditulis di field "event"
// (pesan slog). Atribut baku yang selalu ikut bila ada di context: correlation_id, actor, database,
// trace_id, span_id. Nilai sensitif (password, token, session, cookie, DSN, ...) disamarkan.
//
// Pemilik: A.
package logx

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

// Redacted adalah pengganti nilai sensitif di log.
const Redacted = "[REDACTED]"

// MaxValueLen adalah panjang maksimum nilai string di log sebelum dipotong.
const MaxValueLen = 256

// sensitiveKeys: nama atribut yang nilainya selalu disamarkan (tanpa memedulikan huruf besar/kecil).
// Dicocokkan sebagai substring supaya "new_password" atau "x-csrf-token" ikut tertangkap.
var sensitiveKeys = []string{
	"password", "passwd", "secret", "token", "session", "cookie",
	"authorization", "dsn", "csrf", "api_key", "apikey",
}

var dsnPattern = regexp.MustCompile(`(?i)(postgres(?:ql)?|mysql|mongodb|redis|amqp)://[^\s"']+`)

// TraceExtractor mengambil trace_id dan span_id dari context. Diisi paket telemetry (B)
// lewat SetTraceExtractor; selama belum diisi, field trace tidak ditulis.
type TraceExtractor func(ctx context.Context) (traceID, spanID string)

var traceExtractor TraceExtractor

// SetTraceExtractor mendaftarkan pembaca trace context. Dipanggil sekali saat startup.
func SetTraceExtractor(fn TraceExtractor) { traceExtractor = fn }

// New membuat logger JSON ke w dengan redaksi dan atribut context. Pesan slog menjadi field "event".
func New(w io.Writer, level slog.Level) *slog.Logger {
	inner := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
	return slog.New(&ctxHandler{inner: inner})
}

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.MessageKey:
		a.Key = "event"
		return a
	case slog.TimeKey, slog.LevelKey:
		return a
	}
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	if a.Value.Kind() == slog.KindString {
		if a.Key == "query" {
			return slog.String(a.Key, Truncate(redactNoTruncate(a.Value.String()), 2000))
		}
		return slog.String(a.Key, RedactString(a.Value.String()))
	}
	if a.Value.Kind() == slog.KindAny {
		if err, ok := a.Value.Any().(error); ok {
			return slog.String(a.Key, RedactString(err.Error()))
		}
	}
	return a
}

// IsSensitiveKey melaporkan apakah nama atribut termasuk yang harus disamarkan.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// kvSecretPattern menangkap pasangan rahasia gaya libpq/konfigurasi, mis. "password=abc" atau
// "token: 'abc'", termasuk di dalam pesan error bebas.
var kvSecretPattern = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api_key|session|cookie|csrf|dsn|authorization)(\s*[=:]\s*)('[^']*'|"[^"]*"|[^\s,;&]+)`)

func redactNoTruncate(s string) string {
	s = dsnPattern.ReplaceAllStringFunc(s, func(m string) string {
		scheme := m[:strings.Index(m, "://")+3]
		return scheme + Redacted
	})
	return kvSecretPattern.ReplaceAllString(s, "${1}${2}"+Redacted)
}

// RedactString menyamarkan DSN (user:password@host) serta pasangan rahasia key=value, lalu memotong
// string yang terlalu panjang.
func RedactString(s string) string {
	return Truncate(redactNoTruncate(s), MaxValueLen)
}

// Truncate memotong s menjadi paling banyak n karakter (rune), diberi tanda … bila terpotong.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ctxHandler menambahkan atribut baku dari context ke setiap record.
type ctxHandler struct {
	inner slog.Handler
}

func (h *ctxHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := CorrelationFrom(ctx); id != "" {
		r.AddAttrs(slog.String("correlation_id", id))
	}
	if a := ActorFrom(ctx); a != "" {
		r.AddAttrs(slog.String("actor", a))
	}
	if d := DatabaseFrom(ctx); d != "" {
		r.AddAttrs(slog.String("database", d))
	}
	if traceExtractor != nil {
		if tid, sid := traceExtractor(ctx); tid != "" {
			r.AddAttrs(slog.String("trace_id", tid))
			if sid != "" {
				r.AddAttrs(slog.String("span_id", sid))
			}
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ctxHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *ctxHandler) WithGroup(name string) slog.Handler {
	return &ctxHandler{inner: h.inner.WithGroup(name)}
}
