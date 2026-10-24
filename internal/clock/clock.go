package clock

import (
	"sync"
	"time"
)

// Clock is the time source for the application. Tests use Fake.
type Clock interface {
	Now() time.Time
}

// Real returns the wall clock.
type Real struct{}

// Now returns time.Now().
func (Real) Now() time.Time { return time.Now() }

// Fake is a clock tests can move forward without sleeping.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake starts a fake clock at t.
func NewFake(t time.Time) *Fake {
	return &Fake{t: t}
}

// Now returns the frozen time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance moves the clock forward.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// Set replaces the frozen time.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t
}
