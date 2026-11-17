package logx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// CorrelationHeader adalah header untuk ID korelasi: dibaca dari klien (bila valid) dan dikirim balik.
const CorrelationHeader = "X-Correlation-Id"

type ctxKey int

const (
	correlationKey ctxKey = iota
	actorKey
	databaseKey
	holderKey
)

// holder dibagi oleh seluruh lapisan satu request: middleware di dalam (session.Load) mengisi pelaku,
// dan logger di lapisan terluar membacanya setelah request selesai.
type holder struct{ actor string }

// SetActor mengisi pelaku di holder request (dibuat CorrelationMiddleware). Tidak berefek tanpa holder.
func SetActor(ctx context.Context, actor string) {
	if h, ok := ctx.Value(holderKey).(*holder); ok {
		h.actor = actor
	}
}

// WithCorrelation menyimpan ID korelasi di context.
func WithCorrelation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey, id)
}

// CorrelationFrom mengambil ID korelasi, atau string kosong.
func CorrelationFrom(ctx context.Context) string {
	s, _ := ctx.Value(correlationKey).(string)
	return s
}

// WithActor menyimpan pelaku (username atau "system") di context.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey, actor)
}

// ActorFrom mengambil pelaku, atau string kosong.
func ActorFrom(ctx context.Context) string {
	if s, _ := ctx.Value(actorKey).(string); s != "" {
		return s
	}
	if h, ok := ctx.Value(holderKey).(*holder); ok {
		return h.actor
	}
	return ""
}

// WithDatabase menyimpan ID/nama database target di context.
func WithDatabase(ctx context.Context, database string) context.Context {
	return context.WithValue(ctx, databaseKey, database)
}

// DatabaseFrom mengambil database target, atau string kosong.
func DatabaseFrom(ctx context.Context) string {
	s, _ := ctx.Value(databaseKey).(string)
	return s
}

// NewCorrelationID membuat ID korelasi acak (16 byte heksadesimal).
func NewCorrelationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// validCorrelationID menerima ID dari klien hanya bila pendek dan terdiri dari karakter aman,
// supaya tidak bisa dipakai menyisipkan baris log palsu.
func validCorrelationID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

// CorrelationMiddleware memastikan setiap request punya ID korelasi: dari header X-Correlation-Id
// bila valid, kalau tidak dibuat baru. ID dikirim balik di header respons dan masuk ke context.
func CorrelationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(CorrelationHeader)
		if !validCorrelationID(id) {
			id = NewCorrelationID()
		}
		w.Header().Set(CorrelationHeader, id)
		ctx := context.WithValue(WithCorrelation(r.Context(), id), holderKey, &holder{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
