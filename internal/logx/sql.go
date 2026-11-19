package logx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// SQLEvent menggambarkan satu eksekusi SQL untuk dicatat.
type SQLEvent struct {
	Query    string
	Params   []any
	Duration time.Duration
	Rows     int64 // < 0 bila tidak diketahui
	Err      error

	// Identitas opsional; 0/kosong tidak ditulis.
	MigrationID int64
	ExecutionID int64
	JobID       int64
	Attempt     int
	BatchNo     int
}

var sensitiveTable = regexp.MustCompile(`(?i)\b(users|sessions)\b|password`)

// SQL mencatat satu eksekusi SQL sebagai event "sql.exec": query, parameter (disamarkan), durasi,
// jumlah row, dan error/SQLSTATE. Parameter tidak dicatat bila query menyentuh tabel users/sessions.
func SQL(ctx context.Context, e SQLEvent) {
	attrs := []slog.Attr{
		slog.String("query", Truncate(compact(e.Query), 2000)),
		slog.Int64("duration_ms", e.Duration.Milliseconds()),
		slog.Any("params", RedactParams(e.Query, e.Params)),
	}
	if e.Rows >= 0 {
		attrs = append(attrs, slog.Int64("rows", e.Rows))
	}
	level := slog.LevelInfo
	status := "ok"
	if e.Err != nil {
		level = slog.LevelWarn
		status = "error"
		attrs = append(attrs, slog.String("error", RedactString(e.Err.Error())))
		var pgErr *pgconn.PgError
		if errors.As(e.Err, &pgErr) {
			attrs = append(attrs, slog.String("sqlstate", pgErr.Code))
		}
	}
	attrs = append(attrs, slog.String("status", status))
	if e.MigrationID != 0 {
		attrs = append(attrs, slog.Int64("migration_id", e.MigrationID))
	}
	if e.ExecutionID != 0 {
		attrs = append(attrs, slog.Int64("execution_id", e.ExecutionID))
	}
	if e.JobID != 0 {
		attrs = append(attrs, slog.Int64("job_id", e.JobID))
	}
	if e.Attempt != 0 {
		attrs = append(attrs, slog.Int("attempt", e.Attempt))
	}
	if e.BatchNo != 0 {
		attrs = append(attrs, slog.Int("batch_no", e.BatchNo))
	}
	slog.LogAttrs(ctx, level, "sql.exec", attrs...)
}

// RedactParams mengembalikan parameter yang aman dicatat: dikosongkan ("[REDACTED]") bila query
// menyentuh tabel users/sessions, selain itu tiap nilai diubah ke teks dan dipotong.
func RedactParams(query string, params []any) []string {
	if len(params) == 0 {
		return []string{}
	}
	out := make([]string, len(params))
	sensitive := sensitiveTable.MatchString(query)
	for i, p := range params {
		if sensitive {
			out[i] = Redacted
			continue
		}
		out[i] = RedactString(fmt.Sprint(p))
	}
	return out
}

func compact(q string) string {
	return strings.Join(strings.Fields(q), " ")
}
