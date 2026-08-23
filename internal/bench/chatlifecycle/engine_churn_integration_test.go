//go:build integration

package chatlifecycle

import (
	"context"
	"errors"
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

func TestEngineFormalFirstLifecycleCohortRetainsExactReheatSenders(t *testing.T) {
	const workerCount = 3
	assignment := mustInitialLifecycleSlotAssignment(t)
	all := make([]LifecycleCandidate, 0, lifecycleCohortSize*workerCount)
	fixtures := make([]engineTestFixture, workerCount)
	readyAt := make([]time.Time, workerCount)
	var activeStart time.Time
	for workerID := uint64(0); workerID < workerCount; workerID++ {
		fixtures[workerID] = newEngineTestFixture(t, engineTestLimits{
			Formal: true, WorkerID: workerID, WorkerCount: workerCount,
			OnlineUsers: 10_000, BootstrapLoginsPerSecond: formalBootstrapLoginRate,
			NewUsersPerDay: 250_000, SendRatePerSecond: 2_000,
			WorkCapacity: 160_000, InflightCapacity: 8_192, MaxWorkPerAdvance: 160_000,
			StartingCapacity: 256,
		})
		fixture := &fixtures[workerID]
		fixture.factory.autoAck = true
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("worker %d Start: %v", workerID, err)
		}
		t.Cleanup(func() { _ = fixture.engine.Stop() })

		now := fixture.clock.Now()
		now, _ = fixture.bootstrapScheduledLogins(t, now)
		fixture.engine.finishBootstrapIfOnline(now)
		waitForEngineCompletions(t, fixture.engine, "formal bootstrap")
		readyAt[workerID] = now
		if activeStart.IsZero() || now.After(activeStart) {
			activeStart = now
		}
	}

	for workerID := uint64(0); workerID < workerCount; workerID++ {
		fixture := &fixtures[workerID]
		now := readyAt[workerID]
		for now.Before(activeStart) {
			now = now.Add(time.Second)
			fixture.clock.Set(now)
			step, err := fixture.engine.Step(context.Background(), now, nil)
			if err != nil {
				t.Fatalf("worker %d pre-clock Step: %v", workerID, err)
			}
			fixture.settleScheduledLogins(t, now, step)
		}
	}
	// The candidate seam needs enough real grants to complete every scheduled
	// initial relationship burst; it does not need to replay unrelated hot-set
	// volume at the full production rate.
	allocator, err := NewRateAllocator(500, 1_000, []int64{1, 1, 1})
	if err != nil {
		t.Fatalf("NewRateAllocator: %v", err)
	}
	now := activeStart
	for second := 0; second <= int(LifecycleProofCadence/time.Second); second++ {
		if second > 0 {
			now = now.Add(time.Second)
		}
		for workerID := uint64(0); workerID < workerCount; workerID++ {
			fixture := &fixtures[workerID]
			fixture.clock.Set(now)
			step, stepErr := fixture.engine.Step(context.Background(), now, nil)
			if stepErr != nil {
				t.Fatalf("worker %d Step(%d): %v", workerID, second, stepErr)
			}
			fixture.settleScheduledLogins(t, now, step)
		}
		rate, rateErr := allocator.Tick([]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
		if rateErr != nil {
			t.Fatalf("rate Tick(%d): %v", second, rateErr)
		}
		for workerID := uint64(0); workerID < workerCount; workerID++ {
			fixture := &fixtures[workerID]
			grant, grantErr := fixture.engine.ApplyGrant(context.Background(), now, rate.Released[workerID])
			var runtimeErr *RuntimeError
			if grantErr != nil && (!errors.As(grantErr, &runtimeErr) || runtimeErr.Code() != RuntimeFailureUnderDelivery) {
				t.Fatalf("worker %d grant second %d release %d = %+v, %v", workerID, second, rate.Released[workerID], grant, grantErr)
			}
			if !grant.Admitted {
				t.Fatalf("worker %d grant second %d was not admitted: %+v, %v", workerID, second, grant, grantErr)
			}
			waitForEngineCompletions(t, fixture.engine, "formal measured grant")
		}
	}
	loadedThrough := activeStart.Add(LifecycleProofCadence + 2*observerMaxRoundTimeout + productionLifecycleInitialLoadSchedulingReserve)
	for workerID := uint64(0); workerID < workerCount; workerID++ {
		fixture := &fixtures[workerID]
		candidates, leaseErr := fixture.engine.LeaseLifecycleCandidates(context.Background(), lifecycleCohortSize, assignment, loadedThrough)
		if leaseErr != nil {
			t.Fatalf("worker %d LeaseLifecycleCandidates: %v", workerID, leaseErr)
		}
		all = append(all, candidates...)
	}

	selected, err := SelectLifecycleCohort(all, loadedThrough, assignment, formalLogicalSlotGroups)
	if err != nil || len(selected) != lifecycleCohortSize {
		var perSlot [formalLogicalSlotGroups]int
		for _, candidate := range all {
			if candidate.SlotID > 0 && candidate.SlotID <= formalLogicalSlotGroups {
				perSlot[candidate.SlotID-1]++
			}
		}
		t.Fatalf("formal first cohort = %d/%v from %d candidates by slot %v", len(selected), err, len(all), perSlot)
	}
}
