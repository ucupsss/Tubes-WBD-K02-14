package explorer

import (
	"errors"
	"strconv"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

var (
	ErrTableNotFound       = apperr.NotFound("Tabel atau skema tidak ditemukan di database target")
	ErrDatabaseUnavailable = apperr.Unavailable("Database target tidak dapat dihubungi", errors.New("connection failed"))
)

// TableSummary merepresentasikan ringkasan tabel di target database.
type TableSummary struct {
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	Type       string `json:"type"` // "BASE TABLE", "VIEW"
	ApproxRows int64  `json:"approx_rows"`
}

// ColumnInfo merepresentasikan informasi kolom suatu tabel.
type ColumnInfo struct {
	Name         string  `json:"name"`
	DataType     string  `json:"data_type"`
	IsNullable   bool    `json:"is_nullable"`
	DefaultValue *string `json:"default_value"`
	IsPrimaryKey bool    `json:"is_primary_key"`
}

// ConstraintInfo merepresentasikan constraint (PK, FK, UNIQUE, CHECK).
type ConstraintInfo struct {
	Name          string `json:"name"`
	Type          string `json:"type"` // "PRIMARY KEY", "FOREIGN KEY", "UNIQUE", "CHECK"
	Definition    string `json:"definition"`
	ForeignTable  string `json:"foreign_table,omitempty"`
	ForeignColumn string `json:"foreign_column,omitempty"`
}

// IndexInfo merepresentasikan indeks pada tabel.
type IndexInfo struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	IsUnique   bool   `json:"is_unique"`
	IsPrimary  bool   `json:"is_primary"`
}

// TableMetadata menggabungkan seluruh metadata tabel (kolom, constraint, indeks).
type TableMetadata struct {
	DatabaseID  int64            `json:"database_id"`
	Schema      string           `json:"schema"`
	Table       string           `json:"table"`
	Columns     []ColumnInfo     `json:"columns"`
	Constraints []ConstraintInfo `json:"constraints"`
	Indexes     []IndexInfo      `json:"indexes"`
}

// TableDataPage merepresentasikan satu halaman data dari sebuah tabel.
type TableDataPage struct {
	DatabaseID int64      `json:"database_id"`
	Schema     string     `json:"schema"`
	Table      string     `json:"table"`
	Columns    []string   `json:"columns"`
	Rows       [][]string `json:"rows"` // Nilai diubah ke string yang aman untuk ditampilkan
	TotalRows  int64      `json:"total_rows"`
	Page       int        `json:"page"`
	Size       int        `json:"size"`
	TotalPages int        `json:"total_pages"`
	HasNext    bool       `json:"has_next"`
	HasPrev    bool       `json:"has_prev"`
	PrevPage   int        `json:"prev_page"`
	NextPage   int        `json:"next_page"`
	PKColumns  []string   `json:"pk_columns"`
}

// PaginationInput mem-parse dan membatasi parameter paginasi (page & size).
type PaginationInput struct {
	Page int
	Size int
}

// ParsePagination mengurai nilai page dan size dari query string.
// Default: page = 1, size = 50. Max size = 200. Min size = 1.
func ParsePagination(pageStr, sizeStr string) PaginationInput {
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	size, err := strconv.Atoi(sizeStr)
	if err != nil || size <= 0 {
		size = 50
	} else if size > 200 {
		size = 200
	}

	return PaginationInput{Page: page, Size: size}
}

// Offset menghitung nilai OFFSET SQL berdasarkan page dan size.
func (p PaginationInput) Offset() int {
	return (p.Page - 1) * p.Size
}
