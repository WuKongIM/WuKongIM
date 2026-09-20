//go:build integration

package arrival

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunObservedPreservesSealedSamplesAndArrivalClock(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[int]Sample)
	origins := make(map[int]time.Time)
	r := RunObserved(20, 1000, 2, func(context.Context, int) error {
		return context.DeadlineExceeded
	}, func(index int, planned time.Time, sample Sample) {
		mu.Lock()
		defer mu.Unlock()
		if _, duplicate := seen[index]; duplicate {
			t.Error("duplicate observation")
		}
		seen[index], origins[index] = sample, planned.Add(-time.Duration(index)*time.Second/1000)
	})
	if len(seen) != len(r.Samples) {
		t.Fatal("missing observation")
	}
	for index, sample := range r.Samples {
		if seen[index] != sample || !origins[index].Equal(origins[0]) {
			t.Fatalf("mutated timing/sample at %d", index)
		}
	}
	if len(r.Windows(time.Second)[0].Failures(400)) == 0 {
		t.Fatal("observation erased failures")
	}
}

func TestRunAccountsForSaturationWithoutSlowingArrivals(t *testing.T) {
	result := Run(100, 1000, 1, func(ctx context.Context, _ int) error {
		select {
		case <-time.After(20 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	window := result.Windows(time.Second)[0]
	if window.QueueP99MS < 500 || len(window.Failures(400)) == 0 {
		t.Fatalf("hidden queueing: %+v", window)
	}
}

func TestRunBoundsQueueAndRetainsDrops(t *testing.T) {
	result := Run(600, 1000, 1, func(ctx context.Context, _ int) error {
		select {
		case <-time.After(10 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	window := result.Windows(time.Second)[0]
	if window.Dropped == 0 || len(window.Failures(400)) == 0 {
		t.Fatalf("lost overload: %+v", window)
	}
}

// A slow service can maintain the offered rate with enough concurrency. It
// must still fail the latency gate instead of being mistaken for healthy load.
func TestRunRejectsServiceRegressionAtFull500QPS(t *testing.T) {
	for attempt := 0; attempt < 3; attempt++ {
		result := Run(250, 500, 256, func(ctx context.Context, _ int) error {
			select {
			case <-time.After(450 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		window := result.Windows(time.Second)[0]
		if window.Errors != 0 || window.Dropped != 0 || window.Completed != 250 || window.StartedPerSecond < 475 {
			t.Fatalf("fixture failed to maintain offered load: %+v", window)
		}
		found := false
		for _, reason := range window.Failures(400) {
			if reason == "scheduled_latency" {
				found = true
			}
		}
		if !found {
			t.Fatalf("accepted service regression: %+v", window)
		}
	}
}

func TestRunRetainsClosedFailureCategories(t *testing.T) {
	causes := []error{context.DeadlineExceeded, context.Canceled, errors.New("private operation details")}
	result := Run(3, 1000, 1, func(_ context.Context, index int) error { return causes[index] })
	for i, want := range []string{"deadline", "canceled", "operation"} {
		if result.Samples[i].FailureKind != want || !result.Samples[i].Failed {
			t.Fatalf("sample %d: %+v", i, result.Samples[i])
		}
	}
}

func TestFailedWarmupPreservesEvidenceWithoutMeasurementPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "arrival.json")
	t.Setenv("WK_BENCH_ARRIVAL_REPORT", path)
	r := Result{Rate: 500, Samples: []Sample{{Started: true, Completed: true, Failed: true, FailureKind: "deadline"}}}
	if ReportWarmup(t, r) {
		t.Fatal("failed warmup passed")
	}
	data, err := os.ReadFile(strings.TrimSuffix(path, ".json") + ".warmup.json")
	if err != nil || !strings.Contains(string(data), `"qualification": false`) || !strings.Contains(string(data), `"kind": "deadline"`) {
		t.Fatalf("missing non-qualifying failure evidence: %s %v", data, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("warmup created measurement evidence: %v", err)
	}
}
