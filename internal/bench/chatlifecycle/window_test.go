package chatlifecycle

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestCounterWindowExpiresWrapsAndRejectsUnsafeInput(t *testing.T) {
	window, err := NewCounterWindow(time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1_000, 0)
	for _, sample := range []struct {
		at          time.Time
		numerator   uint64
		denominator uint64
	}{
		{start, 1, 100},
		{start.Add(20 * time.Second), 2, 200},
		{start.Add(40 * time.Second), 3, 300},
	} {
		if err := window.Add(sample.at, sample.numerator, sample.denominator); err != nil {
			t.Fatal(err)
		}
	}
	if gotNumerator, gotDenominator := window.Sum(); gotNumerator != 6 || gotDenominator != 600 {
		t.Fatalf("initial sum = %d/%d, want 6/600", gotNumerator, gotDenominator)
	}
	if err := window.Add(start.Add(time.Minute), 4, 400); err != nil {
		t.Fatalf("wrap after exact expiry: %v", err)
	}
	if gotNumerator, gotDenominator := window.Sum(); gotNumerator != 9 || gotDenominator != 900 {
		t.Fatalf("wrapped sum = %d/%d, want 9/900", gotNumerator, gotDenominator)
	}
	if err := window.Add(start.Add(59*time.Second), 1, 1); !errors.Is(err, ErrWindowTimeRegression) {
		t.Fatalf("backward time error = %v, want %v", err, ErrWindowTimeRegression)
	}
	if err := window.Add(start.Add(70*time.Second), math.MaxUint64, 1); !errors.Is(err, ErrWindowOverflow) {
		t.Fatalf("overflow error = %v, want %v", err, ErrWindowOverflow)
	}
	if gotNumerator, gotDenominator := window.Sum(); gotNumerator != 9 || gotDenominator != 900 {
		t.Fatalf("rejected input mutated sum = %d/%d", gotNumerator, gotDenominator)
	}
	if err := window.Add(start.Add(70*time.Second), 1, 1); !errors.Is(err, ErrWindowCapacity) {
		t.Fatalf("unexpired capacity error = %v, want %v", err, ErrWindowCapacity)
	}
}

func TestGaugeWindowUsesExactOverflowSafeGrowthBoundary(t *testing.T) {
	window, err := NewGaugeWindow(6*time.Hour, 7)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(2_000, 0)
	for hour := 0; hour <= 6; hour++ {
		value := uint64(100)
		if hour == 6 {
			value = 105
		}
		if err := window.Add(start.Add(time.Duration(hour)*time.Hour), value); err != nil {
			t.Fatal(err)
		}
	}
	ready, exceeded, err := window.GrowthExceeds(5)
	if err != nil || !ready || exceeded {
		t.Fatalf("exact five percent = ready %v exceeded %v err %v, want ready pass", ready, exceeded, err)
	}
	if err := window.Add(start.Add(7*time.Hour), 106); err != nil {
		t.Fatal(err)
	}
	ready, exceeded, err = window.GrowthExceeds(5)
	if err != nil || !ready || !exceeded {
		t.Fatalf("six percent = ready %v exceeded %v err %v, want ready failure", ready, exceeded, err)
	}

	overflowSafe, err := NewGaugeWindow(time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := overflowSafe.Add(start, math.MaxUint64-1); err != nil {
		t.Fatal(err)
	}
	if err := overflowSafe.Add(start.Add(time.Hour), math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	ready, exceeded, err = overflowSafe.GrowthExceeds(100)
	if err != nil || !ready || exceeded {
		t.Fatalf("large exact comparison = ready %v exceeded %v err %v", ready, exceeded, err)
	}
}

func TestRollingWindowsRemainConstantMemoryFor72Hours(t *testing.T) {
	start := time.Unix(3_000, 0)
	minute, err := NewCounterWindow(time.Minute, 13)
	if err != nil {
		t.Fatal(err)
	}
	for elapsed := time.Duration(0); elapsed <= 72*time.Hour; elapsed += 5 * time.Second {
		if err := minute.Add(start.Add(elapsed), 1, 1); err != nil {
			t.Fatalf("minute at %v: %v", elapsed, err)
		}
	}
	if minute.Len() > minute.Capacity() || minute.Capacity() != 13 {
		t.Fatalf("minute retention = %d/%d", minute.Len(), minute.Capacity())
	}

	heap, err := NewGaugeWindow(6*time.Hour, 7)
	if err != nil {
		t.Fatal(err)
	}
	gro, err := NewGaugeWindow(24*time.Hour, 25)
	if err != nil {
		t.Fatal(err)
	}
	for hour := 0; hour <= 72; hour++ {
		at := start.Add(time.Duration(hour) * time.Hour)
		if err := heap.Add(at, uint64(hour+1)); err != nil {
			t.Fatal(err)
		}
		if err := gro.Add(at, uint64(hour+1)); err != nil {
			t.Fatal(err)
		}
	}
	if heap.Len() > 7 || gro.Len() > 25 {
		t.Fatalf("resource retention = heap %d goroutine %d", heap.Len(), gro.Len())
	}
}
