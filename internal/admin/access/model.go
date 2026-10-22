package access

import (
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

var (
	ErrGroupNotFound = apperr.NotFound("Grup tidak ditemukan")
	ErrInvalidLevel  = apperr.Invalid("Level hak akses tidak valid (pilih 'none', 'view', atau 'edit')")
)

// DatabaseGrant mewakili status hak akses grup terhadap satu target database.
type DatabaseGrant struct {
	DatabaseID   int64  `json:"database_id"`
	DatabaseName string `json:"database_name"`
	Level        string `json:"level"` // "none", "view", "edit"
}

// SaveAccessInput adalah input untuk menyimpan matriks hak akses grup.
type SaveAccessInput struct {
	GroupID   int64
	Grants    map[int64]string // key: dbID, val: "none" | "view" | "edit"
	GrantedBy int64
}
