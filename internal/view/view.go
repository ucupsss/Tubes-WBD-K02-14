package view

import (
	"net/http"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

type Flash struct {
	Kind    string // "success" | "error"
	Message string
}

func FlashSuccess(msg string) *Flash {
	return &Flash{Kind: "success", Message: msg}
}

func FlashError(msg string) *Flash {
	return &Flash{Kind: "error", Message: msg}
}

type PageData struct {
	Title     string
	Principal *authz.Principal
	CSRFToken string
	Flash     *Flash
	Data      any
}

// View mengelola rendering template HTML.
type View struct{}

func New() *View {
	return &View{}
}

func (v *View) Render(w http.ResponseWriter, r *http.Request, status int, name string, data PageData) {
	w.WriteHeader(status)
}
