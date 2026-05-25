package clock

import "time"

// Clock provides time for tests and runtime loops.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}

// SystemClock uses the process wall clock.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }
