package logx

import (
	"context"
	"log/slog"
)

// Audit mencatat aktivitas penting sebagai event "audit.<action>" ke log aplikasi (stdout).
// Audit log sengaja tidak disimpan di database platform (keputusan D41).
//
// Contoh: logx.Audit(ctx, "approve", slog.Int64("execution_id", 7), slog.String("status", "ok")).
// Pelaku, database, dan ID korelasi diambil dari context.
func Audit(ctx context.Context, action string, attrs ...slog.Attr) {
	slog.LogAttrs(ctx, slog.LevelInfo, "audit."+action, attrs...)
}
