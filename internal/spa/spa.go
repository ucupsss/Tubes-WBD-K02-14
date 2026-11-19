// Package spa menyajikan hasil build frontend (Single-Page Application) yang di-embed ke binary.
//
// File statis hasil build (assets/*.js, *.css, ...) dilayani apa adanya. Path lain yang tidak cocok dengan
// file atau route server mana pun dijawab dengan index.html supaya router SPA di browser yang menanganinya
// (mis. /login, /admin/groups/3 saat halaman di-refresh).
//
// Pemilik: A.
package spa

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// Handler menyajikan SPA dari subfolder "dist" sebuah fs.FS (biasanya web.FS).
type Handler struct {
	files fs.FS
	index []byte
}

// New membaca dist/index.html. Bila belum ada (frontend belum di-build), Available() bernilai false
// dan server tetap berjalan dengan halaman error bawaan.
func New(fsys fs.FS) *Handler {
	sub, err := fs.Sub(fsys, "dist")
	if err != nil {
		return &Handler{}
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return &Handler{files: sub}
	}
	return &Handler{files: sub, index: index}
}

// Available melaporkan apakah build frontend tersedia.
func (h *Handler) Available() bool { return len(h.index) > 0 }

// Serve menjawab request GET/HEAD dan mengembalikan true bila SPA yang menjawab.
// Mengembalikan false (tanpa menulis apa pun) bila request bukan untuk SPA: build tidak ada, metode bukan
// GET/HEAD, atau meminta file ber-ekstensi yang tidak ada (supaya tidak ada HTML yang menyamar sebagai JS/CSS).
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) bool {
	if !h.Available() || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if info, err := fs.Stat(h.files, name); err == nil && !info.IsDir() {
			setSecurityHeaders(w)
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, h.files, name)
			return true
		}
		if path.Ext(name) != "" {
			return false
		}
	}
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.index))
	return true
}

func setSecurityHeaders(w http.ResponseWriter) {
	hd := w.Header()
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "same-origin")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
}
