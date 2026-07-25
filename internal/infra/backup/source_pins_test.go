package backup_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestClusterSourcePinManagerSelectsOnlyLargestOldestBudgetVictim(t *testing.T) {
	node := &fakeSourcePinNode{bytes: map[uint16]uint64{1: 20, 2: 40, 3: 40}}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(node, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	observations := make(map[uint16]backupruntime.SourcePinObservation)
	for _, hashSlot := range []uint16{1, 2, 3} {
		lease := testPinLease(hashSlot, now.Add(-time.Duration(hashSlot)*time.Minute).UnixMilli())
		frontier := backupcontract.SlotFrontier{
			HashSlot: hashSlot, Generation: lease.Generation, Lease: lease, SourceSlotID: lease.SlotID,
			SourcePinStartedAtUnixMillis: lease.AcquiredAtUnixMillis,
			Metadata:                     backupcontract.StreamFrontier{SourceCursor: "7", SourceHighWatermark: 7},
		}
		observations[hashSlot], err = manager.Observe(context.Background(), hashSlot, lease, frontier)
		if err != nil {
			t.Fatalf("Observe(%d) error = %v", hashSlot, err)
		}
	}
	if observations[3].NodePinnedBytes != 100 || !observations[3].NodeBudgetVictim {
		t.Fatalf("latest observation = %#v, want oldest among largest Slots selected", observations[3])
	}
	lease := testPinLease(1, now.Add(-time.Minute).UnixMilli())
	frontier := backupcontract.SlotFrontier{
		HashSlot: 1, Generation: lease.Generation, Lease: lease, SourceSlotID: lease.SlotID,
		SourcePinStartedAtUnixMillis: lease.AcquiredAtUnixMillis,
		Metadata:                     backupcontract.StreamFrontier{SourceCursor: "7", SourceHighWatermark: 7},
	}
	small, err := manager.Observe(context.Background(), 1, lease, frontier)
	if err != nil || small.NodeBudgetVictim {
		t.Fatalf("small Slot selected after full accounting: %#v, %v", small, err)
	}
}

func TestClusterSourcePinManagerFencesStaleRelease(t *testing.T) {
	node := &fakeSourcePinNode{bytes: map[uint16]uint64{1: 20}}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(node, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	oldLease := testPinLease(1, now.Add(-time.Minute).UnixMilli())
	frontier := backupcontract.SlotFrontier{
		HashSlot: 1, Generation: oldLease.Generation, Lease: oldLease, SourceSlotID: oldLease.SlotID,
		SourcePinStartedAtUnixMillis: oldLease.AcquiredAtUnixMillis,
	}
	if _, err := manager.Observe(context.Background(), 1, oldLease, frontier); err != nil {
		t.Fatalf("Observe(old) error = %v", err)
	}
	newLease := oldLease
	newLease.LeaderTerm++
	newLease.HolderNodeID++
	newLease.Sequence++
	newLease.AcquiredAtUnixMillis = now.UnixMilli()
	frontier.Lease = newLease
	replaced, err := manager.Observe(context.Background(), 1, newLease, frontier)
	if err != nil {
		t.Fatalf("Observe(new) error = %v", err)
	}
	if replaced.Age != time.Minute {
		t.Fatalf("takeover pin age = %s, want preserved 1m", replaced.Age)
	}
	if _, err := manager.Release(context.Background(), 1, oldLease); err != backupruntime.ErrCaptureLeaseFenced {
		t.Fatalf("Release(stale) error = %v", err)
	}
	if node.releases != 0 {
		t.Fatalf("stale release reached node %d times", node.releases)
	}
}

func TestClusterSourcePinManagerReleasesRecordedSlotBeforePhysicalRemap(t *testing.T) {
	node := &fakeSourcePinNode{bytes: map[uint16]uint64{1: 20}}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(node, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	oldLease := testPinLease(1, now.Add(-time.Minute).UnixMilli())
	oldFrontier := backupcontract.SlotFrontier{
		HashSlot: 1, Generation: oldLease.Generation, Lease: oldLease, SourceSlotID: oldLease.SlotID,
		SourcePinStartedAtUnixMillis: oldLease.AcquiredAtUnixMillis,
	}
	if _, err := manager.Observe(context.Background(), 1, oldLease, oldFrontier); err != nil {
		t.Fatalf("Observe(old) error = %v", err)
	}
	node.slotIDs = map[uint16]uint32{1: 10}
	newLease := oldLease
	newLease.SlotID = 10
	newLease.LeaderTerm++
	newLease.ConfigEpoch++
	newLease.Sequence++
	newLease.AcquiredAtUnixMillis = now.UnixMilli()
	newFrontier := backupcontract.SlotFrontier{
		HashSlot: 1, Generation: newLease.Generation, Lease: newLease, SourceSlotID: newLease.SlotID,
		SourcePinStartedAtUnixMillis: oldLease.AcquiredAtUnixMillis,
	}
	if _, err := manager.Observe(context.Background(), 1, newLease, newFrontier); err != nil {
		t.Fatalf("Observe(new) error = %v", err)
	}
	if node.releases != 1 || len(node.releaseSlotIDs) != 1 ||
		node.releaseSlotIDs[0] != oldLease.SlotID {
		t.Fatalf("remap releases = %v, want old physical Slot %d", node.releaseSlotIDs, oldLease.SlotID)
	}
}

func TestClusterSourcePinManagerReleaseUsesRecordedPhysicalSlotAndUpdatesAggregate(t *testing.T) {
	node := &fakeSourcePinNode{bytes: map[uint16]uint64{1: 20, 2: 40}}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(node, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	for _, hashSlot := range []uint16{1, 2} {
		lease := testPinLease(hashSlot, now.Add(-time.Minute).UnixMilli())
		frontier := backupcontract.SlotFrontier{
			HashSlot: hashSlot, Generation: lease.Generation, Lease: lease, SourceSlotID: lease.SlotID,
			SourcePinStartedAtUnixMillis: lease.AcquiredAtUnixMillis,
		}
		if _, err := manager.Observe(context.Background(), hashSlot, lease, frontier); err != nil {
			t.Fatalf("Observe(%d) error = %v", hashSlot, err)
		}
	}
	lease := testPinLease(1, now.Add(-time.Minute).UnixMilli())
	released, err := manager.Release(context.Background(), 1, lease)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if released.NodePinnedBytes != 40 || node.releases != 1 ||
		len(node.releaseSlotIDs) != 1 || node.releaseSlotIDs[0] != lease.SlotID {
		t.Fatalf("release=%#v node=%#v", released, node)
	}
}

func TestClusterSourcePinManagerDeduplicatesSharedPhysicalSlotAndReleasesOldestFloor(t *testing.T) {
	node := &fakeSourcePinNode{
		bytes:   map[uint16]uint64{1: 100, 2: 60},
		slotIDs: map[uint16]uint32{1: 10, 2: 10},
	}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(node, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	var firstLease backupcontract.SlotCaptureLease
	for _, hashSlot := range []uint16{1, 2} {
		lease := testPinLease(hashSlot, now.Add(-time.Duration(3-hashSlot)*time.Minute).UnixMilli())
		lease.SlotID = 10
		if hashSlot == 1 {
			firstLease = lease
		}
		frontier := backupcontract.SlotFrontier{
			HashSlot: hashSlot, Generation: lease.Generation, Lease: lease, SourceSlotID: 10,
			SourcePinStartedAtUnixMillis: lease.AcquiredAtUnixMillis,
			Metadata: backupcontract.StreamFrontier{
				SourceCursor: fmt.Sprintf("%d", hashSlot*10),
			},
		}
		observation, observeErr := manager.Observe(context.Background(), hashSlot, lease, frontier)
		if observeErr != nil {
			t.Fatalf("Observe(%d) error = %v", hashSlot, observeErr)
		}
		if hashSlot == 2 && (observation.NodePinnedBytes != 100 || observation.NodeBudgetVictim) {
			t.Fatalf("shared physical accounting = %#v, want 100 bytes and older floor victim", observation)
		}
	}
	released, err := manager.Release(context.Background(), 1, firstLease)
	if err != nil {
		t.Fatalf("Release(oldest floor) error = %v", err)
	}
	if released.NodePinnedBytes != 60 {
		t.Fatalf("remaining physical bytes = %d, want 60", released.NodePinnedBytes)
	}
}

func TestClusterSourcePinManagerRefreshesSharedPhysicalFloorBytes(t *testing.T) {
	calls := make(map[uint64]int)
	node := &fakeSourcePinNode{
		slotIDs: map[uint16]uint32{1: 10, 2: 10},
		holdFn: func(hashSlot uint16, afterIndex uint64) uint64 {
			calls[afterIndex]++
			switch {
			case hashSlot == 1 && afterIndex == 10 && calls[afterIndex] == 1:
				return 90
			case hashSlot == 1 && afterIndex == 10:
				return 990
			case hashSlot == 2 && afterIndex == 20:
				return 980
			default:
				return 0
			}
		},
	}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(node, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	for _, item := range []struct {
		hashSlot uint16
		cursor   string
	}{
		{hashSlot: 1, cursor: "10"},
		{hashSlot: 2, cursor: "20"},
	} {
		lease := testPinLease(item.hashSlot, now.Add(-time.Minute).UnixMilli())
		lease.SlotID = 10
		frontier := backupcontract.SlotFrontier{
			HashSlot: item.hashSlot, Generation: lease.Generation, Lease: lease,
			SourceSlotID: lease.SlotID, SourcePinStartedAtUnixMillis: lease.AcquiredAtUnixMillis,
			Metadata: backupcontract.StreamFrontier{SourceCursor: item.cursor},
		}
		observation, observeErr := manager.Observe(
			context.Background(), item.hashSlot, lease, frontier,
		)
		if observeErr != nil {
			t.Fatalf("Observe(%d) error = %v", item.hashSlot, observeErr)
		}
		if item.hashSlot == 2 &&
			(observation.NodePinnedBytes != 990 || observation.NodeBudgetVictim) {
			t.Fatalf("refreshed shared floor observation = %#v, want 990-byte floor owned by Slot 1", observation)
		}
	}
	if calls[10] != 2 {
		t.Fatalf("oldest physical floor measured %d times, want refresh after peer observation", calls[10])
	}
}

func TestClusterSourcePinManagerRemeasuresNewFloorAfterMemberRelease(t *testing.T) {
	hashTwoCalls := 0
	node := &fakeSourcePinNode{
		slotIDs: map[uint16]uint32{1: 10, 2: 10},
		holdFn: func(hashSlot uint16, _ uint64) uint64 {
			if hashSlot == 1 {
				return 990
			}
			hashTwoCalls++
			if hashTwoCalls == 1 {
				return 80
			}
			return 980
		},
	}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(
		node, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	leases := make(map[uint16]backupcontract.SlotCaptureLease)
	for _, item := range []struct {
		hashSlot uint16
		cursor   string
	}{
		{hashSlot: 1, cursor: "10"},
		{hashSlot: 2, cursor: "20"},
	} {
		lease := testPinLease(item.hashSlot, now.Add(-time.Minute).UnixMilli())
		lease.SlotID = 10
		leases[item.hashSlot] = lease
		frontier := backupcontract.SlotFrontier{
			HashSlot: item.hashSlot, Generation: lease.Generation,
			Lease: lease, SourceSlotID: 10,
			SourcePinStartedAtUnixMillis: lease.AcquiredAtUnixMillis,
			Metadata: backupcontract.StreamFrontier{
				SourceCursor: item.cursor,
			},
		}
		if _, err := manager.Observe(
			context.Background(), item.hashSlot, lease, frontier,
		); err != nil {
			t.Fatalf("Observe(%d) error = %v", item.hashSlot, err)
		}
	}
	released, err := manager.Release(context.Background(), 1, leases[1])
	if err != nil {
		t.Fatalf("Release(old floor) error = %v", err)
	}
	if released.NodePinnedBytes != 980 || hashTwoCalls != 2 {
		t.Fatalf("released=%#v hash-two calls=%d, want remeasured 980-byte floor", released, hashTwoCalls)
	}
}

func TestClusterSourcePinManagerCleansRefreshPinAfterPhysicalRemap(t *testing.T) {
	hashOneHolds := 0
	node := &fakeSourcePinNode{
		slotIDs: map[uint16]uint32{1: 10, 2: 10},
		holdSlotFn: func(hashSlot uint16, _ uint64) uint32 {
			if hashSlot != 1 {
				return 10
			}
			hashOneHolds++
			if hashOneHolds == 1 {
				return 10
			}
			return 11
		},
	}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(
		node, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	for _, item := range []struct {
		hashSlot uint16
		cursor   string
	}{
		{hashSlot: 1, cursor: "10"},
		{hashSlot: 2, cursor: "20"},
	} {
		lease := testPinLease(item.hashSlot, now.Add(-time.Minute).UnixMilli())
		lease.SlotID = 10
		frontier := backupcontract.SlotFrontier{
			HashSlot: item.hashSlot, Generation: lease.Generation,
			Lease: lease, SourceSlotID: 10,
			SourcePinStartedAtUnixMillis: lease.AcquiredAtUnixMillis,
			Metadata: backupcontract.StreamFrontier{
				SourceCursor: item.cursor,
			},
		}
		_, observeErr := manager.Observe(
			context.Background(), item.hashSlot, lease, frontier,
		)
		if item.hashSlot == 1 && observeErr != nil {
			t.Fatalf("Observe(first floor) error = %v", observeErr)
		}
		if item.hashSlot == 2 &&
			observeErr != backupruntime.ErrCaptureLeaseFenced {
			t.Fatalf("Observe(remapped refresh) error = %v, want fenced", observeErr)
		}
	}
	if node.releases != 1 || len(node.releaseSlotIDs) != 1 ||
		node.releaseSlotIDs[0] != 11 {
		t.Fatalf(
			"refresh remap releases = %v, want exact newly pinned physical Slot 11",
			node.releaseSlotIDs,
		)
	}
}

func TestClusterSourcePinManagerVictimAgeSurvivesLeaseAdoption(t *testing.T) {
	node := &fakeSourcePinNode{bytes: map[uint16]uint64{1: 40, 2: 40}}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(
		node, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	oldLease := testPinLease(1, now.Add(-10*time.Minute).UnixMilli())
	oldFrontier := backupcontract.SlotFrontier{
		HashSlot: 1, Generation: oldLease.Generation, Lease: oldLease,
		SourceSlotID:                 oldLease.SlotID,
		SourcePinStartedAtUnixMillis: oldLease.AcquiredAtUnixMillis,
	}
	if _, err := manager.Observe(context.Background(), 1, oldLease, oldFrontier); err != nil {
		t.Fatalf("Observe(old victim) error = %v", err)
	}
	secondLease := testPinLease(2, now.Add(-5*time.Minute).UnixMilli())
	secondFrontier := backupcontract.SlotFrontier{
		HashSlot: 2, Generation: secondLease.Generation, Lease: secondLease,
		SourceSlotID:                 secondLease.SlotID,
		SourcePinStartedAtUnixMillis: secondLease.AcquiredAtUnixMillis,
	}
	if _, err := manager.Observe(context.Background(), 2, secondLease, secondFrontier); err != nil {
		t.Fatalf("Observe(second) error = %v", err)
	}
	takeover := oldLease
	takeover.LeaderTerm++
	takeover.Sequence++
	takeover.AcquiredAtUnixMillis = now.UnixMilli()
	if _, err := manager.AdoptLease(context.Background(), 1, takeover); err != nil {
		t.Fatalf("AdoptLease() error = %v", err)
	}
	observation, err := manager.Observe(
		context.Background(), 2, secondLease, secondFrontier,
	)
	if err != nil {
		t.Fatalf("Observe(second after takeover) error = %v", err)
	}
	if observation.NodeBudgetVictim {
		t.Fatalf("newer floor selected after older floor takeover: %#v", observation)
	}
}

func TestClusterSourcePinManagerSerializesSameSlotReleaseAndReplacement(t *testing.T) {
	node := &blockingSourcePinNode{
		releaseStarted:         make(chan struct{}),
		releaseContinue:        make(chan struct{}),
		replacementHoldStarted: make(chan struct{}),
	}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(node, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	oldLease := testPinLease(1, now.Add(-time.Minute).UnixMilli())
	oldFrontier := backupcontract.SlotFrontier{
		HashSlot: 1, Generation: oldLease.Generation, Lease: oldLease, SourceSlotID: oldLease.SlotID,
		SourcePinStartedAtUnixMillis: oldLease.AcquiredAtUnixMillis,
	}
	if _, err := manager.Observe(context.Background(), 1, oldLease, oldFrontier); err != nil {
		t.Fatalf("Observe(old) error = %v", err)
	}

	releaseDone := make(chan error, 1)
	go func() {
		_, releaseErr := manager.Release(context.Background(), 1, oldLease)
		releaseDone <- releaseErr
	}()
	<-node.releaseStarted

	newLease := oldLease
	newLease.LeaderTerm++
	newLease.Sequence++
	newLease.AcquiredAtUnixMillis = now.UnixMilli()
	newFrontier := backupcontract.SlotFrontier{
		HashSlot: 1, Generation: newLease.Generation, Lease: newLease, SourceSlotID: newLease.SlotID,
		SourcePinStartedAtUnixMillis: oldLease.AcquiredAtUnixMillis,
	}
	observeDone := make(chan error, 1)
	go func() {
		_, observeErr := manager.Observe(context.Background(), 1, newLease, newFrontier)
		observeDone <- observeErr
	}()
	select {
	case <-node.replacementHoldStarted:
		t.Fatal("replacement hold started before old physical release completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(node.releaseContinue)
	if err := <-releaseDone; err != nil {
		t.Fatalf("Release(old) error = %v", err)
	}
	if err := <-observeDone; err != nil {
		t.Fatalf("Observe(new) error = %v", err)
	}
}

func TestClusterSourcePinManagerAdoptsTakeoverLeaseWithoutPinGap(t *testing.T) {
	node := &fakeSourcePinNode{bytes: map[uint16]uint64{1: 20}}
	now := time.UnixMilli(1_753_400_300_000)
	manager, err := backupinfra.NewClusterSourcePinManager(node, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewClusterSourcePinManager() error = %v", err)
	}
	oldLease := testPinLease(1, now.Add(-time.Minute).UnixMilli())
	frontier := backupcontract.SlotFrontier{
		HashSlot: 1, Generation: oldLease.Generation, Lease: oldLease, SourceSlotID: oldLease.SlotID,
		SourcePinStartedAtUnixMillis: oldLease.AcquiredAtUnixMillis,
	}
	if _, err := manager.Observe(context.Background(), 1, oldLease, frontier); err != nil {
		t.Fatalf("Observe(old) error = %v", err)
	}
	newLease := oldLease
	newLease.LeaderTerm++
	newLease.HolderNodeID++
	newLease.Sequence++
	newLease.AcquiredAtUnixMillis = now.UnixMilli()
	adopted, err := manager.AdoptLease(context.Background(), 1, newLease)
	if err != nil {
		t.Fatalf("AdoptLease() error = %v", err)
	}
	if node.releases != 0 || adopted.PinnedBytes != 20 {
		t.Fatalf("adopted=%#v releases=%d, want existing physical floor", adopted, node.releases)
	}
	if _, err := manager.Release(context.Background(), 1, newLease); err != nil {
		t.Fatalf("Release(adopted) error = %v", err)
	}
	if node.releases != 1 {
		t.Fatalf("release count = %d, want 1", node.releases)
	}
}

func testPinLease(hashSlot uint16, acquiredAt int64) backupcontract.SlotCaptureLease {
	return backupcontract.SlotCaptureLease{
		SlotID: uint32(hashSlot) + 1, LeaderTerm: 7, ConfigEpoch: 3,
		HolderNodeID: 1, Generation: "slot-generation-1",
		Sequence: 1, AcquiredAtUnixMillis: acquiredAt,
	}
}

type fakeSourcePinNode struct {
	bytes          map[uint16]uint64
	slotIDs        map[uint16]uint32
	holdFn         func(uint16, uint64) uint64
	holdSlotFn     func(uint16, uint64) uint32
	releases       int
	releaseSlotIDs []uint32
}

type blockingSourcePinNode struct {
	mu                     sync.Mutex
	holds                  int
	releaseStarted         chan struct{}
	releaseContinue        chan struct{}
	replacementHoldStarted chan struct{}
}

func (n *blockingSourcePinNode) HoldBackupSourcePin(_ context.Context, hashSlot uint16, _ uint64) (clusterpkg.BackupSourcePinObservation, error) {
	n.mu.Lock()
	n.holds++
	holds := n.holds
	n.mu.Unlock()
	if holds == 2 {
		close(n.replacementHoldStarted)
	}
	return clusterpkg.BackupSourcePinObservation{
		HashSlot: hashSlot, SlotID: uint32(hashSlot) + 1, PinnedBytes: 20,
	}, nil
}

func (n *blockingSourcePinNode) ReleaseBackupSourcePin(_ context.Context, _ uint16, _ uint32) error {
	close(n.releaseStarted)
	<-n.releaseContinue
	return nil
}

func (n *fakeSourcePinNode) HoldBackupSourcePin(_ context.Context, hashSlot uint16, afterIndex uint64) (clusterpkg.BackupSourcePinObservation, error) {
	slotID := uint32(hashSlot) + 1
	if n.slotIDs != nil && n.slotIDs[hashSlot] != 0 {
		slotID = n.slotIDs[hashSlot]
	}
	if n.holdSlotFn != nil {
		slotID = n.holdSlotFn(hashSlot, afterIndex)
	}
	pinnedBytes := n.bytes[hashSlot]
	if n.holdFn != nil {
		pinnedBytes = n.holdFn(hashSlot, afterIndex)
	}
	return clusterpkg.BackupSourcePinObservation{
		HashSlot: hashSlot, SlotID: slotID, PinnedBytes: pinnedBytes,
	}, nil
}

func (n *fakeSourcePinNode) ReleaseBackupSourcePin(_ context.Context, _ uint16, slotID uint32) error {
	n.releases++
	n.releaseSlotIDs = append(n.releaseSlotIDs, slotID)
	return nil
}
