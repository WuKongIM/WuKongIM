package proxy

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestPermissionMetadataSlotWorkersAreBounded(t *testing.T) {
	const groups = permissionBatchSlotWorkers * 2
	entered := make(chan struct{}, groups)
	release := make(chan struct{})
	done := make(chan struct{})
	var active atomic.Int64
	var peak atomic.Int64
	go func() {
		runPermissionMetadataSlotWorkers(groups, func(int) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				observed := peak.Load()
				if current <= observed || peak.CompareAndSwap(observed, current) {
					break
				}
			}
			entered <- struct{}{}
			<-release
		})
		close(done)
	}()

	for i := 0; i < permissionBatchSlotWorkers; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("workers entered = %d, want %d", i, permissionBatchSlotWorkers)
		}
	}
	select {
	case <-entered:
		t.Fatalf("workers exceeded bound %d", permissionBatchSlotWorkers)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers did not finish after release")
	}
	if got := peak.Load(); got != permissionBatchSlotWorkers {
		t.Fatalf("peak workers = %d, want %d", got, permissionBatchSlotWorkers)
	}
}

func TestPermissionMetadataSlotWorkersCoverReviewedTwelveSlotTopologyInOneWave(t *testing.T) {
	const representedSlots = 12
	entered := make(chan struct{}, representedSlots)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runPermissionMetadataSlotWorkers(representedSlots, func(int) {
			entered <- struct{}{}
			<-release
		})
		close(done)
	}()

	for slot := 0; slot < representedSlots; slot++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			<-done
			t.Fatalf("permission Slot reads entered = %d, want %d in the first wave", slot, representedSlots)
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("permission Slot workers did not finish after release")
	}
}
