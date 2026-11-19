// Package approval mengelola persetujuan perubahan database oleh leader group (Milestone 2, D27–D28, D40).
//
// Setiap execution (apply, forward, rollback) butuh approval dari leader SETIAP group yang punya grant
// view atau edit ke database target. Satu baris approval per (execution, ronde, group).
// Package ini tidak mengubah status execution sendiri: ia memanggil Executions (diimplementasi C).
//
// Pemilik: A.
package approval

import (
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

// Decision adalah keputusan satu baris approval.
type Decision string

const (
	Pending  Decision = "pending"
	Approved Decision = "approved"
	Rejected Decision = "rejected"
)

// Status execution yang dipakai package ini (nilai string sama dengan kolom executions.status milik C).
const (
	StatusPendingReview = "pending_review"
	StatusApproved      = "approved"
	StatusRejected      = "rejected"
)

var (
	ErrNotLeader       = apperr.Forbidden("Anda bukan leader group yang perlu menyetujui perubahan ini")
	ErrAlreadyDecided  = apperr.Conflict("Keputusan untuk group Anda sudah dicatat").WithReason("already_decided")
	ErrNotPending      = apperr.Conflict("Perubahan ini tidak sedang menunggu review").WithReason("not_pending_review")
	ErrNoGroups        = apperr.Conflict("Tidak ada group yang memiliki akses ke database ini").WithReason("no_groups")
	ErrExecUnavailable = apperr.Conflict("Fitur eksekusi migration belum tersedia").WithReason("executions_unavailable")
)

// GroupLeader adalah group yang punya akses ke sebuah database beserta leader-nya saat ini.
type GroupLeader struct {
	GroupID   int64
	GroupName string
	LeaderID  *int64
}

// Approval adalah satu baris approval beserta nama untuk ditampilkan.
type Approval struct {
	ExecutionID   int64
	Round         int
	GroupID       int64
	GroupName     string
	LeaderID      *int64 // leader group saat ini
	LeaderName    string
	Decision      Decision
	Comment       string
	DecidedByName string
	DecidedAt     *time.Time
}

// PendingItem adalah satu antrean approval milik seorang leader (untuk inbox).
type PendingItem struct {
	ExecutionID int64
	Round       int
	GroupID     int64
	GroupName   string
	CreatedAt   time.Time
}

// Verification adalah hasil pengecekan approval oleh worker saat revalidasi (D40).
type Verification struct {
	Complete        bool
	MissingGroupIDs []int64
}

// Outcome adalah akibat satu keputusan terhadap execution.
type Outcome struct {
	Status string // StatusApproved, StatusRejected, atau StatusPendingReview (masih menunggu leader lain)
}

// ExecInfo adalah data execution yang dibutuhkan approval.
type ExecInfo struct {
	DatabaseID int64
	Round      int
	Status     string
}
