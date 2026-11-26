package logx

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafe_RemovesControlCharactersAndTruncates(t *testing.T) {
	cases := map[string]string{
		"/api/me":                      "/api/me",
		"alice\n{\"event\":\"palsu\"}": "alice{\"event\":\"palsu\"}",
		"a\r\nb\tc\x00d\x1b[31m":       "abcd[31m",
		"":                             "",
		"ünïcödé/путь":                 "ünïcödé/путь",
	}
	for in, want := range cases {
		if got := Safe(in); got != want {
			t.Errorf("Safe(%q) = %q, want %q", in, got, want)
		}
	}

	long := strings.Repeat("é", MaxValueLen+10)
	got := Safe(long)
	if !utf8.ValidString(got) {
		t.Fatalf("hasil potong bukan UTF-8 valid: %q", got)
	}
	if utf8.RuneCountInString(got) != MaxValueLen+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("jumlah rune %d, want %d diakhiri …", utf8.RuneCountInString(got), MaxValueLen+1)
	}
}
