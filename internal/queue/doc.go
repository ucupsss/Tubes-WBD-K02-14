// Package queue adalah antrian job migration di database platform (D23).
//
// API memasukkan baris ke migration_jobs dan langsung mengembalikan job ID.
// Worker lain mengambil baris itu dengan FOR UPDATE SKIP LOCKED.
package queue
