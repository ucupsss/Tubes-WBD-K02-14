package logx

import (
	"strings"
	"unicode"
)

// Safe membersihkan nilai dari user (path URL, username) sebelum ditulis ke log: newline dan
// karakter kontrol lain dibuang supaya tidak bisa memalsukan baris log, lalu dipotong ke
// MaxValueLen karakter supaya satu request tidak bisa membanjiri log. Handler JSON sudah
// meng-escape karakter kontrol; Safe tetap dipakai agar log aman walau handler atau tujuan log berganti.
func Safe(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return Truncate(s, MaxValueLen)
}
