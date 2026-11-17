package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

// IsAPI melaporkan apakah request ditujukan ke API JSON (path diawali /api/).
func IsAPI(r *http.Request) bool {
	return r != nil && strings.HasPrefix(r.URL.Path, "/api/")
}

// ErrorBody adalah bentuk tunggal error JSON untuk semua endpoint API.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail: code stabil untuk mesin, message aman untuk manusia, details opsional.
type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// WriteJSON menulis v sebagai JSON dengan status yang diberikan.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write json", "err", err)
	}
}

// NoContent menulis 204 tanpa body.
func NoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// WriteError memetakan error ke status HTTP dan ErrorBody. Error tak dikenal menjadi 500 generik
// (detail hanya masuk log, tidak pernah ke klien).
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var appErr *apperr.Error
	errors.As(err, &appErr)

	status, code, msg := http.StatusInternalServerError, "internal", "Terjadi kesalahan pada server"
	switch apperr.KindOf(err) {
	case apperr.KindInvalid:
		status, code, msg = http.StatusBadRequest, "invalid", messageOr(appErr, "Permintaan tidak valid")
	case apperr.KindUnauthenticated:
		status, code, msg = http.StatusUnauthorized, "unauthenticated", messageOr(appErr, "Silakan login terlebih dahulu")
	case apperr.KindForbidden:
		status, code, msg = http.StatusForbidden, "forbidden", messageOr(appErr, "Anda tidak memiliki akses")
	case apperr.KindNotFound:
		status, code, msg = http.StatusNotFound, "not_found", messageOr(appErr, "Data tidak ditemukan")
	case apperr.KindConflict:
		status, code, msg = http.StatusConflict, "conflict", messageOr(appErr, "Data bentrok dengan data yang sudah ada")
	case apperr.KindUnavailable:
		status, code, msg = http.StatusBadGateway, "target_unavailable", messageOr(appErr, "Database tidak dapat dihubungi")
	default:
		slog.ErrorContext(r.Context(), "request error",
			"err", err,
			"method", r.Method,
			"path", r.URL.Path,
			"user_id", userID(r),
		)
	}

	details := map[string]any{}
	if appErr != nil {
		if len(appErr.Fields) > 0 {
			details["fields"] = appErr.Fields
		}
		if appErr.Reason != "" {
			details["reason"] = appErr.Reason
		}
	}
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: msg, Details: details}})
}

// DecodeJSON membaca body JSON ke dst. Field tak dikenal dan body berlebih ditolak (400).
func DecodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return apperr.Invalid("Body permintaan kosong")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apperr.Invalid("Body permintaan terlalu besar")
		}
		if errors.Is(err, io.EOF) {
			return apperr.Invalid("Body permintaan kosong")
		}
		return apperr.Invalid("Body permintaan bukan JSON yang valid")
	}
	if dec.More() {
		return apperr.Invalid("Body permintaan bukan JSON yang valid")
	}
	return nil
}
