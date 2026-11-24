// Package migration mengelola definisi migration, revisi SQL, riwayat execution, dan eksekusi di target.
//
// API JSON membuat execution pending_review. Worker menjalankan SQL lewat Executor setelah
// kunci database dan revalidasi. SQL up/down yang sudah diajukan tidak diubah di tempat.
//
// Pemilik: C. Lihat pembagian.md & panduan-pengerjaan.md.
package migration
