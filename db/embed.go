// Package db berisi file SQL migrasi platform DB yang di-embed ke binary.
//
// Pemilik: C (runner di internal/platformdb). Isi file: lihat range nomor di pembagian.md.
package db

import "embed"

// PlatformMigrations berisi platform_migrations/*.sql.
//
//go:embed platform_migrations/*.sql
var PlatformMigrations embed.FS
