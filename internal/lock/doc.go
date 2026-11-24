// Package lock menyimpan lease eksekusi per database target (D34).
//
// Satu database hanya punya satu execution berjalan. Lease diperpanjang heartbeat
// dan boleh diambil alih setelah kedaluwarsa, setelah bukti di target diperiksa.
package lock
