package view

import (
	"encoding/base64"
	"net/http"
	"os"
	"strings"
)

const FlashCookie = "dbmp_flash"

// EncodeFlash packs a flash message into a cookie value.
func EncodeFlash(f *Flash) string {
	if f == nil {
		return ""
	}
	payload := f.Kind + "\n" + f.Message
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// DecodeFlash unpacks a cookie value. It returns nil when the value is unusable.
func DecodeFlash(v string) *Flash {
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil
	}
	kind, msg, ok := strings.Cut(string(raw), "\n")
	if !ok || (kind != "success" && kind != "error") || msg == "" {
		return nil
	}
	return &Flash{Kind: kind, Message: msg}
}

// SetFlashCookie stores a one-shot flash message. Secure is set only in production.
func SetFlashCookie(w http.ResponseWriter, f *Flash) {
	if f == nil || f.Message == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     FlashCookie,
		Value:    EncodeFlash(f),
		Path:     "/",
		MaxAge:   120,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   os.Getenv("APP_ENV") == "production",
	})
}

func clearFlashCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     FlashCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   os.Getenv("APP_ENV") == "production",
	})
}

func consumeFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(FlashCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	clearFlashCookie(w)
	return DecodeFlash(c.Value)
}
