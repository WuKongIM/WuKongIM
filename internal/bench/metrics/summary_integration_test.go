//go:build integration

package metrics

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

var summaryBenchmarkSink HistogramSummary

// BenchmarkExactSummary compares complete summaries, including the owned copy.
// Ordered/equal inputs also expose how much copying and summing cost when sort
// has little work. The legacy implementation stays here as a measured control.
func BenchmarkExactSummary(b *testing.B) {
	for _, n := range []int{30000, 810000} {
		for _, shape := range []string{"random", "sorted", "reverse", "equal", "duplicates", "organ-pipe"} {
			values := make([]time.Duration, n)
			rng := rand.New(rand.NewPCG(42, 17))
			for i := range values {
				switch shape {
				case "equal":
					values[i] = 17
				case "duplicates":
					values[i] = time.Duration(rng.IntN(16))
				case "organ-pipe":
					values[i] = time.Duration(min(i, n-1-i))
				default:
					values[i] = time.Duration(rng.Int64N(int64(time.Second)))
				}
			}
			if shape == "sorted" || shape == "reverse" {
				slices.Sort(values)
			}
			if shape == "reverse" {
				slices.Reverse(values)
			}
			for _, algorithm := range []struct {
				name      string
				summarize func([]time.Duration) HistogramSummary
			}{
				{"legacy", referenceSummary}, {"typed", summarizeDurations},
			} {
				b.Run(fmt.Sprintf("Samples%d/%s/%s", n, shape, algorithm.name), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						summaryBenchmarkSink = algorithm.summarize(slices.Clone(values))
					}
				})
			}
		}
	}
}
