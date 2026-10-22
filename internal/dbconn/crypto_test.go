package dbconn_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
)

func TestCrypto_EncryptDecrypt_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}

	testCases := []struct {
		name      string
		plaintext string
	}{
		{"empty string", ""},
		{"simple password", "secret123"},
		{"complex password", "P@$$w0rd!#%^&*()_+~`|}{[]:;?><,./-="},
		{"long text", "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore."},
		{"utf8 characters", "KoneksiDatabase_🚀_日本語_12345"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ciphertext, err := dbconn.Encrypt([]byte(tc.plaintext), key)
			if err != nil {
				t.Fatalf("Encrypt error: %v", err)
			}

			if len(ciphertext) <= 12 && len(tc.plaintext) > 0 {
				t.Fatalf("ciphertext too short: %d", len(ciphertext))
			}

			decrypted, err := dbconn.Decrypt(ciphertext, key)
			if err != nil {
				t.Fatalf("Decrypt error: %v", err)
			}

			if string(decrypted) != tc.plaintext {
				t.Fatalf("got %q, want %q", string(decrypted), tc.plaintext)
			}
		})
	}
}

func TestCrypto_NonDeterministic(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}

	plaintext := []byte("mypassword")
	c1, err := dbconn.Encrypt(plaintext, key)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := dbconn.Encrypt(plaintext, key)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(c1, c2) {
		t.Fatal("two encryptions of the same plaintext must produce different ciphertexts (random nonce)")
	}
}

func TestCrypto_InvalidKey(t *testing.T) {
	invalidKeys := [][]byte{
		nil,
		make([]byte, 0),
		make([]byte, 16),
		make([]byte, 31),
		make([]byte, 33),
	}

	for _, key := range invalidKeys {
		_, err := dbconn.Encrypt([]byte("test"), key)
		if err != dbconn.ErrInvalidKeyLength {
			t.Errorf("Encrypt with key len %d: got %v, want %v", len(key), err, dbconn.ErrInvalidKeyLength)
		}

		_, err = dbconn.Decrypt([]byte("0123456789012345"), key)
		if err != dbconn.ErrInvalidKeyLength {
			t.Errorf("Decrypt with key len %d: got %v, want %v", len(key), err, dbconn.ErrInvalidKeyLength)
		}
	}
}

func TestCrypto_CorruptedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)

	ciphertext, err := dbconn.Encrypt([]byte("sensitivedata"), key)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper one byte
	ciphertext[len(ciphertext)-1] ^= 0xFF

	_, err = dbconn.Decrypt(ciphertext, key)
	if err == nil {
		t.Fatal("expected error when decrypting tampered ciphertext, got nil")
	}

	// Too short ciphertext (less than nonce size 12)
	shortCiphertext := []byte("too_short")
	_, err = dbconn.Decrypt(shortCiphertext, key)
	if err != dbconn.ErrCiphertextShort {
		t.Fatalf("got %v, want %v", err, dbconn.ErrCiphertextShort)
	}
}

func TestCrypto_ParseSecretKey(t *testing.T) {
	raw32 := "12345678901234567890123456789012"
	b64Key := base64.StdEncoding.EncodeToString([]byte(raw32))

	k1, err := dbconn.ParseSecretKey(b64Key)
	if err != nil || len(k1) != 32 {
		t.Fatalf("ParseSecretKey base64 failed: %v", err)
	}

	k2, err := dbconn.ParseSecretKey(raw32)
	if err != nil || len(k2) != 32 {
		t.Fatalf("ParseSecretKey raw failed: %v", err)
	}

	_, err = dbconn.ParseSecretKey("invalid_short")
	if err != dbconn.ErrInvalidKeyLength {
		t.Fatalf("got %v, want %v", err, dbconn.ErrInvalidKeyLength)
	}
}
