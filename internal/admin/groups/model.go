package groups

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

var (
	ErrNotFound     = apperr.NotFound("Group tidak ditemukan")
	ErrNameTaken    = apperr.Conflict("Nama group sudah dipakai")
	ErrUserNotFound = apperr.NotFound("User tidak ditemukan")
)

const (
	maxNameLen        = 64
	maxDescriptionLen = 500
)

type Group struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
	MemberCount int
}

type Member struct {
	UserID   int64
	Username string
	IsActive bool
}

// UserOption adalah user aktif yang belum menjadi anggota (pilihan di form tambah anggota).
type UserOption struct {
	ID       int64
	Username string
}

// Detail berisi data halaman detail group.
type Detail struct {
	Group      Group
	Members    []Member
	Candidates []UserOption
}

type Input struct {
	Name        string
	Description string
}

func (in Input) normalized() Input {
	return Input{Name: strings.TrimSpace(in.Name), Description: strings.TrimSpace(in.Description)}
}

func (in Input) Validate() error {
	in = in.normalized()
	fields := map[string]string{}
	if n := utf8.RuneCountInString(in.Name); n == 0 || n > maxNameLen {
		fields["name"] = "Nama group wajib diisi, maksimal 64 karakter"
	}
	if utf8.RuneCountInString(in.Description) > maxDescriptionLen {
		fields["description"] = "Deskripsi maksimal 500 karakter"
	}
	if len(fields) > 0 {
		return apperr.InvalidFields("Input tidak valid", fields)
	}
	return nil
}
