package migration

import (
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

// Jenis execution (D25). Resume bukan jenis baru.
const (
	KindApply    = "apply"
	KindForward  = "forward"
	KindRollback = "rollback"
)

// Status execution (D26). "Draft" bukan status execution: migration yang belum punya execution.
const (
	StatusPendingReview     = "pending_review"
	StatusApproved          = "approved"
	StatusQueued            = "queued"
	StatusRunning           = "running"
	StatusSuccess           = "success"
	StatusFailed            = "failed"
	StatusPartial           = "partial"
	StatusPartiallyReverted = "partially_reverted"
	StatusUncertain         = "uncertain"
	StatusRejected          = "rejected"
	StatusCancelled         = "cancelled"
)

// Mode eksekusi pada revisi. Batch menunda SQL sampai paket batch tersedia.
const (
	ModeNormal = "normal"
	ModeBatch  = "batch"
)

// Revision adalah satu versi SQL. Setelah frozen_at terisi, isi SQL tidak boleh diubah.
type Revision struct {
	ID                 int64
	MigrationID        int64
	RevisionNo         int
	UpSQL              string
	DownSQL            *string
	Irreversible       bool
	IrreversibleReason *string
	ExecMode           string
	BatchTable         *string
	BatchColumn        *string
	BatchSize          *int
	SQLHash            string
	FrozenAt           *time.Time
	CreatedBy          int64
	CreatedAt          time.Time
}

// Migration adalah identitas satu perubahan pada satu database target.
type Migration struct {
	ID                int64
	DatabaseID        int64
	Version           *int32
	Title             string
	CurrentRevisionID int64
	CreatedBy         int64
	CreatedByUsername string
	CreatedAt         time.Time
	Revision          Revision
}

// Execution adalah satu permintaan untuk menjalankan atau membatalkan sebuah revisi.
type Execution struct {
	ID                   int64
	DatabaseID           int64
	MigrationID          int64
	MigrationTitle       string
	RevisionID           int64
	Kind                 string
	Status               string
	RequestedBy          int64
	RequestedByUsername  string
	RollsBackExecutionID *int64
	ApprovalRound        int
	VersionApplied       *int32
	BatchesCommitted     int
	RowsCommitted        int64
	CreatedAt            time.Time
	StartedAt            *time.Time
	FinishedAt           *time.Time
	ErrorCode            *string
	ErrorMessage         *string
}

// Attempt adalah satu percobaan teknis atas sebuah execution, termasuk retry otomatis.
type Attempt struct {
	ID           int64
	ExecutionID  int64
	AttemptNo    int
	WorkerID     *string
	Status       string
	SQLExecuted  string
	SQLState     *string
	ErrorMessage *string
	StartedAt    time.Time
	FinishedAt   *time.Time
	TraceID      *string
}

// Page adalah daftar berhalaman.
type Page struct {
	Items    any
	Page     int
	PageSize int
	Total    int
}

// Kesalahan domain migration. Reason pada Conflict dipakai sebagai details.reason di API.
var (
	ErrNotFound       = apperr.NotFound("Migration tidak ditemukan")
	ErrExecNotFound   = apperr.NotFound("Eksekusi tidak ditemukan")
	ErrImmutable      = apperr.Conflict("Revisi yang sudah diajukan tidak dapat diubah").WithReason("immutable")
	ErrNotDeletable   = apperr.Conflict("Hanya draft yang belum pernah diajukan yang dapat dihapus").WithReason("not_draft")
	ErrNotTop         = apperr.Conflict("Hanya migration teratas pada rantai aktif yang dapat di-rollback").WithReason("not_top_of_chain")
	ErrIrreversible   = apperr.Conflict("Migration ini ditandai tidak dapat di-rollback").WithReason("irreversible")
	ErrNotActive      = apperr.Conflict("Migration ini tidak sedang aktif").WithReason("not_active")
	ErrAlreadyActive  = apperr.Conflict("Migration ini masih aktif di rantai").WithReason("already_active")
	ErrNotForwardable = apperr.Conflict("Migration ini tidak dapat di-forward").WithReason("not_forwardable")
	ErrNotSubmittable = apperr.Conflict("Migration ini tidak dapat diajukan. Ubah SQL untuk membuat revisi baru, atau gunakan forward.").WithReason("not_submittable")
	ErrBlocked        = apperr.Conflict("Masih ada eksekusi yang belum selesai untuk migration ini").WithReason("blocked")
	ErrNotAnalyzable  = apperr.Conflict("Hanya revisi yang belum diajukan yang dapat dianalisis").WithReason("not_analyzable")
	ErrRejectedSQL    = apperr.Invalid("SQL ditolak: perintah transaction control, VACUUM, CREATE DATABASE, atau CREATE INDEX CONCURRENTLY tidak diizinkan")
	ErrNotApproved    = apperr.Conflict("Eksekusi belum disetujui").WithReason("not_approved")
	ErrNotResumable   = apperr.Conflict("Hanya eksekusi yang berhenti di tengah yang dapat dilanjutkan").WithReason("not_resumable")
	ErrNotCancellable = apperr.Conflict("Eksekusi ini tidak dapat dibatalkan").WithReason("not_cancellable")
	ErrJobRunning     = apperr.Conflict("Eksekusi sedang berjalan").WithReason("job_running")
	ErrJobNotFound    = apperr.NotFound("Job tidak ditemukan")
)

// NextVersion adalah posisi berikutnya pada rantai aktif.
func NextVersion(currentMax int32) int32 {
	return currentMax + 1
}

func openStatuses() []string {
	return []string{
		StatusPendingReview, StatusApproved, StatusQueued, StatusRunning,
		StatusPartial, StatusPartiallyReverted, StatusUncertain,
	}
}
