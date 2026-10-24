package migration

import (
	"strconv"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

// Status is the lifecycle of one migration row.
type Status string

const (
	StatusDraft   Status = "draft"
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

// Migration is one row in the platform history for a target database.
type Migration struct {
	ID                 int64
	DatabaseID         int64
	Version            *int32
	Title              string
	SQLText            string
	Status             Status
	CreatedBy          int64
	CreatedByUsername  string
	CreatedAt          time.Time
	UpdatedBy          *int64
	UpdatedAt          time.Time
	ExecutedBy         *int64
	ExecutedByUsername string
	ExecutedAt         *time.Time
	FinishedAt         *time.Time
	DurationMS         *int32
	ErrorMessage       *string
}

// Run is one execution attempt.
type Run struct {
	ID                 int64
	MigrationID        int64
	ExecutedBy         int64
	ExecutedByUsername string
	SQLSnapshot        string
	Status             Status
	ErrorMessage       *string
	StartedAt          time.Time
	FinishedAt         time.Time
}

var (
	ErrNotFound      = apperr.NotFound("Migration tidak ditemukan")
	ErrImmutable     = apperr.Conflict("Migration yang sudah sukses tidak dapat diubah")
	ErrNotExecutable = apperr.Conflict("Migration ini tidak dapat dijalankan")
	ErrNotDeletable  = apperr.Conflict("Hanya draft yang dapat dihapus")
	ErrRejectedSQL   = apperr.Invalid("SQL ditolak: perintah transaction control, VACUUM, CREATE DATABASE, atau CREATE INDEX CONCURRENTLY tidak diizinkan")
)

// VersionLabel is the applied version, or an em dash while the migration has none.
func (m Migration) VersionLabel() string {
	if m.Version == nil {
		return "—"
	}
	return strconv.FormatInt(int64(*m.Version), 10)
}

// ErrorText is the stored error, or empty.
func (m Migration) ErrorText() string {
	if m.ErrorMessage == nil {
		return ""
	}
	return *m.ErrorMessage
}

// ErrorText is the stored run error, or empty.
func (r Run) ErrorText() string {
	if r.ErrorMessage == nil {
		return ""
	}
	return *r.ErrorMessage
}

// Duration is how long the attempt took.
func (r Run) Duration() time.Duration {
	return r.FinishedAt.Sub(r.StartedAt)
}

// WhenLabel prefers the execution time and falls back to creation time.
func (m Migration) WhenLabel() time.Time {
	if m.ExecutedAt != nil {
		return *m.ExecutedAt
	}
	return m.CreatedAt
}

func checkEditable(status Status) error {
	if status == StatusDraft || status == StatusFailed {
		return nil
	}
	return ErrImmutable
}

func checkDeletable(status Status) error {
	if status != StatusDraft {
		return ErrNotDeletable
	}
	return nil
}

func checkExecutable(status Status) error {
	if status == StatusDraft || status == StatusFailed {
		return nil
	}
	return ErrNotExecutable
}

// NextVersion is the next linear version after the highest applied version (0 when none).
func NextVersion(currentMax int32) int32 {
	return currentMax + 1
}
