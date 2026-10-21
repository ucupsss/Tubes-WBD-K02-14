// Package web berisi aset frontend (templates & static) yang di-embed ke binary.
//
// Pemilik: C. Kode render ada di internal/view.
package web

import "embed"

// FS berisi folder templates/ dan static/.
//
//go:embed templates static
var FS embed.FS
