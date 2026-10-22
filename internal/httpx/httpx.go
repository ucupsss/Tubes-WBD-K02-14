package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// PathInt64 mem-parse parameter path r.PathValue(name) ke int64.
func PathInt64(r *http.Request, name string) (int64, error) {
	val := r.PathValue(name)
	if val == "" {
		return 0, fmt.Errorf("param %s kosong", name)
	}
	id, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("param %s bukan int64: %w", name, err)
	}
	return id, nil
}

// HandleError memetakan apperr.Kind ke kode HTTP dan merender respon error yang aman.
func HandleError(w http.ResponseWriter, r *http.Request, v *view.View, err error) {
	kind := apperr.KindOf(err)
	status := http.StatusInternalServerError
	msg := "Terjadi kesalahan pada server"

	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr.Msg != "" {
		msg = appErr.Msg
	}

	switch kind {
	case apperr.KindInvalid:
		status = http.StatusBadRequest
	case apperr.KindUnauthenticated:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	case apperr.KindForbidden:
		status = http.StatusForbidden
	case apperr.KindNotFound:
		status = http.StatusNotFound
	case apperr.KindConflict:
		status = http.StatusConflict
	case apperr.KindUnavailable:
		status = http.StatusBadGateway
	}

	http.Error(w, msg, status)
}

// Redirect melakukan HTTP 303 See Other (Post/Redirect/Get) dengan pesan flash opsional.
func Redirect(w http.ResponseWriter, r *http.Request, url string, flash *view.Flash) {
	http.Redirect(w, r, url, http.StatusSeeOther)
}
