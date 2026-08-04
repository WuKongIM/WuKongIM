package chatlifecycle

import (
	"errors"
	"math/bits"
)

const maxRateWorkers = 1_024

var (
	errRateRequired    = errors.New("chat lifecycle rate: rate must be positive")
	errRateBurst       = errors.New("chat lifecycle rate: global burst must equal exactly two ticks of rate")
	errRateWorkers     = errors.New("chat lifecycle rate: worker count must be in 1..1024")
	errRateWeight      = errors.New("chat lifecycle rate: every worker weight must be positive")
	errRateWeightTotal = errors.New("chat lifecycle rate: worker weight total overflows uint64")
	errRateDemandCount = errors.New("chat lifecycle rate: demand count must equal worker count")
)

// RateTick is one bounded coordinator release. Fresh always sums to the
// configured global rate; Released never exceeds the configured global burst.
type RateTick struct {
	Fresh    []uint64
	Released []uint64
	Credit   []uint64
}

type scheduledRate struct {
	rate  uint64
	burst uint64
}

// RateAllocator apportions one global integer-per-second budget across a
// bounded worker set. It retains at most the configured global burst and gives
// every worker only its weighted share of that burst.
type RateAllocator struct {
	weights    []uint64
	weightSum  uint64
	rate       uint64
	burst      uint64
	phase      uint64
	older      []uint64
	recent     []uint64
	pending    scheduledRate
	hasPending bool
}

// NewRateAllocator copies and validates worker weights. Rate and burst are
// global limits, never per-worker token-bucket capacities.
func NewRateAllocator(rate, burst uint64, weights []int64) (*RateAllocator, error) {
	weightSum, convertedWeights, err := validateRateInputs(rate, burst, weights)
	if err != nil {
		return nil, err
	}
	return &RateAllocator{
		weights:   convertedWeights,
		weightSum: weightSum,
		rate:      rate,
		burst:     burst,
		older:     make([]uint64, len(weights)),
		recent:    make([]uint64, len(weights)),
	}, nil
}

// ScheduleRate stages a capacity-rate update. The old rate remains fully in
// force until the next Tick; applying the update creates no historical debt.
func (a *RateAllocator) ScheduleRate(rate, burst uint64) error {
	if rate == 0 {
		return errRateRequired
	}
	if rate > ^uint64(0)/2 || burst != 2*rate {
		return errRateBurst
	}
	a.pending = scheduledRate{rate: rate, burst: burst}
	a.hasPending = true
	return nil
}

// Tick adds one exact global-rate grant, expires credit beyond the weighted
// global burst, and releases no more than each worker's supplied demand.
func (a *RateAllocator) Tick(demand []uint64) (RateTick, error) {
	if len(demand) != len(a.weights) {
		return RateTick{}, errRateDemandCount
	}
	if a.hasPending {
		a.rate = a.pending.rate
		a.burst = a.pending.burst
		a.hasPending = false
		clear(a.older)
		clear(a.recent)
	}

	fresh := apportionRate(a.rate, a.weights, a.weightSum, a.phase)
	a.phase = addMod(a.phase, a.rate%a.weightSum, a.weightSum)
	released := make([]uint64, len(a.weights))
	creditSnapshot := make([]uint64, len(a.weights))
	for worker := range a.weights {
		// Credit older than the preceding tick expires here. The two fixed
		// generations make both state size and retention age explicit.
		a.older[worker] = a.recent[worker]
		a.recent[worker] = fresh[worker]
		remainingDemand := demand[worker]
		fromOlder := min(remainingDemand, a.older[worker])
		a.older[worker] -= fromOlder
		remainingDemand -= fromOlder
		fromRecent := min(remainingDemand, a.recent[worker])
		a.recent[worker] -= fromRecent
		released[worker] = fromOlder + fromRecent
		creditSnapshot[worker] = a.older[worker] + a.recent[worker]
	}
	return RateTick{Fresh: fresh, Released: released, Credit: creditSnapshot}, nil
}

func validateRateInputs(rate, burst uint64, weights []int64) (uint64, []uint64, error) {
	if rate == 0 {
		return 0, nil, errRateRequired
	}
	if rate > ^uint64(0)/2 || burst != 2*rate {
		return 0, nil, errRateBurst
	}
	if len(weights) == 0 || len(weights) > maxRateWorkers {
		return 0, nil, errRateWorkers
	}
	converted := make([]uint64, len(weights))
	var total uint64
	for index, weight := range weights {
		if weight <= 0 {
			return 0, nil, errRateWeight
		}
		converted[index] = uint64(weight)
		var carry uint64
		total, carry = bits.Add64(total, uint64(weight), 0)
		if carry != 0 {
			return 0, nil, errRateWeightTotal
		}
	}
	return total, converted, nil
}

// apportionRate uses cumulative weighted boundaries. The final boundary is
// always target, so integer grants sum exactly without a correction loop.
func apportionRate(target uint64, weights []uint64, weightSum, phase uint64) []uint64 {
	grants := make([]uint64, len(weights))
	var prefix, previous uint64
	for worker, weight := range weights {
		prefix += weight // validated total makes this addition safe
		high, low := bits.Mul64(target, prefix)
		low, carry := bits.Add64(low, phase, 0)
		high += carry
		boundary, _ := bits.Div64(high, low, weightSum)
		grants[worker] = boundary - previous
		previous = boundary
	}
	return grants
}

func addMod(left, right, modulus uint64) uint64 {
	// Both inputs are below modulus. This form avoids overflowing left+right.
	if left >= modulus-right {
		return left - (modulus - right)
	}
	return left + right
}
