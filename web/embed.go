// Package web berisi aset frontend yang di-embed ke binary:
//   - dist/      hasil build SPA (frontend/, dibuat oleh `make web-build`); disajikan oleh internal/spa.
//   - templates/ dan static/  halaman HTML lama milik explorer/migration/explain yang belum dipindah ke SPA.
//
// Pemilik: A (SPA), C (template lama). Kode render template ada di internal/view.
package web

import "embed"

// FS berisi folder templates/, static/, dan dist/ (all: agar .gitkeep ikut, sehingga build Go tetap
// jalan sebelum frontend di-build).
//
//go:embed templates static all:dist
var FS embed.FS
