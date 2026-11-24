// Package batch mengeksekusi perubahan database berukuran besar dalam potongan-potongan
// (mode per batch) dengan transaction terpisah per batch, pencatatan checkpoint di
// database target (_dbmp.batch_checkpoints) pada transaction yang sama, dukungan resume
// dari checkpoint terakhir, serta kompensasi/revert per batch dalam urutan terbalik
// tanpa memuat seluruh dataset ke memori (Milestone 2, D36, D37, D39).
//
// Pemilik: B (Daniel).
package batch

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

// Direction adalah arah eksekusi batch: apply (up_sql maju) atau revert (down_sql mundur).
const (
	DirectionApply  = "apply"
	DirectionRevert = "revert"
)

// Status hasil eksekusi batch oleh Runner.
type Status string

const (
	StatusDone              Status = "done"               // seluruh batch selesai
	StatusPartial           Status = "partial"            // sebagian batch apply sudah committed, lalu gagal
	StatusPartiallyReverted Status = "partially_reverted" // sebagian batch revert sudah committed, lalu gagal
	StatusFailed            Status = "failed"             // gagal sebelum ada batch baru yang committed dan belum ada batch committed sebelumnya
)

var (
	ErrInvalidSpec       = apperr.Invalid("Konfigurasi mode batch tidak valid")
	ErrMissingBounds     = apperr.Invalid("SQL mode batch wajib memuat placeholder :from dan :to")
	ErrInvalidBatchTable = apperr.Invalid("Tabel atau kolom batch tidak ditemukan atau bukan bertipe integer")
)

// Spec mendefinisikan konfigurasi eksekusi batch untuk satu execution.
type Spec struct {
	ExecutionID int64
	// SourceExecutionID adalah execution_id tempat checkpoint direction='apply' berada saat melakukan Revert.
	// Bila 0, menggunakan ExecutionID.
	SourceExecutionID int64
	MigrationID       int64
	JobID             int64
	Attempt           int
	// Table adalah nama tabel yang menjadi acuan cursor batch (mis. "big_events" atau "public.big_events").
	Table string
	// Column adalah kolom integer unik yang menjadi cursor (mis. "id").
	Column string
	// Size adalah jumlah maksimum baris per batch (> 0).
	Size int
	// UpSQL dan DownSQL wajib memuat placeholder :from dan :to.
	UpSQL   string
	DownSQL string
	// StatementTimeout dan LockTimeout opsional; bila kosong default 30s dan 5s.
	StatementTimeout string
	LockTimeout      string
}

// Checkpoint adalah satu catatan batch yang sudah di-commit di _dbmp.batch_checkpoints
// (dan dicerminkan ke execution_batches di database platform).
type Checkpoint struct {
	ExecutionID  int64     `json:"execution_id"`
	Direction    string    `json:"direction"`
	BatchNo      int       `json:"batch_no"`
	FromValue    int64     `json:"from_value"`
	ToValue      int64     `json:"to_value"`
	RowsAffected int64     `json:"rows_affected"`
	CommittedAt  time.Time `json:"committed_at"`
}

// Result menggambarkan hasil akhir pemanggilan Apply atau Revert.
type Result struct {
	Status           Status `json:"status"`
	BatchesCommitted int    `json:"batches_committed"`
	RowsCommitted    int64  `json:"rows_committed"`
	LastBatchNo      int    `json:"last_batch_no"`
	LastFrom         int64  `json:"last_from"`
	LastTo           int64  `json:"last_to"`
	LastExecutedSQL  string `json:"last_executed_sql"`
}

// ProgressSink dipanggil setiap kali satu batch berhasil di-commit di database target.
// Implementasi oleh worker (C) memperbarui kolom batches_committed/rows_committed pada
// tabel executions dan mencerminkan baris ke tabel execution_batches di platform DB.
type ProgressSink interface {
	BatchCommitted(ctx context.Context, direction string, batchNo int, from, to, rows int64) error
}

// ProgressFunc adalah adapter fungsi agar dapat digunakan sebagai ProgressSink.
type ProgressFunc func(ctx context.Context, direction string, batchNo int, from, to, rows int64) error

// BatchCommitted memenuhi interface ProgressSink.
func (f ProgressFunc) BatchCommitted(ctx context.Context, direction string, batchNo int, from, to, rows int64) error {
	if f == nil {
		return nil
	}
	return f(ctx, direction, batchNo, from, to, rows)
}

// ValidateSpec memeriksa kelengkapan konfigurasi Spec untuk arah yang diminta.
func ValidateSpec(spec Spec, direction string) error {
	if spec.ExecutionID <= 0 {
		return apperr.InvalidFields("execution_id harus positif", map[string]string{"execution_id": "execution_id harus positif"})
	}
	if strings.TrimSpace(spec.Table) == "" {
		return apperr.InvalidFields("Nama tabel batch wajib diisi", map[string]string{"batch_table": "Nama tabel batch wajib diisi"})
	}
	if strings.TrimSpace(spec.Column) == "" {
		return apperr.InvalidFields("Nama kolom batch wajib diisi", map[string]string{"batch_column": "Nama kolom batch wajib diisi"})
	}
	if spec.Size <= 0 {
		return apperr.InvalidFields("Ukuran batch harus lebih besar dari 0", map[string]string{"batch_size": "Ukuran batch harus lebih besar dari 0"})
	}
	sqlText := spec.UpSQL
	if direction == DirectionRevert {
		sqlText = spec.DownSQL
	}
	if err := ValidateBatchSQL(sqlText); err != nil {
		return err
	}
	return nil
}

// ValidateBatchSQL memastikan SQL tidak kosong dan memiliki placeholder :from serta :to
// di luar string literal, dollar-quote, atau komentar SQL.
func ValidateBatchSQL(sqlText string) error {
	if strings.TrimSpace(sqlText) == "" {
		return apperr.Invalid("SQL mode batch tidak boleh kosong")
	}
	hasFrom, hasTo := scanPlaceholders(sqlText)
	if !hasFrom || !hasTo {
		return ErrMissingBounds
	}
	return nil
}

// ExpandBounds mengganti setiap placeholder :from dan :to (yang berada di luar literal string,
// identifier ber-quote, dollar-quote, blok komentar, dan bukan bagian dari cast PostgreSQL ::from / ::to)
// dengan angka literal int64 sesuai D39.
func ExpandBounds(sqlText string, from, to int64) (string, error) {
	hasFrom, hasTo := scanPlaceholders(sqlText)
	if !hasFrom || !hasTo {
		return "", ErrMissingBounds
	}
	fromStr := strconv.FormatInt(from, 10)
	toStr := strconv.FormatInt(to, 10)
	return replacePlaceholders(sqlText, fromStr, toStr), nil
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func scanPlaceholders(s string) (hasFrom, hasTo bool) {
	_ = walkSQL(s, func(name string) string {
		if name == "from" {
			hasFrom = true
		} else if name == "to" {
			hasTo = true
		}
		return ":" + name
	})
	return hasFrom, hasTo
}

func replacePlaceholders(s, fromVal, toVal string) string {
	return walkSQL(s, func(name string) string {
		switch name {
		case "from":
			return fromVal
		case "to":
			return toVal
		default:
			return ":" + name
		}
	})
}

// walkSQL menelusuri teks SQL dan memanggil onPlaceholder("from"|"to") hanya untuk token :from dan :to
// yang berada di luar single-quote, double-quote, dollar-quote, komentar --, komentar /* */,
// dan bukan diawali titik dua ganda (::type).
func walkSQL(s string, onPlaceholder func(name string) string) string {
	var out strings.Builder
	out.Grow(len(s) + 32)
	n := len(s)
	i := 0
	for i < n {
		c := s[i]

		// 1. Komentar satu baris --
		if c == '-' && i+1 < n && s[i+1] == '-' {
			start := i
			i += 2
			for i < n && s[i] != '\n' {
				i++
			}
			out.WriteString(s[start:i])
			continue
		}

		// 2. Komentar blok /* ... */
		if c == '/' && i+1 < n && s[i+1] == '*' {
			start := i
			i += 2
			for i+1 < n && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			} else {
				i = n
			}
			out.WriteString(s[start:i])
			continue
		}

		// 3. String literal '...' atau identifier "..."
		if c == '\'' || c == '"' {
			quote := c
			start := i
			i++
			for i < n {
				if s[i] == quote {
					if i+1 < n && s[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			out.WriteString(s[start:i])
			continue
		}

		// 4. Dollar-quoted string $tag$...$tag$
		if c == '$' {
			if tagLen := matchDollarTag(s[i:]); tagLen > 0 {
				tag := s[i : i+tagLen]
				out.WriteString(tag)
				i += tagLen
				endIdx := strings.Index(s[i:], tag)
				if endIdx >= 0 {
					out.WriteString(s[i : i+endIdx+tagLen])
					i += endIdx + tagLen
				} else {
					out.WriteString(s[i:])
					i = n
				}
				continue
			}
		}

		// 5. Type cast PostgreSQL ::
		if c == ':' && i+1 < n && s[i+1] == ':' {
			out.WriteString("::")
			i += 2
			continue
		}

		// 6. Placeholder :from atau :to
		if c == ':' && (i == 0 || s[i-1] != ':') {
			rest := s[i+1:]
			if strings.HasPrefix(rest, "from") && (len(rest) == 4 || !isIdentChar(rest[4])) {
				out.WriteString(onPlaceholder("from"))
				i += 5
				continue
			}
			if strings.HasPrefix(rest, "to") && (len(rest) == 2 || !isIdentChar(rest[2])) {
				out.WriteString(onPlaceholder("to"))
				i += 3
				continue
			}
		}

		out.WriteByte(c)
		i++
	}
	return out.String()
}

func matchDollarTag(s string) int {
	if len(s) < 2 || s[0] != '$' {
		return 0
	}
	if s[1] == '$' {
		return 2
	}
	if !((s[1] >= 'a' && s[1] <= 'z') || (s[1] >= 'A' && s[1] <= 'Z') || s[1] == '_') {
		return 0
	}
	for i := 2; i < len(s); i++ {
		if s[i] == '$' {
			return i + 1
		}
		if !isIdentChar(s[i]) {
			return 0
		}
	}
	return 0
}
