package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const tokenBytes = 32

// newToken membuat token opaque 32 byte dari crypto/rand, di-encode base64url tanpa padding (D4).
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session.newToken: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken adalah satu-satunya bentuk token yang disimpan di platform DB.
func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
