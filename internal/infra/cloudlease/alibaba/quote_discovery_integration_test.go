//go:build integration

package alibaba

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestQuoteSlowCatalogStillLeavesAdmissionTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reference := time.Date(2026, 11, 21, 13, 32, 24, 0, time.UTC)
		start := time.Now()
		now := func() time.Time { return reference.Add(time.Since(start)) }
		api := &slowAvailabilityReadAPI{fakeReadAPI: completeReadAPI()}
		controller := cloudlease.NewController(New(api, Options{Now: now}), now)
		plan := approvedPlan(reference)

		quote, err := controller.Quote(context.Background(), plan)
		if err != nil {
			t.Fatalf("Quote: %v", err)
		}
		if quote.Zone != "cn-hangzhou-h" || quote.Selection["instance_type"] != "ecs.g8.large" ||
			quote.EstimatedCostMicros != 46_400_000 ||
			quote.QuotedAt != reference || quote.ValidUntil != reference.Add(10*time.Minute) {
			t.Fatalf("quote changed the complete selection, cost, or original ten-minute validity")
		}
		// Model two minutes for transport, verification, and the hosted Acquire job.
		if _, err := cloudlease.ReleaseSelectorFromPlanQuote(plan, quote, now().Add(2*time.Minute)); err != nil {
			t.Fatalf("validity exhausted before admission at elapsed %v: %v", time.Since(start), err)
		}
		if got := len(api.availabilityRequests); got != 4 {
			t.Fatalf("checked %d placements, want all four", got)
		}
		if got := api.peak.Load(); got != 2 || api.active.Load() != 0 {
			t.Fatalf("availability concurrency peaked at %d with %d unfinished, want two and zero", got, api.active.Load())
		}
	})
}

// slowAvailabilityReadAPI models a slow provider read without wall-clock delay.
type slowAvailabilityReadAPI struct {
	*fakeReadAPI
	active atomic.Int32
	peak   atomic.Int32
}

func (f *slowAvailabilityReadAPI) Availability(ctx context.Context, request AvailabilityRequest) (Availability, error) {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for current := f.peak.Load(); active > current; current = f.peak.Load() {
		if f.peak.CompareAndSwap(current, active) {
			break
		}
	}
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	select {
	case <-timer.C:
		return f.fakeReadAPI.Availability(ctx, request)
	case <-ctx.Done():
		return Availability{}, ctx.Err()
	}
}
