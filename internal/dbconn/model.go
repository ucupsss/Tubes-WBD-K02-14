package dbconn

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNotFound      = errors.New("koneksi database tidak ditemukan")
	ErrUnavailable   = errors.New("database target tidak dapat dihubungi")
	ErrHasMigrations = errors.New("koneksi database memiliki riwayat migrasi dan tidak dapat dihapus")
	ErrDuplicateName = errors.New("nama koneksi database sudah terdaftar")
)

// Connection merepresentasikan baris dalam tabel platform DB db_connections.
type Connection struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	DBName      string    `json:"db_name"`
	Username    string    `json:"username"`
	PasswordEnc []byte    `json:"-"` // Tidak boleh di-serialize ke JSON/log
	SSLMode     string    `json:"sslmode"`
	CreatedBy   *int64    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// BuildDSN menyusun DSN PostgreSQL untuk koneksi driver pgxpool.
// DSN ini mengandung kredensial plain, JANGAN dicatat di log.
func (c *Connection) BuildDSN(plainPassword string) string {
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	port := c.Port
	if port <= 0 {
		port = 5432
	}

	userInfo := url.UserPassword(c.Username, plainPassword)
	u := url.URL{
		Scheme:   "postgres",
		User:     userInfo,
		Host:     fmt.Sprintf("%s:%d", c.Host, port),
		Path:     c.DBName,
		RawQuery: fmt.Sprintf("sslmode=%s", url.QueryEscape(sslMode)),
	}
	return u.String()
}

// SafeSummary mengembalikan ringkasan koneksi yang aman untuk log (tanpa kredensial).
func (c *Connection) SafeSummary() string {
	return fmt.Sprintf("id=%d name=%q host=%s port=%d db=%s sslmode=%s",
		c.ID, c.Name, c.Host, c.Port, c.DBName, c.SSLMode)
}

// CreateInput adalah input untuk membuat koneksi database baru.
type CreateInput struct {
	Name      string
	Host      string
	Port      int
	DBName    string
	Username  string
	Password  string
	SSLMode   string
	CreatedBy int64
}

func (in CreateInput) Validate() map[string]string {
	errs := make(map[string]string)
	if strings.TrimSpace(in.Name) == "" {
		errs["name"] = "Nama koneksi wajib diisi"
	}
	if strings.TrimSpace(in.Host) == "" {
		errs["host"] = "Host wajib diisi"
	}
	if in.Port <= 0 || in.Port > 65535 {
		errs["port"] = "Port harus antara 1 sampai 65535"
	}
	if strings.TrimSpace(in.DBName) == "" {
		errs["db_name"] = "Nama database wajib diisi"
	}
	if strings.TrimSpace(in.Username) == "" {
		errs["username"] = "Username wajib diisi"
	}
	if in.Password == "" {
		errs["password"] = "Password wajib diisi untuk koneksi baru"
	}
	if in.SSLMode == "" {
		in.SSLMode = "disable"
	}
	return errs
}

// UpdateInput adalah input untuk memperbarui koneksi database.
// Password bersifat opsional; jika kosong, password lama tidak berubah.
type UpdateInput struct {
	Name     string
	Host     string
	Port     int
	DBName   string
	Username string
	Password string // kosong = tidak ubah password
	SSLMode  string
}

func (in UpdateInput) Validate() map[string]string {
	errs := make(map[string]string)
	if strings.TrimSpace(in.Name) == "" {
		errs["name"] = "Nama koneksi wajib diisi"
	}
	if strings.TrimSpace(in.Host) == "" {
		errs["host"] = "Host wajib diisi"
	}
	if in.Port <= 0 || in.Port > 65535 {
		errs["port"] = "Port harus antara 1 sampai 65535"
	}
	if strings.TrimSpace(in.DBName) == "" {
		errs["db_name"] = "Nama database wajib diisi"
	}
	if strings.TrimSpace(in.Username) == "" {
		errs["username"] = "Username wajib diisi"
	}
	return errs
}

// GroupAccess merepresentasikan baris dalam tabel group_db_access.
type GroupAccess struct {
	GroupID    int64     `json:"group_id"`
	DatabaseID int64     `json:"database_id"`
	Level      string    `json:"level"` // "view" atau "edit"
	GrantedBy  *int64    `json:"granted_by,omitempty"`
	GrantedAt  time.Time `json:"granted_at"`
}
