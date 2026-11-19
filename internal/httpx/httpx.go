package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// PathInt64 parses r.PathValue(name) as int64. A missing or invalid value is 404.
func PathInt64(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.NotFound("Data tidak ditemukan")
	}
	return id, nil
}

// HandleError maps apperr.Kind to an HTTP status and a safe message.
// When the view has templates, the shared error page is rendered. Otherwise the
// status is written as plain text so callers with view.New() still observe the code.
func HandleError(w http.ResponseWriter, r *http.Request, v *view.View, err error) {
	if IsAPI(r) {
		WriteError(w, r, err)
		return
	}
	kind := apperr.KindOf(err)
	status := http.StatusInternalServerError
	msg := "Terjadi kesalahan pada server"

	var appErr *apperr.Error
	errors.As(err, &appErr)

	switch kind {
	case apperr.KindInvalid:
		status = http.StatusBadRequest
		msg = messageOr(appErr, "Permintaan tidak valid")
	case apperr.KindUnauthenticated:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	case apperr.KindForbidden:
		status = http.StatusForbidden
		msg = messageOr(appErr, "Anda tidak memiliki akses")
	case apperr.KindNotFound:
		status = http.StatusNotFound
		msg = messageOr(appErr, "Data tidak ditemukan")
	case apperr.KindConflict:
		status = http.StatusConflict
		msg = messageOr(appErr, "Data bentrok dengan data yang sudah ada")
	case apperr.KindUnavailable:
		status = http.StatusBadGateway
		msg = messageOr(appErr, "Database tidak dapat dihubungi")
	default:
		slog.Error("request error",
			"err", err,
			"method", r.Method,
			"path", r.URL.Path,
			"user_id", userID(r),
		)
	}

	if v != nil && v.HasTemplates() {
		label, hint := statusText(status)
		v.Render(w, r, status, "errors/error.html", view.PageData{
			Title: fmt.Sprintf("%d %s", status, label),
			Data: map[string]any{
				"Status":  status,
				"Label":   label,
				"Message": msg,
				"Hint":    hint,
			},
		})
		return
	}
	http.Error(w, msg, status)
}

// statusText returns the status name and a short explanation of what it means for the user.
func statusText(status int) (label, hint string) {
	switch status {
	case http.StatusBadRequest:
		return "Permintaan Tidak Valid", "Data yang dikirim tidak sesuai format. Periksa kembali isian Anda, lalu coba lagi."
	case http.StatusForbidden:
		return "Akses Ditolak", "Anda sudah login, tetapi tidak memiliki hak akses untuk halaman atau aksi ini. Hubungi Platform Admin bila Anda membutuhkan akses."
	case http.StatusNotFound:
		return "Tidak Ditemukan", "Halaman atau data yang diminta tidak ada. Periksa kembali alamat URL, misalnya ada karakter berlebih di akhir alamat."
	case http.StatusConflict:
		return "Konflik", "Aksi tidak dapat dilakukan karena bertentangan dengan kondisi data saat ini."
	case http.StatusBadGateway:
		return "Database Tidak Dapat Dihubungi", "Platform tidak dapat terhubung ke database target. Pastikan database tersebut menyala, lalu coba lagi."
	default:
		return "Kesalahan Server", "Terjadi kesalahan di sisi server. Silakan coba lagi beberapa saat lagi."
	}
}

func messageOr(appErr *apperr.Error, fallback string) string {
	if appErr != nil && appErr.Msg != "" {
		return appErr.Msg
	}
	return fallback
}

func userID(r *http.Request) int64 {
	if r == nil {
		return 0
	}
	p, ok := authz.PrincipalFrom(r.Context())
	if !ok {
		return 0
	}
	return p.UserID
}

// Redirect sends 303 See Other and stores an optional flash cookie.
func Redirect(w http.ResponseWriter, r *http.Request, url string, flash *view.Flash) {
	view.SetFlashCookie(w, flash)
	http.Redirect(w, r, url, http.StatusSeeOther)
}
