package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const (
	BcryptCost        = 12
	MinPasswordLength = 8
	MaxPasswordBytes  = 72 // batas bcrypt; byte sesudahnya diabaikan diam-diam, jadi ditolak
)

// ValidatePassword mengembalikan pesan error untuk user, atau "" bila valid.
func ValidatePassword(password string) string {
	switch {
	case len([]rune(password)) < MinPasswordLength:
		return fmt.Sprintf("Password minimal %d karakter", MinPasswordLength)
	case len(password) > MaxPasswordBytes:
		return fmt.Sprintf("Password maksimal %d byte", MaxPasswordBytes)
	}
	return ""
}

// HashPassword membuat hash bcrypt. Panjang password harus sudah divalidasi pemanggil.
func HashPassword(password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", fmt.Errorf("auth.HashPassword: password lebih dari %d byte", MaxPasswordBytes)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth.HashPassword: %w", err)
	}
	return string(h), nil
}

// CheckPassword bernilai true bila password cocok dengan hash.
func CheckPassword(hash, password string) bool {
	if len(password) > MaxPasswordBytes {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// dummyHash dipakai saat username tidak ditemukan, supaya waktu respons login sama
// dengan saat username ada (mencegah user enumeration lewat timing). Hash bcrypt cost 12
// dari string acak yang tidak disimpan, jadi tidak ada password yang cocok dengannya.
const dummyHash = "$2a$12$/sg/2ArmxGts8lzBjRy5oenJumycV4vI5IruN1W/zAizb8ZYCb0IW"

// burnPasswordCheck menjalankan bcrypt terhadap dummy hash dan membuang hasilnya.
func burnPasswordCheck(password string) {
	_ = CheckPassword(dummyHash, password)
}
