package view

import (
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
)

// View renders HTML pages. A view from New has no templates and only writes the status code,
// which keeps handler unit tests that do not care about HTML working.
type View struct {
	base *template.Template
	fsys fs.FS
	csrf func(*http.Request) string
}

// New returns a template-less view. Render only writes the HTTP status.
func New() *View {
	return &View{}
}

// MustNew parses layout templates from fsys (typically web.FS). It panics on failure
// because a server cannot serve pages without them.
func MustNew(fsys fs.FS) *View {
	v, err := NewFromFS(fsys)
	if err != nil {
		panic(err)
	}
	return v
}

// NewFromFS parses templates/layout/*.html from fsys.
func NewFromFS(fsys fs.FS) (*View, error) {
	root := template.New("root").Funcs(funcMap())
	root, err := root.ParseFS(fsys, "templates/layout/*.html")
	if err != nil {
		return nil, err
	}
	return &View{base: root, fsys: fsys}, nil
}

// SetCSRF registers the function used to fill PageData.CSRFToken.
// Leave it unset until the csrf package exports a token reader.
func (v *View) SetCSRF(fn func(*http.Request) string) {
	if v == nil {
		return
	}
	v.csrf = fn
}

// HasTemplates reports whether Render will execute HTML.
func (v *View) HasTemplates() bool {
	return v != nil && v.base != nil
}

// Render writes an HTML page. Principal and CSRF are filled when the caller left them empty.
// A flash cookie from the previous redirect is shown once and cleared.
func (v *View) Render(w http.ResponseWriter, r *http.Request, status int, name string, data PageData) {
	if v == nil || v.base == nil {
		w.WriteHeader(status)
		return
	}
	if data.Principal == nil {
		if p, ok := authz.PrincipalFrom(r.Context()); ok {
			cp := p
			data.Principal = &cp
		}
	}
	if data.CSRFToken == "" && v.csrf != nil {
		data.CSRFToken = v.csrf(r)
	}
	if data.Flash == nil {
		data.Flash = consumeFlash(w, r)
	}

	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		slog.Error("template name rejected", "template", name)
		http.Error(w, "Terjadi kesalahan pada server", http.StatusInternalServerError)
		return
	}

	clone, err := v.base.Clone()
	if err != nil {
		slog.Error("template clone", "err", err, "template", name)
		http.Error(w, "Terjadi kesalahan pada server", http.StatusInternalServerError)
		return
	}
	// B's EXPLAIN partial is optional. The detail page names it only when ShowPlan is set,
	// but html/template still requires the name to exist while escaping the page.
	if _, statErr := fs.Stat(v.fsys, "templates/explain/plan.html"); statErr == nil {
		if _, err := clone.ParseFS(v.fsys, "templates/explain/plan.html"); err != nil {
			slog.Error("template parse", "err", err, "template", "explain/plan.html")
			http.Error(w, "Terjadi kesalahan pada server", http.StatusInternalServerError)
			return
		}
	}
	if _, err := clone.ParseFS(v.fsys, "templates/"+name); err != nil {
		slog.Error("template parse", "err", err, "template", name)
		http.Error(w, "Terjadi kesalahan pada server", http.StatusInternalServerError)
		return
	}
	if clone.Lookup("explain_plan") == nil {
		if _, err := clone.Parse(`{{ define "explain_plan" }}{{ end }}`); err != nil {
			slog.Error("template parse", "err", err, "template", "explain_plan")
			http.Error(w, "Terjadi kesalahan pada server", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := clone.ExecuteTemplate(w, "layout", data); err != nil {
		slog.Error("template execute", "err", err, "template", name)
	}
}
