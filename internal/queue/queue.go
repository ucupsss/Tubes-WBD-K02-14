package queue

import (
	"context"
	"strings"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// Status job di tabel migration_jobs.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusDead    = "dead"

	// MaxAttempts adalah jumlah percobaan teknis maksimum, termasuk yang pertama (D35).
	MaxAttempts = 3
)

// Job adalah satu baris migration_jobs.
type Job struct {
	ID           int64
	ExecutionID  int64
	DatabaseID   int64
	Status       string
	Attempts     int
	ClaimedBy    string
	EnqueuedAt   time.Time
	ClaimedAt    *time.Time
	RunAfter     time.Time
	TraceContext map[string]string
}

// Queue dipakai API untuk menerbitkan job dan worker untuk mengambilnya.
type Queue interface {
	Enqueue(ctx context.Context, db platformdb.DBTX, execID, dbID int64, carrier map[string]string) (int64, error)
	Claim(ctx context.Context, workerID string) (*Job, error)
	Complete(ctx context.Context, jobID int64, status string) error
	Requeue(ctx context.Context, jobID int64, after time.Duration) error
	Get(ctx context.Context, jobID int64) (Job, error)
	FinishQueued(ctx context.Context, db platformdb.DBTX, execID int64) (bool, error)
}

// SQLError adalah kegagalan SQL yang boleh di-retry hanya bila SQLState-nya sementara.
type SQLError struct {
	SQLState string
	Message  string
}

// Error mengembalikan pesan SQL, atau SQLState bila pesan kosong.
func (e *SQLError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.SQLState
}

// Retryable melaporkan SQLSTATE yang boleh dicoba lagi (D35): kelas 08, 40001, 40P01, 57P01, 53300.
func Retryable(sqlstate string) bool {
	sqlstate = strings.ToUpper(strings.TrimSpace(sqlstate))
	if len(sqlstate) >= 2 && sqlstate[:2] == "08" {
		return true
	}
	switch sqlstate {
	case "40001", "40P01", "57P01", "53300":
		return true
	default:
		return false
	}
}

// retryDelays adalah jeda sebelum attempt berikutnya: 5s, 15s, lalu 45s.
// Attempt ke-3 adalah yang terakhir, jadi jeda 45 detik tidak dijadwalkan.
var retryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second}

// Backoff mengembalikan jeda setelah attempt yang gagal (1-based). ok false bila tidak ada attempt lagi.
func Backoff(failedAttempt int) (time.Duration, bool) {
	if failedAttempt < 1 || failedAttempt >= MaxAttempts || failedAttempt > len(retryDelays) {
		return 0, false
	}
	return retryDelays[failedAttempt-1], true
}
