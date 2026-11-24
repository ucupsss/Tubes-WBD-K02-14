package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// HashRevision adalah sha256(up || 0x00 || down || mode), dan untuk mode batch
// ditambah 0x00 || tabel || 0x00 || kolom || 0x00 || ukuran.
func HashRevision(up, down, mode, table, column string, size int) string {
	h := sha256.New()
	h.Write([]byte(up))
	h.Write([]byte{0})
	h.Write([]byte(down))
	h.Write([]byte(mode))
	if mode == ModeBatch {
		h.Write([]byte{0})
		h.Write([]byte(table))
		h.Write([]byte{0})
		h.Write([]byte(column))
		h.Write([]byte{0})
		h.Write([]byte(strconv.Itoa(size)))
	}
	return hex.EncodeToString(h.Sum(nil))
}
