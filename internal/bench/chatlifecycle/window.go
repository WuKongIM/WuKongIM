package chatlifecycle

import (
	"errors"
	"math"
	"math/bits"
	"time"
)

var (
	// ErrWindowConfig rejects a non-positive span or capacity.
	ErrWindowConfig = errors.New("chat lifecycle window: invalid configuration")
	// ErrWindowTimeRegression rejects samples older than the last accepted sample.
	ErrWindowTimeRegression = errors.New("chat lifecycle window: time regressed")
	// ErrWindowCapacity rejects evidence that would overwrite an unexpired sample.
	ErrWindowCapacity = errors.New("chat lifecycle window: unexpired capacity exhausted")
	// ErrWindowOverflow rejects arithmetic that cannot be represented exactly.
	ErrWindowOverflow = errors.New("chat lifecycle window: counter overflow")
)

type counterWindowEntry struct {
	at                     time.Time
	numerator, denominator uint64
}

// CounterWindow retains exact rational deltas inside one fixed-capacity rolling
// time window. An unexpired entry is never overwritten.
type CounterWindow struct {
	span                   time.Duration
	entries                []counterWindowEntry
	head, size             int
	numerator, denominator uint64
	last                   time.Time
}

// NewCounterWindow allocates all storage used by the reducer.
func NewCounterWindow(span time.Duration, capacity int) (*CounterWindow, error) {
	if span <= 0 || capacity <= 0 {
		return nil, ErrWindowConfig
	}
	return &CounterWindow{span: span, entries: make([]counterWindowEntry, capacity)}, nil
}

// Add expires samples at least one full span old and appends one exact delta.
func (w *CounterWindow) Add(at time.Time, numerator, denominator uint64) error {
	if w == nil || at.IsZero() {
		return ErrWindowConfig
	}
	if !w.last.IsZero() && at.Before(w.last) {
		return ErrWindowTimeRegression
	}
	expired := 0
	nextNumerator, nextDenominator := w.numerator, w.denominator
	for expired < w.size {
		entry := w.entries[(w.head+expired)%len(w.entries)]
		if at.Sub(entry.at) < w.span {
			break
		}
		nextNumerator -= entry.numerator
		nextDenominator -= entry.denominator
		expired++
	}
	if math.MaxUint64-nextNumerator < numerator || math.MaxUint64-nextDenominator < denominator {
		return ErrWindowOverflow
	}
	if w.size-expired == len(w.entries) {
		return ErrWindowCapacity
	}
	w.head = (w.head + expired) % len(w.entries)
	w.size -= expired
	w.numerator = nextNumerator + numerator
	w.denominator = nextDenominator + denominator
	position := (w.head + w.size) % len(w.entries)
	w.entries[position] = counterWindowEntry{at: at, numerator: numerator, denominator: denominator}
	w.size++
	w.last = at
	return nil
}

// Sum returns the exact aggregate retained by the current window.
func (w *CounterWindow) Sum() (uint64, uint64) {
	if w == nil {
		return 0, 0
	}
	return w.numerator, w.denominator
}

// Len returns the current number of retained deltas.
func (w *CounterWindow) Len() int {
	if w == nil {
		return 0
	}
	return w.size
}

// Capacity returns the immutable sample bound.
func (w *CounterWindow) Capacity() int {
	if w == nil {
		return 0
	}
	return len(w.entries)
}

type gaugeWindowEntry struct {
	at    time.Time
	value uint64
}

// GaugeWindow retains fixed-capacity endpoints for an exact rolling slope.
type GaugeWindow struct {
	span       time.Duration
	entries    []gaugeWindowEntry
	head, size int
	last       time.Time
}

// NewGaugeWindow allocates all storage used by the reducer.
func NewGaugeWindow(span time.Duration, capacity int) (*GaugeWindow, error) {
	if span <= 0 || capacity <= 1 {
		return nil, ErrWindowConfig
	}
	return &GaugeWindow{span: span, entries: make([]gaugeWindowEntry, capacity)}, nil
}

// Add retains the newest sample and the exact-span predecessor without
// overwriting evidence still required for a slope.
func (w *GaugeWindow) Add(at time.Time, value uint64) error {
	if w == nil || at.IsZero() {
		return ErrWindowConfig
	}
	if !w.last.IsZero() && at.Before(w.last) {
		return ErrWindowTimeRegression
	}
	expired := 0
	for expired < w.size {
		entry := w.entries[(w.head+expired)%len(w.entries)]
		if at.Sub(entry.at) <= w.span {
			break
		}
		expired++
	}
	if w.size-expired == len(w.entries) {
		return ErrWindowCapacity
	}
	w.head = (w.head + expired) % len(w.entries)
	w.size -= expired
	position := (w.head + w.size) % len(w.entries)
	w.entries[position] = gaugeWindowEntry{at: at, value: value}
	w.size++
	w.last = at
	return nil
}

// GrowthExceeds reports whether the newest value grew by strictly more than
// percent from an endpoint covering the complete configured span.
func (w *GaugeWindow) GrowthExceeds(percent uint64) (ready, exceeded bool, err error) {
	if w == nil || percent > 100 {
		return false, false, ErrWindowConfig
	}
	if w.size < 2 {
		return false, false, nil
	}
	oldest := w.entries[w.head]
	newest := w.entries[(w.head+w.size-1)%len(w.entries)]
	if newest.at.Sub(oldest.at) < w.span {
		return false, false, nil
	}
	if newest.value <= oldest.value {
		return true, false, nil
	}
	if oldest.value == 0 {
		return true, true, nil
	}
	deltaHigh, deltaLow := bits.Mul64(newest.value-oldest.value, 100)
	baseHigh, baseLow := bits.Mul64(oldest.value, percent)
	return true, deltaHigh > baseHigh || (deltaHigh == baseHigh && deltaLow > baseLow), nil
}

// Len returns the current number of retained gauge samples.
func (w *GaugeWindow) Len() int {
	if w == nil {
		return 0
	}
	return w.size
}

// Capacity returns the immutable sample bound.
func (w *GaugeWindow) Capacity() int {
	if w == nil {
		return 0
	}
	return len(w.entries)
}
