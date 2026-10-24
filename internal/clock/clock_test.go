package clock

import (
	"testing"
	"time"
)

func TestFake(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	f := NewFake(start)
	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("now = %s", got)
	}
	f.Advance(31 * time.Minute)
	if got := f.Now(); !got.Equal(start.Add(31 * time.Minute)) {
		t.Fatalf("after advance = %s", got)
	}
	next := start.Add(2 * time.Hour)
	f.Set(next)
	if got := f.Now(); !got.Equal(next) {
		t.Fatalf("after set = %s", got)
	}
}

func TestReal(t *testing.T) {
	t.Parallel()
	before := time.Now().Add(-time.Second)
	got := (Real{}).Now()
	after := time.Now().Add(time.Second)
	if got.Before(before) || got.After(after) {
		t.Fatalf("now = %s", got)
	}
}
