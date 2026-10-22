package databases

import (
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
)

type Connection = dbconn.Connection
type CreateInput = dbconn.CreateInput
type UpdateInput = dbconn.UpdateInput

var (
	ErrNotFound      = apperr.NotFound("Koneksi database tidak ditemukan")
	ErrDuplicateName = apperr.Conflict("Nama koneksi database sudah digunakan")
	ErrHasMigrations = apperr.Conflict("Koneksi database memiliki riwayat migrasi dan tidak dapat dihapus")
)
