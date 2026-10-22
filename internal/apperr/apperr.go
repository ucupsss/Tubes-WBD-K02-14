package apperr

import (
	"errors"
	"fmt"
)

type Kind int

const (
	KindInternal        Kind = iota // 500 — pesan generik
	KindInvalid                     // 422 (form) / 400
	KindUnauthenticated             // redirect /login
	KindForbidden                   // 403
	KindNotFound                    // 404
	KindConflict                    // 409 — mis. migration immutable, nama duplikat
	KindUnavailable                 // 502 — target DB tidak bisa dihubungi
)

type Error struct {
	Kind   Kind
	Msg    string            // AMAN ditampilkan ke user (Bahasa Indonesia)
	Fields map[string]string // error per field form (opsional)
	Err    error             // penyebab internal, hanya untuk log
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func NotFound(msg string) *Error {
	return &Error{Kind: KindNotFound, Msg: msg}
}

func Forbidden(msg string) *Error {
	return &Error{Kind: KindForbidden, Msg: msg}
}

func Conflict(msg string) *Error {
	return &Error{Kind: KindConflict, Msg: msg}
}

func Unavailable(msg string, cause error) *Error {
	return &Error{Kind: KindUnavailable, Msg: msg, Err: cause}
}

func Invalid(msg string) *Error {
	return &Error{Kind: KindInvalid, Msg: msg}
}

func InvalidFields(msg string, fields map[string]string) *Error {
	return &Error{Kind: KindInvalid, Msg: msg, Fields: fields}
}

func Internal(msg string, cause error) *Error {
	return &Error{Kind: KindInternal, Msg: msg, Err: cause}
}

func KindOf(err error) Kind {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return KindInternal
}

func FieldErrors(err error) (map[string]string, bool) {
	var appErr *Error
	if errors.As(err, &appErr) && len(appErr.Fields) > 0 {
		return appErr.Fields, true
	}
	return nil, false
}
