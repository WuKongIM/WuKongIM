package chatlifecycle

import (
	"errors"
	"math"
	"testing"
)

func TestRateTickGrantsExactGlobalTargetWithoutDrift(t *testing.T) {
	allocator, err := NewRateAllocator(2_000, 4_000, []int64{1, 1, 1})
	if err != nil {
		t.Fatalf("NewRateAllocator() error = %v", err)
	}

	var workerTotals [3]uint64
	for tick := 0; tick < 3_003; tick++ {
		result, err := allocator.Tick([]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
		if err != nil {
			t.Fatalf("Tick(%d) error = %v", tick, err)
		}
		if got := sumUint64(result.Fresh); got != 2_000 {
			t.Fatalf("tick %d fresh total = %d, want 2000", tick, got)
		}
		if got := sumUint64(result.Released); got != 2_000 {
			t.Fatalf("tick %d released total = %d, want 2000", tick, got)
		}
		for worker := range workerTotals {
			workerTotals[worker] += result.Fresh[worker]
		}
	}
	if workerTotals[0] != workerTotals[1] || workerTotals[1] != workerTotals[2] {
		t.Fatalf("equal-weight totals = %v, want no long-run drift", workerTotals)
	}
}

func TestRateUnusedCreditExpiresAtTwoSecondsAndBurstIsGlobal(t *testing.T) {
	allocator, err := NewRateAllocator(2_000, 4_000, []int64{1, 1, 1})
	if err != nil {
		t.Fatalf("NewRateAllocator() error = %v", err)
	}
	zeroDemand := []uint64{0, 0, 0}
	for tick := 0; tick < 3; tick++ {
		result, err := allocator.Tick(zeroDemand)
		if err != nil {
			t.Fatalf("Tick(%d) error = %v", tick, err)
		}
		wantCredit := uint64(2_000 * (tick + 1))
		if wantCredit > 4_000 {
			wantCredit = 4_000
		}
		if got := sumUint64(result.Credit); got != wantCredit {
			t.Fatalf("tick %d retained credit = %d, want %d", tick, got, wantCredit)
		}
	}

	result, err := allocator.Tick([]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatalf("burst Tick() error = %v", err)
	}
	if got := sumUint64(result.Released); got != 4_000 {
		t.Fatalf("global burst = %d, want 4000", got)
	}
	for worker, released := range result.Released {
		if released >= 4_000 {
			t.Fatalf("worker %d release = %d, must not own the global burst", worker, released)
		}
	}
}

func TestRateUpdateAppliesOnNextTickWithoutRetroactiveDebt(t *testing.T) {
	allocator, err := NewRateAllocator(2_000, 4_000, []int64{2, 3, 5})
	if err != nil {
		t.Fatalf("NewRateAllocator() error = %v", err)
	}
	first, err := allocator.Tick([]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatalf("first Tick() error = %v", err)
	}
	if got := sumUint64(first.Fresh); got != 2_000 {
		t.Fatalf("first fresh = %d, want 2000", got)
	}
	if err := allocator.ScheduleRate(2_500, 5_000); err != nil {
		t.Fatalf("ScheduleRate() error = %v", err)
	}
	second, err := allocator.Tick([]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatalf("second Tick() error = %v", err)
	}
	if got := sumUint64(second.Fresh); got != 2_500 {
		t.Fatalf("updated fresh = %d, want 2500", got)
	}
	if got := sumUint64(second.Released); got != 2_500 {
		t.Fatalf("updated release = %d, want 2500 with no retroactive debt", got)
	}

	withCredit, err := NewRateAllocator(2_000, 4_000, []int64{2, 3, 5})
	if err != nil {
		t.Fatalf("NewRateAllocator(with credit) error = %v", err)
	}
	if _, err := withCredit.Tick([]uint64{0, 0, 0}); err != nil {
		t.Fatalf("credit Tick() error = %v", err)
	}
	if err := withCredit.ScheduleRate(1_000, 2_000); err != nil {
		t.Fatalf("ScheduleRate(lower) error = %v", err)
	}
	updated, err := withCredit.Tick([]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatalf("updated Tick() error = %v", err)
	}
	if got := sumUint64(updated.Released); got != 1_000 {
		t.Fatalf("updated release with old credit = %d, want only new 1000", got)
	}
}

func TestRateTwoTickCreditIsExactAcrossUnequalWeights(t *testing.T) {
	for rate := uint64(1); rate <= 20; rate++ {
		for first := int64(1); first <= 5; first++ {
			for second := int64(1); second <= 5; second++ {
				allocator, err := NewRateAllocator(rate, 2*rate, []int64{first, second, 3})
				if err != nil {
					t.Fatalf("NewRateAllocator(%d,%d,%d) error = %v", rate, first, second, err)
				}
				var result RateTick
				for tick := 0; tick < 3; tick++ {
					result, err = allocator.Tick([]uint64{0, 0, 0})
					if err != nil {
						t.Fatalf("Tick(%d) error = %v", tick, err)
					}
					want := rate * uint64(tick+1)
					if want > 2*rate {
						want = 2 * rate
					}
					if got := sumUint64(result.Credit); got != want {
						t.Fatalf("rate=%d weights=%v tick=%d credit=%d, want %d", rate, []int64{first, second, 3}, tick, got, want)
					}
				}
			}
		}
	}
}

func TestRateRejectsUnboundedOrInvalidState(t *testing.T) {
	tests := []struct {
		name    string
		rate    uint64
		burst   uint64
		weights []int64
		want    error
	}{
		{name: "zero rate", burst: 1, weights: []int64{1}, want: errRateRequired},
		{name: "burst below rate", rate: 2, burst: 1, weights: []int64{1}, want: errRateBurst},
		{name: "burst exceeds two ticks", rate: 1, burst: 3, weights: []int64{1}, want: errRateBurst},
		{name: "empty weights", rate: 1, burst: 2, want: errRateWorkers},
		{name: "zero weight", rate: 1, burst: 2, weights: []int64{1, 0}, want: errRateWeight},
		{name: "negative weight", rate: 1, burst: 2, weights: []int64{1, -1}, want: errRateWeight},
		{name: "weight overflow", rate: 1, burst: 2, weights: []int64{math.MaxInt64, math.MaxInt64, 2}, want: errRateWeightTotal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRateAllocator(tt.rate, tt.burst, tt.weights)
			if !errors.Is(err, tt.want) {
				t.Fatalf("NewRateAllocator() error = %v, want %v", err, tt.want)
			}
		})
	}

	allocator, err := NewRateAllocator(1, 2, []int64{1, 1})
	if err != nil {
		t.Fatalf("NewRateAllocator() error = %v", err)
	}
	if _, err := allocator.Tick([]uint64{1}); !errors.Is(err, errRateDemandCount) {
		t.Fatalf("Tick(short demand) error = %v, want %v", err, errRateDemandCount)
	}
	if err := allocator.ScheduleRate(0, 1); !errors.Is(err, errRateRequired) {
		t.Fatalf("ScheduleRate(zero) error = %v, want %v", err, errRateRequired)
	}
}

func sumUint64(values []uint64) uint64 {
	var total uint64
	for _, value := range values {
		total += value
	}
	return total
}
