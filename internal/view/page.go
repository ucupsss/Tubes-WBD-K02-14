package view

import (
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

// Flash is a one-shot message shown after a redirect.
type Flash struct {
	Kind    string // "success" | "error"
	Message string
}

// FlashSuccess builds a success flash.
func FlashSuccess(msg string) *Flash {
	return &Flash{Kind: "success", Message: msg}
}

// FlashError builds an error flash.
func FlashError(msg string) *Flash {
	return &Flash{Kind: "error", Message: msg}
}

// PageData is the root object passed to every template.
type PageData struct {
	Title     string
	Principal *authz.Principal
	CSRFToken string
	Flash     *Flash
	Data      any
}
