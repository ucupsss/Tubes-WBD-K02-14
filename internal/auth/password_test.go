package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("rahasia123")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "rahasia123" || !strings.HasPrefix(hash, "$2a$") {
		t.Fatalf("hash tidak terlihat seperti bcrypt: %q", hash)
	}
	if cost, _ := bcrypt.Cost([]byte(hash)); cost != BcryptCost {
		t.Fatalf("cost = %d, want %d", cost, BcryptCost)
	}
	if !CheckPassword(hash, "rahasia123") {
		t.Fatal("password benar harus cocok")
	}
	if CheckPassword(hash, "rahasia124") {
		t.Fatal("password salah tidak boleh cocok")
	}
}

func TestHashPassword_UniqueSalt(t *testing.T) {
	a, err := HashPassword("samepassword")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("samepassword")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("dua hash dari password yang sama harus berbeda (salt)")
	}
}

func TestHashPassword_TooLong(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordBytes+1)); err == nil {
		t.Fatal("password > 72 byte harus ditolak")
	}
}

func TestCheckPassword_RejectsBcryptTruncation(t *testing.T) {
	base := strings.Repeat("a", MaxPasswordBytes)
	hash, err := HashPassword(base)
	if err != nil {
		t.Fatal(err)
	}
	// bcrypt mengabaikan byte ke-73 dst.; kita harus menolaknya, bukan menganggapnya cocok.
	if CheckPassword(hash, base+"extra") {
		t.Fatal("password > 72 byte tidak boleh cocok")
	}
}

func TestCheckPassword_InvalidHash(t *testing.T) {
	if CheckPassword("bukan-hash", "apapun123") {
		t.Fatal("hash rusak tidak boleh cocok")
	}
}

func TestDummyHash_IsValidBcryptCost(t *testing.T) {
	cost, err := bcrypt.Cost([]byte(dummyHash))
	if err != nil {
		t.Fatalf("dummyHash bukan bcrypt valid: %v", err)
	}
	if cost != BcryptCost {
		t.Fatalf("dummyHash cost = %d, want %d (supaya timing sama)", cost, BcryptCost)
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"empty", "", true},
		{"too short", "1234567", true},
		{"min length", "12345678", false},
		{"multibyte counted as chars", "ééééééé", true},
		{"max bytes", strings.Repeat("a", MaxPasswordBytes), false},
		{"over max bytes", strings.Repeat("a", MaxPasswordBytes+1), true},
		{"multibyte over 72 bytes", strings.Repeat("é", 37), true}, // 74 byte
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidatePassword(tt.pw); (got != "") != tt.wantErr {
				t.Fatalf("ValidatePassword(%q) = %q, wantErr %v", tt.pw, got, tt.wantErr)
			}
		})
	}
}
