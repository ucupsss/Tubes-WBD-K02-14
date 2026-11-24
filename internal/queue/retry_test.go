package queue

import (
	"testing"
	"time"
)

func TestRetryable(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"08006", "08001", "08", "40001", "40P01", "57P01", "53300"} {
		if !Retryable(code) {
			t.Fatalf("%s harus di-retry", code)
		}
	}
	for _, code := range []string{"", "42601", "23505", "42P01", "57014"} {
		if Retryable(code) {
			t.Fatalf("%s tidak boleh di-retry", code)
		}
	}
}

func TestBackoff_StopsAtThirdAttempt(t *testing.T) {
	t.Parallel()
	d, ok := Backoff(1)
	if !ok || d != 5*time.Second {
		t.Fatalf("attempt 1 = %s %v", d, ok)
	}
	d, ok = Backoff(2)
	if !ok || d != 15*time.Second {
		t.Fatalf("attempt 2 = %s %v", d, ok)
	}
	if _, ok = Backoff(3); ok {
		t.Fatal("attempt 3 adalah yang terakhir")
	}
}
