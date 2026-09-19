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
