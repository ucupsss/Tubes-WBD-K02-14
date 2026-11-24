package dbconn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Kesalahan kunci dan ciphertext. Jangan bungkus dengan detail DSN.
var (
	ErrInvalidKeyLength = errors.New("secret key must be 32 bytes (AES-256)")
	ErrCiphertextShort  = errors.New("ciphertext too short")
	ErrDecryptFailed    = errors.New("failed to decrypt ciphertext")
)

// ParseSecretKey mengonversi secret key (string base64 atau raw 32-byte) menjadi 32 bytes slice.
func ParseSecretKey(keyStr string) ([]byte, error) {
	// Coba decode base64 jika memungkinkan
	if decoded, err := base64.StdEncoding.DecodeString(keyStr); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	// Coba URL-safe base64
	if decoded, err := base64.URLEncoding.DecodeString(keyStr); err == nil && len(decoded) == 32 {
		return decoded, nil
	}

	raw := []byte(keyStr)
	if len(raw) == 32 {
		return raw, nil
	}

	return nil, ErrInvalidKeyLength
}

// Encrypt mengenkripsi plaintext menggunakan AES-256-GCM dengan secretKey 32 bytes.
// Format output: nonce (12 byte) + ciphertext & auth tag.
func Encrypt(plaintext []byte, secretKey []byte) ([]byte, error) {
	if len(secretKey) != 32 {
		return nil, ErrInvalidKeyLength
	}

	block, err := aes.NewCipher(secretKey)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("rand.Reader: %w", err)
	}

	// Seal menambahkan ciphertext ke slice nonce (nonce + ciphertext)
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt mendekripsi data yang dienkripsi oleh Encrypt menggunakan AES-256-GCM.
func Decrypt(ciphertext []byte, secretKey []byte) ([]byte, error) {
	if len(secretKey) != 32 {
		return nil, ErrInvalidKeyLength
	}

	block, err := aes.NewCipher(secretKey)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrCiphertextShort
	}

	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}

	return plaintext, nil
}
