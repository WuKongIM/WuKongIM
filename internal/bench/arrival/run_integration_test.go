//go:build integration

package arrival

import (
	"context"
	"testing"
	"time"
)

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
