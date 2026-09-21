package metrics

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"
	"time"
)

// referenceSummary preserves the pre-optimization exact nearest-rank contract.
func referenceSummary(values []time.Duration) HistogramSummary {
	if len(values) == 0 {
		return HistogramSummary{}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	var sum time.Duration
	for _, v := range values {
		sum += v
	}
	rank := func(q float64) float64 { return values[int(math.Ceil(q*float64(len(values))))-1].Seconds() }
	return HistogramSummary{Count: uint64(len(values)), SumSeconds: sum.Seconds(),
		MinSeconds: values[0].Seconds(), MaxSeconds: values[len(values)-1].Seconds(),
		P50Seconds: rank(.5), P95Seconds: rank(.95), P99Seconds: rank(.99)}
}

func TestSummarizeDurationsMatchesExactReference(t *testing.T) {
	cases := map[string][]time.Duration{
		"empty": nil, "single": {7}, "pair": {2, 1},
		"signed-extremes": {time.Duration(math.MaxInt64), time.Duration(math.MinInt64), 0, -1, 1},
		"sum-overflow":    {time.Duration(math.MaxInt64), time.Duration(math.MaxInt64), 3},
	}
	rng := rand.New(rand.NewPCG(42, 17))
	for _, n := range []int{3, 19, 20, 21, 99, 100, 101, 199, 200, 201, 1024} {
		for _, shape := range []string{"random", "sorted", "reverse", "equal", "duplicates", "organ-pipe"} {
			values := make([]time.Duration, n)
			for i := range values {
				switch shape {
				case "equal":
					values[i] = 17
				case "duplicates":
					values[i] = time.Duration(rng.IntN(5) - 2)
				case "organ-pipe":
					values[i] = time.Duration(min(i, n-1-i))
				default:
					values[i] = time.Duration(rng.Uint64())
				}
			}
			if shape == "sorted" || shape == "reverse" {
				slices.Sort(values)
			}
			if shape == "reverse" {
				slices.Reverse(values)
			}
			cases[fmt.Sprintf("%s/%d", shape, n)] = values
		}
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			got := summarizeDurations(slices.Clone(values))
			want := referenceSummary(slices.Clone(values))
			if got != want {
				t.Fatalf("summary = %+v, want %+v", got, want)
			}
		})
	}
}
