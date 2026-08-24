//go:build integration

package chatlifecycle

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestEngineLocalThreeNodeGrantsSurviveFirstSessionChurn(t *testing.T) {
	for _, sendRate := range []int{1_000, 2_000} {
		sendRate := sendRate
		t.Run(fmt.Sprintf("qps-%d", sendRate), func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{
				WorkerID: 2, WorkerCount: 3, OnlineUsers: 2_500,
				BootstrapLoginsPerSecond: 200, NewUsersPerDay: 250_000, SendRatePerSecond: sendRate,
				HotSet: HotSetConfig{PersonChannels: 2_000, GroupChannels: 500},
				Groups: GroupCatalogConfig{
					Small: 400, Medium: 75, Large: 24, VeryLarge: 1,
					VeryLargeMembers: 100_000, FixedMembership: true, VeryLargeSendEvery: time.Minute,
				},
				WorkCapacity: 32_768, InflightCapacity: 4_096, MaxWorkPerAdvance: 32_768,
				StartingCapacity: 256,
			})
			fixture.factory.autoAck = true
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer fixture.engine.Stop()
			now := fixture.clock.Now()
			now, _ = fixture.bootstrapScheduledLogins(t, now)
			fixture.engine.finishBootstrapIfOnline(now)
			waitForEngineCompletions(t, fixture.engine, "bootstrap")

			allocator, err := NewRateAllocator(uint64(sendRate), uint64(2*sendRate), []int64{1, 1, 1})
			if err != nil {
				t.Fatalf("NewRateAllocator: %v", err)
			}
			for second := 0; second < 310; second++ {
				if second > 0 {
					now = now.Add(time.Second)
					fixture.clock.Set(now)
					if _, err := fixture.engine.Step(context.Background(), now, nil); err != nil {
						t.Fatalf("Step(%d): %v", second, err)
					}
				}
				rate, err := allocator.Tick([]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
				if err != nil {
					t.Fatalf("rate Tick(%d): %v", second, err)
				}
				grant, err := fixture.engine.ApplyGrant(context.Background(), now, rate.Released[2])
				if err != nil || !grant.Admitted || grant.Snapshot.Released != rate.Released[2] {
					t.Fatalf("grant second %d release %d = %+v, %v", second, rate.Released[2], grant, err)
				}
			}
		})
	}
}
