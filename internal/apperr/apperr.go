package apperr

import (
	"errors"
	"fmt"
)

// Kind memetakan kesalahan aplikasi ke satu kelas respons HTTP.
type Kind int

// Konstanta Kind. Angka iota hanya pembeda; kode HTTP ada di komentar dan dipetakan oleh httpx.
const (
	KindInternal        Kind = iota // 500 — pesan generik
	KindInvalid                     // 422 (form) / 400
	KindUnauthenticated             // redirect /login
	KindForbidden                   // 403
	KindNotFound                    // 404
	KindConflict                    // 409 — mis. migration immutable, nama duplikat
	KindUnavailable                 // 502 — target DB tidak bisa dihubungi
)

// Error adalah kesalahan aplikasi yang aman ditampilkan ke pengguna.
// Err disimpan untuk log dan tidak boleh ikut ke respons.
type Error struct {
	Kind   Kind
	Msg    string            // AMAN ditampilkan ke user (Bahasa Indonesia)
	Fields map[string]string // error per field form (opsional)
	Reason string            // kode mesin untuk API (details.reason), mis. "not_top_of_chain" (opsional)
	Err    error             // penyebab internal, hanya untuk log
}

// Error mengembalikan pesan untuk manusia, ditambah penyebab internal bila ada.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

// Unwrap mengembalikan penyebab internal agar errors.Is dan errors.As bisa menelusurinya.
func (e *Error) Unwrap() error { return e.Err }

// NotFound membuat kesalahan 404.
func NotFound(msg string) *Error {
	return &Error{Kind: KindNotFound, Msg: msg}
}

// Forbidden membuat kesalahan 403.
func Forbidden(msg string) *Error {
	return &Error{Kind: KindForbidden, Msg: msg}
}

// Conflict membuat kesalahan 409. Pasangkan WithReason untuk kode mesin di details.reason.
func Conflict(msg string) *Error {
	return &Error{Kind: KindConflict, Msg: msg}
}

// Unavailable membuat kesalahan 502. cause hanya untuk log.
func Unavailable(msg string, cause error) *Error {
	return &Error{Kind: KindUnavailable, Msg: msg, Err: cause}
}

// Invalid membuat kesalahan 400.
func Invalid(msg string) *Error {
	return &Error{Kind: KindInvalid, Msg: msg}
}

// Unauthenticated membuat kesalahan untuk pengguna yang belum login.
func Unauthenticated(msg string) *Error {
	return &Error{Kind: KindUnauthenticated, Msg: msg}
}

// InvalidFields membuat kesalahan 400 dengan pesan per field formulir.
func InvalidFields(msg string, fields map[string]string) *Error {
	return &Error{Kind: KindInvalid, Msg: msg, Fields: fields}
}

// Internal membuat kesalahan 500. cause hanya untuk log.
func Internal(msg string, cause error) *Error {
	return &Error{Kind: KindInternal, Msg: msg, Err: cause}
}

// WithReason mengisi kode mesin untuk klien API tanpa mengubah pesan untuk manusia.
func (e *Error) WithReason(reason string) *Error {
	cp := *e
	cp.Reason = reason
	return &cp
}

// KindOf mengembalikan Kind dari err, atau KindInternal bila err bukan *Error.
func KindOf(err error) Kind {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return KindInternal
}

// FieldErrors mengembalikan peta field bila err adalah *Error yang punya Fields.
func FieldErrors(err error) (map[string]string, bool) {
	var appErr *Error
	if errors.As(err, &appErr) && len(appErr.Fields) > 0 {
		return appErr.Fields, true
	}
	return nil, false
}
