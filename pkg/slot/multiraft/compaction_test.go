package multiraft

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRuntimeLogCompactionSnapshotsAndCompactsAppliedEntries(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1,
		CheckInterval:  time.Nanosecond,
	})
	store := &internalFakeStorage{}
	fsm := &snapshottingStateMachine{}
	slotID := SlotID(190)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: fsm},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	var last Result
	for i := 1; i <= 3; i++ {
		fut, err := rt.Propose(context.Background(), slotID, proposalString(fmt.Sprintf("set-%d", i)))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", i, err)
		}
		last = waitForFutureResult(t, fut)
	}

	waitForCondition(t, func() bool {
		snap, err := store.Snapshot(context.Background())
		return err == nil && snap.Metadata.Index >= last.Index
	})

	snap, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snap.Metadata.Index != last.Index {
		t.Fatalf("Snapshot().Index = %d, want %d", snap.Metadata.Index, last.Index)
	}
	wantData := fmt.Sprintf("idx=%d data=set-3", last.Index)
	rawData, configIndex, err := decodeSlotSnapshotData(snap.Data)
	if err != nil {
		t.Fatalf("decode Snapshot().Data error = %v", err)
	}
	if string(rawData) != wantData {
		t.Fatalf("Snapshot().Data raw payload = %q, want final applied command", rawData)
	}
	if configIndex != 1 {
		t.Fatalf("Snapshot().Data config index = %d, want bootstrap config entry index 1", configIndex)
	}
	first, err := store.FirstIndex(context.Background())
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	if first != snap.Metadata.Index+1 {
		t.Fatalf("FirstIndex() = %d, want %d", first, snap.Metadata.Index+1)
	}
}

func TestRuntimeLogCompactionCanBeDisabled(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        false,
		EnabledSet:     true,
		TriggerEntries: 1,
		CheckInterval:  time.Nanosecond,
	})
	store := &internalFakeStorage{}
	slotID := SlotID(191)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: &snapshottingStateMachine{}},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	for i := 1; i <= 3; i++ {
		fut, err := rt.Propose(context.Background(), slotID, proposalString(fmt.Sprintf("disabled-%d", i)))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", i, err)
		}
		waitForFutureResult(t, fut)
	}

	snap, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !raft.IsEmptySnap(snap) {
		t.Fatalf("Snapshot() = %+v, want empty when compaction disabled", snap.Metadata)
	}
}

func TestRuntimeLogCompactionFailureDoesNotFailSlot(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1,
		CheckInterval:  time.Nanosecond,
	})
	store := &internalFakeStorage{}
	sentinel := errors.New("snapshot failed once")
	fsm := &snapshottingStateMachine{snapshotErr: sentinel}
	slotID := SlotID(192)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: fsm},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	first, err := rt.Propose(context.Background(), slotID, proposalString("after-failed-compaction"))
	if err != nil {
		t.Fatalf("first Propose() error = %v", err)
	}
	waitForFutureResult(t, first)

	second, err := rt.Propose(context.Background(), slotID, proposalString("after-retry"))
	if err != nil {
		t.Fatalf("second Propose() error = %v", err)
	}
	res := waitForFutureResult(t, second)
	waitForCondition(t, func() bool {
		snap, err := store.Snapshot(context.Background())
		return err == nil && snap.Metadata.Index >= res.Index
	})
}

func TestRuntimeManualLogCompactionForcesSnapshotBelowAutomaticThreshold(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1000,
		CheckInterval:  time.Hour,
	})
	store := &internalFakeStorage{}
	slotID := SlotID(194)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: &snapshottingStateMachine{}},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("manual"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	applied := waitForFutureResult(t, fut).Index
	firstBefore, err := store.FirstIndex(context.Background())
	if err != nil {
		t.Fatalf("FirstIndex() before error = %v", err)
	}

	result, err := rt.CompactLog(context.Background(), slotID)
	if err != nil {
		t.Fatalf("CompactLog() error = %v", err)
	}

	if !result.Compacted {
		t.Fatalf("CompactLog().Compacted = false, skipped=%q", result.SkippedReason)
	}
	if result.NodeID != 1 || result.SlotID != slotID {
		t.Fatalf("CompactLog() node/slot = %d/%d, want 1/%d", result.NodeID, result.SlotID, slotID)
	}
	if result.AppliedIndex != applied || result.AfterSnapshotIndex != applied {
		t.Fatalf("CompactLog() indexes = applied:%d after:%d, want %d", result.AppliedIndex, result.AfterSnapshotIndex, applied)
	}
	if result.BeforeSnapshotIndex != 0 {
		t.Fatalf("BeforeSnapshotIndex = %d, want 0", result.BeforeSnapshotIndex)
	}
	firstAfter, err := store.FirstIndex(context.Background())
	if err != nil {
		t.Fatalf("FirstIndex() after error = %v", err)
	}
	if firstAfter <= firstBefore {
		t.Fatalf("FirstIndex after manual compaction = %d, want > %d", firstAfter, firstBefore)
	}
}

func TestRuntimeBackupPinRetainsOnlyPinnedSlotLogUntilRelease(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled: true, EnabledSet: true, TriggerEntries: 1000, CheckInterval: time.Hour,
	})
	pinnedStore := &internalFakeStorage{}
	healthyStore := &internalFakeStorage{}
	for _, item := range []struct {
		slot  SlotID
		store *internalFakeStorage
	}{
		{slot: 195, store: pinnedStore},
		{slot: 196, store: healthyStore},
	} {
		if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
			Slot:   SlotOptions{ID: item.slot, Storage: item.store, StateMachine: &snapshottingStateMachine{}},
			Voters: []NodeID{1},
		}); err != nil {
			t.Fatalf("BootstrapSlot(%d) error = %v", item.slot, err)
		}
		waitForSingleNodeLeader(t, rt, item.slot)
		future, err := rt.Propose(context.Background(), item.slot, proposalString("pin"))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", item.slot, err)
		}
		waitForFutureResult(t, future)
	}
	if err := rt.SetLogCompactionPin(context.Background(), 195, "backup-slot-17", 0, true); err != nil {
		t.Fatalf("SetLogCompactionPin(hold) error = %v", err)
	}
	pinned, err := rt.CompactLog(context.Background(), 195)
	if err != nil || !pinned.Compacted || pinned.AfterSnapshotIndex != pinned.AppliedIndex {
		t.Fatalf("pinned CompactLog() = %#v, %v", pinned, err)
	}
	pinnedFirst, err := pinnedStore.FirstIndex(context.Background())
	if err != nil || pinnedFirst != 1 {
		t.Fatalf("pinned FirstIndex() = %d, %v, want retained index 1", pinnedFirst, err)
	}
	healthy, err := rt.CompactLog(context.Background(), 196)
	if err != nil || !healthy.Compacted {
		t.Fatalf("healthy CompactLog() = %#v, %v", healthy, err)
	}
	if err := rt.SetLogCompactionPin(context.Background(), 195, "backup-slot-17", 0, false); err != nil {
		t.Fatalf("SetLogCompactionPin(release) error = %v", err)
	}
	releasedFirst, err := pinnedStore.FirstIndex(context.Background())
	if err != nil || releasedFirst != pinned.AppliedIndex+1 {
		t.Fatalf("released FirstIndex() = %d, %v, want %d", releasedFirst, err, pinned.AppliedIndex+1)
	}
}

func TestRuntimeSharedPhysicalSlotCompactsThroughMinimumBackupFloor(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled: true, EnabledSet: true, TriggerEntries: 1000, CheckInterval: time.Hour,
	})
	const slotID SlotID = 198
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID: slotID, Storage: &internalFakeStorage{}, StateMachine: &snapshottingStateMachine{},
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)
	for index := 0; index < 8; index++ {
		future, err := rt.Propose(context.Background(), slotID, proposalString("shared-floor"))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", index, err)
		}
		waitForFutureResult(t, future)
	}
	if err := rt.SetLogCompactionPin(context.Background(), slotID, "hash-slot-1", 3, true); err != nil {
		t.Fatalf("SetLogCompactionPin(first) error = %v", err)
	}
	if err := rt.SetLogCompactionPin(context.Background(), slotID, "hash-slot-2", 6, true); err != nil {
		t.Fatalf("SetLogCompactionPin(second) error = %v", err)
	}
	first, err := rt.CompactLog(context.Background(), slotID)
	if err != nil || !first.Compacted || first.AfterSnapshotIndex != first.AppliedIndex {
		t.Fatalf("first CompactLog() = %#v, %v, want current recovery snapshot", first, err)
	}
	store := rt.slots[slotID].storage
	firstRetained, err := store.FirstIndex(context.Background())
	if err != nil || firstRetained != 4 {
		t.Fatalf("first retained index = %d, %v, want 4", firstRetained, err)
	}
	if err := rt.SetLogCompactionPin(context.Background(), slotID, "hash-slot-1", 0, false); err != nil {
		t.Fatalf("release first floor: %v", err)
	}
	secondRetained, err := store.FirstIndex(context.Background())
	if err != nil || secondRetained != 7 {
		t.Fatalf("second retained index = %d, %v, want 7", secondRetained, err)
	}
	if err := rt.SetLogCompactionPin(context.Background(), slotID, "hash-slot-2", 0, false); err != nil {
		t.Fatalf("release second floor: %v", err)
	}
	finalRetained, err := store.FirstIndex(context.Background())
	if err != nil || finalRetained != first.AppliedIndex+1 {
		t.Fatalf("final retained index = %d, %v, want %d", finalRetained, err, first.AppliedIndex+1)
	}
}

func TestRuntimePinnedArchiveIsNotReplayedAfterCurrentSnapshotRestore(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled: true, EnabledSet: true, TriggerEntries: 1000, CheckInterval: time.Hour,
	})
	store := &countingEntriesStorage{internalFakeStorage: &internalFakeStorage{}}
	const slotID SlotID = 199
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID: slotID, Storage: store, StateMachine: &snapshottingStateMachine{},
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)
	for index := 0; index < 4; index++ {
		future, err := rt.Propose(context.Background(), slotID, proposalString("non-idempotent"))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", index, err)
		}
		waitForFutureResult(t, future)
	}
	if err := rt.SetLogCompactionPin(context.Background(), slotID, "hash-slot-1", 1, true); err != nil {
		t.Fatalf("SetLogCompactionPin() error = %v", err)
	}
	result, err := rt.CompactLog(context.Background(), slotID)
	if err != nil || !result.Compacted {
		t.Fatalf("CompactLog() = %#v, %v", result, err)
	}
	if first, _ := store.FirstIndex(context.Background()); first != 2 {
		t.Fatalf("retained FirstIndex() = %d, want 2", first)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	store.entriesMu.Lock()
	store.ranges = nil
	store.entriesMu.Unlock()

	reopened := newCompactionRuntime(t, LogCompactionConfig{Enabled: false, EnabledSet: true})
	fsm := &snapshottingStateMachine{}
	if err := reopened.OpenSlot(context.Background(), SlotOptions{
		ID: slotID, Storage: store, StateMachine: fsm,
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		fsm.mu.Lock()
		defer fsm.mu.Unlock()
		return fsm.restoreCount == 1
	})
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if len(fsm.commands) != 0 {
		t.Fatalf("replayed %d retained pre-snapshot commands", len(fsm.commands))
	}
	ranges := store.entriesRanges()
	if len(ranges) > 0 && ranges[0].lo <= store.snapshot.Metadata.Index {
		t.Fatalf("recovery loaded retained archive range %#v through snapshot %d", ranges[0], store.snapshot.Metadata.Index)
	}
}

func TestRuntimeCompactionAndLowerPinInstallationAreLinearized(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled: true, EnabledSet: true, TriggerEntries: 1000, CheckInterval: time.Hour,
	})
	fsm := newBlockingSnapshotStateMachine()
	const slotID SlotID = 201
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID: slotID, Storage: &internalFakeStorage{}, StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)
	future, err := rt.Propose(context.Background(), slotID, proposalString("compaction-pin-race"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	result := waitForFutureResult(t, future)
	if err := rt.SetLogCompactionPin(
		context.Background(), slotID, "existing-high-floor", result.Index, true,
	); err != nil {
		t.Fatalf("SetLogCompactionPin(high floor) error = %v", err)
	}
	compactDone := make(chan error, 1)
	go func() {
		_, compactErr := rt.CompactLog(context.Background(), slotID)
		compactDone <- compactErr
	}()
	<-fsm.snapshotStarted

	pinDone := make(chan error, 1)
	go func() {
		pinDone <- rt.SetLogCompactionPin(
			context.Background(), slotID, "new-lower-floor", 0, true,
		)
	}()
	select {
	case err := <-pinDone:
		t.Fatalf("lower pin completed before destructive compaction: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(fsm.snapshotContinue)
	if err := <-compactDone; err != nil {
		t.Fatalf("CompactLog() error = %v", err)
	}
	if err := <-pinDone; err != nil {
		t.Fatalf("SetLogCompactionPin(lower floor) error = %v", err)
	}
}

func TestRuntimeCancelledWaitingPinCannotMutateLater(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled: false, EnabledSet: true,
	})
	store := &blockingRetainedLogStorage{
		internalFakeStorage: &internalFakeStorage{},
		started:             make(chan struct{}),
		continued:           make(chan struct{}),
	}
	const slotID SlotID = 200
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: &snapshottingStateMachine{}},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)
	future, err := rt.Propose(context.Background(), slotID, proposalString("pin-cancel"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	waitForFutureResult(t, future)
	if err := rt.SetLogCompactionPin(
		context.Background(), slotID, "active-pin", 0, true,
	); err != nil {
		t.Fatalf("SetLogCompactionPin(initial) error = %v", err)
	}
	advanceDone := make(chan error, 1)
	go func() {
		advanceDone <- rt.SetLogCompactionPin(
			context.Background(), slotID, "active-pin", 1, true,
		)
	}()
	<-store.started
	g := slotFor(rt, slotID)
	if err := g.waitApplyIdle(context.Background()); err != nil {
		t.Fatalf("waitApplyIdle() error = %v", err)
	}
	g.compactor.cfg = LogCompactionConfig{
		Enabled: true, EnabledSet: true, TriggerEntries: 1, CheckInterval: time.Hour,
	}
	sendFuture, err := rt.Propose(context.Background(), slotID, proposalString("trim-does-not-block-send"))
	if err != nil {
		t.Fatalf("Propose(during trim) error = %v", err)
	}
	waitForFutureResult(t, sendFuture)
	nextFuture, err := rt.Propose(context.Background(), slotID, proposalString("trim-still-does-not-block-send"))
	if err != nil {
		t.Fatalf("Propose(second during trim) error = %v", err)
	}
	waitForFutureResult(t, nextFuture)
	if g.compactor.lastSnapshotIdx != 0 {
		t.Fatalf(
			"skipped automatic compaction recorded snapshot %d, want 0",
			g.compactor.lastSnapshotIdx,
		)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pinDone := make(chan error, 1)
	go func() {
		pinDone <- rt.SetLogCompactionPin(ctx, slotID, "cancelled-pin", 0, true)
	}()
	cancel()
	if err := <-pinDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("SetLogCompactionPin() error = %v, want context canceled", err)
	}
	close(store.continued)
	if err := <-advanceDone; err != nil {
		t.Fatalf("SetLogCompactionPin(advance) error = %v", err)
	}
	g.compactor.pinMu.RLock()
	_, held := g.compactor.pins["cancelled-pin"]
	g.compactor.pinMu.RUnlock()
	if held {
		t.Fatal("cancelled pin mutated after its operation wait was cancelled")
	}
}

func TestRuntimePinMutationSucceedsWhenBestEffortTrimFails(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled: true, EnabledSet: true, TriggerEntries: 1000, CheckInterval: time.Hour,
	})
	injected := errors.New("injected archive trim failure")
	store := &failingRetainedLogStorage{
		internalFakeStorage: &internalFakeStorage{},
		err:                 injected,
		failures:            1,
	}
	slotID := SlotID(199)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: &snapshottingStateMachine{}},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)
	for index := 0; index < 3; index++ {
		future, err := rt.Propose(
			context.Background(), slotID, proposalString(fmt.Sprintf("pin-%d", index)),
		)
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", index, err)
		}
		_ = waitForFutureResult(t, future)
	}
	if err := rt.SetLogCompactionPin(
		context.Background(), slotID, "backup-slot-1", 1, true,
	); err != nil {
		t.Fatalf("SetLogCompactionPin(initial hold) error = %v", err)
	}
	if err := rt.SetLogCompactionPin(
		context.Background(), slotID, "backup-slot-1", 1, true,
	); err != nil {
		t.Fatalf("SetLogCompactionPin(unchanged hold) error = %v", err)
	}
	if calls := store.callCount(); calls != 0 {
		t.Fatalf("initial/unchanged hold ran %d archive trims, want 0", calls)
	}
	compacted, err := rt.CompactLog(context.Background(), slotID)
	if err != nil || !compacted.Compacted {
		t.Fatalf("CompactLog() = %#v, %v", compacted, err)
	}
	g := slotFor(rt, slotID)
	if err := rt.SetLogCompactionPin(
		context.Background(), slotID, "backup-slot-1", 2, true,
	); err != nil {
		t.Fatalf("SetLogCompactionPin(applied advance) error = %v", err)
	}
	waitForCondition(t, func() bool {
		first, firstErr := store.FirstIndex(context.Background())
		return firstErr == nil && first == 3 &&
			store.callCount() >= 2 && !g.compactor.trimDirty()
	})
	store.setFailures(1)
	if err := rt.SetLogCompactionPin(
		context.Background(), slotID, "backup-slot-1", 0, false,
	); err != nil {
		t.Fatalf("SetLogCompactionPin(applied release) error = %v", err)
	}
	waitForCondition(t, func() bool {
		first, firstErr := store.FirstIndex(context.Background())
		return firstErr == nil &&
			first == compacted.AppliedIndex+1 &&
			store.callCount() >= 4 &&
			!g.compactor.trimDirty()
	})
	g.compactor.pinMu.RLock()
	_, held := g.compactor.pins["backup-slot-1"]
	dirty := g.compactor.archiveTrimDirty
	g.compactor.pinMu.RUnlock()
	if held || dirty {
		t.Fatalf("retried release left held=%t dirty=%t, want both false", held, dirty)
	}
}

func TestRuntimeCloseSlotWaitsForActivePinOperation(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled: false, EnabledSet: true,
	})
	store := &blockingRetainedLogStorage{
		internalFakeStorage: &internalFakeStorage{},
		started:             make(chan struct{}),
		continued:           make(chan struct{}),
	}
	const slotID SlotID = 202
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID: slotID, Storage: store, StateMachine: &snapshottingStateMachine{},
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)
	future, err := rt.Propose(context.Background(), slotID, proposalString("close-pin"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	waitForFutureResult(t, future)
	if err := rt.SetLogCompactionPin(
		context.Background(), slotID, "active-pin", 0, true,
	); err != nil {
		t.Fatalf("SetLogCompactionPin(initial) error = %v", err)
	}
	pinDone := make(chan error, 1)
	go func() {
		pinDone <- rt.SetLogCompactionPin(
			context.Background(), slotID, "active-pin", 1, true,
		)
	}()
	<-store.started
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.CloseSlot(context.Background(), slotID)
	}()
	select {
	case err := <-closeDone:
		t.Fatalf("CloseSlot() returned before active pin operation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(store.continued)
	if err := <-pinDone; err != nil {
		t.Fatalf("SetLogCompactionPin(advance) error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("CloseSlot() error = %v", err)
	}
}

func TestRuntimeManualLogCompactionWaitsForAsyncApply(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1000,
		CheckInterval:  time.Hour,
	})
	slotID := SlotID(197)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("manual-barrier"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	done := make(chan error, 1)
	go func() {
		_, err := rt.CompactLog(context.Background(), slotID)
		done <- err
	}()
	waitForSlotWorkerWaitingOnApply(t, rt, slotID)
	select {
	case err := <-done:
		t.Fatalf("CompactLog() returned before async apply finished: %v", err)
	default:
	}

	fsm.unblock()
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CompactLog() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CompactLog() did not return after async apply unblocked")
	}
}

func TestRuntimeManualLogCompactionCancellationDoesNotPinSlotWorker(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1000,
		CheckInterval:  time.Hour,
	})
	slotID := SlotID(198)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("manual-cancel"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = rt.CompactLog(ctx, slotID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CompactLog() error = %v, want %v", err, context.DeadlineExceeded)
	}

	beforeTicks := slotTickCount(rt, slotID)
	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	g.markTickPending()
	rt.scheduler.enqueue(slotID)
	waitForCondition(t, func() bool {
		return slotTickCount(rt, slotID) > beforeTicks
	})

	fsm.unblock()
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestRuntimeManualCompactionReadsSnapshotMetadataWithoutPayload(t *testing.T) {
	sentinel := errors.New("snapshot payload unavailable")
	store := &slotSnapshotMetadataOnlyStorage{
		firstIndex:  11,
		lastIndex:   12,
		snapshotErr: sentinel,
		terms:       map[uint64]uint64{10: 7},
	}
	g := &slot{
		id:      SlotID(196),
		storage: store,
		status:  Status{NodeID: 1},
		compactor: newLogCompactor(LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1000,
			CheckInterval:  time.Hour,
		}, 10),
	}

	result, err := g.compactLogManually(context.Background(), 10)
	if err != nil {
		t.Fatalf("compactLogManually() error = %v", err)
	}
	if result.Compacted {
		t.Fatalf("compactLogManually().Compacted = true, want false")
	}
	if result.SkippedReason != LogCompactionSkippedUpToDate {
		t.Fatalf("compactLogManually().SkippedReason = %q, want %q", result.SkippedReason, LogCompactionSkippedUpToDate)
	}
	if result.BeforeSnapshotIndex != 10 || result.AfterSnapshotIndex != 10 {
		t.Fatalf("compactLogManually() snapshot indexes = before:%d after:%d, want 10", result.BeforeSnapshotIndex, result.AfterSnapshotIndex)
	}
	if store.snapshotCalls != 0 {
		t.Fatalf("Snapshot() calls = %d, want 0", store.snapshotCalls)
	}
	if store.firstIndexCalls == 0 || store.termCalls != 0 {
		t.Fatalf("metadata calls = FirstIndex:%d Term:%d, want boundary without payload or term reads", store.firstIndexCalls, store.termCalls)
	}
}

func TestRuntimeCompactedLeaderSendsSnapshotToNewLearner(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: time.Millisecond,
		Seed:     19,
	})
	slotID := SlotID(195)
	voters := []NodeID{1, 2}
	cluster.bootstrapSlot(t, slotID, voters)
	cluster.waitForLeaderAmong(t, slotID, voters)

	leaderID := cluster.waitForLeaderAmong(t, slotID, voters)
	var last Result
	for i := 0; i < 3; i++ {
		fut, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalString(fmt.Sprintf("before-learner-%d", i)))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", i, err)
		}
		last = waitForFutureResult(t, fut)
	}
	cluster.waitForNodeCommitIndex(t, leaderID, slotID, last.Index)

	compacted, err := cluster.runtime(leaderID).CompactLog(context.Background(), slotID)
	if err != nil {
		t.Fatalf("CompactLog() error = %v", err)
	}
	if !compacted.Compacted {
		t.Fatalf("CompactLog().Compacted = false, skipped=%q", compacted.SkippedReason)
	}

	learnerStore := &internalFakeStorage{}
	learnerFSM := &internalFakeStateMachine{}
	cluster.stores[3][slotID] = learnerStore
	cluster.fsms[3][slotID] = learnerFSM
	if err := cluster.runtime(3).OpenSlot(context.Background(), SlotOptions{
		ID:           slotID,
		Storage:      learnerStore,
		StateMachine: learnerFSM,
	}); err != nil {
		t.Fatalf("OpenSlot(learner) error = %v", err)
	}

	change, err := cluster.runtime(leaderID).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 3,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	changeResult := waitForFutureResult(t, change)

	var learnerStatus Status
	var learnerStatusErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cluster.requireHealthyNetwork(t)
		learnerStatus, learnerStatusErr = cluster.runtime(3).Status(slotID)
		if learnerStatusErr == nil && learnerStatus.AppliedIndex >= changeResult.Index {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if learnerStatusErr != nil || learnerStatus.AppliedIndex < changeResult.Index {
		t.Fatalf("learner status = %+v err=%v, want applied >= %d", learnerStatus, learnerStatusErr, changeResult.Index)
	}
	learnerSnap, err := learnerStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("learner Snapshot() error = %v", err)
	}
	if learnerSnap.Metadata.Index < compacted.AfterSnapshotIndex {
		t.Fatalf("learner snapshot index = %d, want >= compacted index %d", learnerSnap.Metadata.Index, compacted.AfterSnapshotIndex)
	}
	learnerFSM.mu.Lock()
	restoreCount := learnerFSM.restoreCount
	learnerFSM.mu.Unlock()
	if restoreCount == 0 {
		t.Fatal("learner Restore() count = 0, want snapshot restore")
	}
}

func TestRuntimeSnapshotCatchUpCarriesConfigAppliedIndexToNewLearner(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: time.Millisecond,
		Seed:     23,
	})
	slotID := SlotID(201)
	voters := []NodeID{1, 2}
	for _, nodeID := range voters {
		store := &internalFakeStorage{}
		fsm := &snapshottingStateMachine{}
		cluster.stores[nodeID][slotID] = store
		if err := cluster.runtime(nodeID).BootstrapSlot(context.Background(), BootstrapSlotRequest{
			Slot: SlotOptions{
				ID:           slotID,
				Storage:      store,
				StateMachine: fsm,
			},
			Voters: voters,
		}); err != nil {
			t.Fatalf("BootstrapSlot(node=%d) error = %v", nodeID, err)
		}
	}
	leaderID := cluster.waitForLeaderAmong(t, slotID, voters)

	learnerStore := &internalFakeStorage{}
	learnerFSM := &snapshottingStateMachine{}
	cluster.stores[3][slotID] = learnerStore
	if err := cluster.runtime(3).OpenSlot(context.Background(), SlotOptions{
		ID:           slotID,
		Storage:      learnerStore,
		StateMachine: learnerFSM,
	}); err != nil {
		t.Fatalf("OpenSlot(learner) error = %v", err)
	}
	cluster.partitionNode(3)

	change, err := cluster.runtime(leaderID).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 3,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	changeResult := waitForFutureResult(t, change)

	var normalResult Result
	for i := 0; i < 3; i++ {
		payload := fmt.Sprintf("after-config-%d", i)
		fut, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalString(payload))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", i, err)
		}
		normalResult = waitForFutureResult(t, fut)
	}
	if normalResult.Index <= changeResult.Index {
		t.Fatalf("normal proposal index = %d, want > config index %d", normalResult.Index, changeResult.Index)
	}

	compacted, err := cluster.runtime(leaderID).CompactLog(context.Background(), slotID)
	if err != nil {
		t.Fatalf("CompactLog() error = %v", err)
	}
	if !compacted.Compacted {
		t.Fatalf("CompactLog().Compacted = false, skipped=%q", compacted.SkippedReason)
	}
	if compacted.AfterSnapshotIndex <= changeResult.Index {
		t.Fatalf("snapshot index = %d, want > config entry index %d", compacted.AfterSnapshotIndex, changeResult.Index)
	}

	cluster.healNode(3)
	cluster.waitForCondition(t, func() bool {
		st, err := cluster.runtime(3).Status(slotID)
		if err != nil || st.AppliedIndex < compacted.AfterSnapshotIndex {
			return false
		}
		learnerFSM.mu.Lock()
		defer learnerFSM.mu.Unlock()
		return learnerFSM.restoreCount > 0
	})

	learnerStatus, err := cluster.runtime(3).Status(slotID)
	if err != nil {
		t.Fatalf("Status(learner) error = %v", err)
	}
	if learnerStatus.ConfigAppliedIndex != changeResult.Index {
		t.Fatalf("learner Status().ConfigAppliedIndex = %d, want config entry index %d", learnerStatus.ConfigAppliedIndex, changeResult.Index)
	}

	learnerFSM.mu.Lock()
	restores := append([]Snapshot(nil), learnerFSM.restores...)
	learnerFSM.mu.Unlock()
	if len(restores) == 0 {
		t.Fatal("learner Restore() snapshots = 0, want snapshot restore")
	}
	wantData := fmt.Sprintf("idx=%d data=after-config-2", normalResult.Index)
	if got := string(restores[len(restores)-1].Data); got != wantData {
		t.Fatalf("learner Restore().Data = %q, want raw snapshot data %q", got, wantData)
	}
}

func TestSlotSnapshotDataEnvelopeRoundTripAndLegacyRaw(t *testing.T) {
	raw, configIndex, err := decodeSlotSnapshotData([]byte("legacy-raw"))
	if err != nil {
		t.Fatalf("decode legacy raw error = %v", err)
	}
	if string(raw) != "legacy-raw" || configIndex != 0 {
		t.Fatalf("decode legacy raw = data:%q config:%d, want raw payload and config 0", raw, configIndex)
	}

	encoded := encodeSlotSnapshotData([]byte("payload"), 7)
	raw, configIndex, err = decodeSlotSnapshotData(encoded)
	if err != nil {
		t.Fatalf("decode envelope error = %v", err)
	}
	if string(raw) != "payload" || configIndex != 7 {
		t.Fatalf("decode envelope = data:%q config:%d, want payload/config 7", raw, configIndex)
	}
}

func TestSlotSnapshotDataEnvelopeRejectsMalformedMagicMatch(t *testing.T) {
	if _, _, err := decodeSlotSnapshotData(append([]byte(nil), slotSnapshotDataMagic...)); err == nil {
		t.Fatal("decode truncated envelope error = nil")
	}
	data := make([]byte, slotSnapshotDataHeaderSize)
	copy(data, slotSnapshotDataMagic)
	data[len(slotSnapshotDataMagic)] = slotSnapshotDataVersion + 1
	if _, _, err := decodeSlotSnapshotData(data); err == nil {
		t.Fatal("decode unsupported envelope version error = nil")
	}
}

func TestOpenSlotRestoresSnapshotThenReplaysPostSnapshotEntries(t *testing.T) {
	store := &internalFakeStorage{}
	ctx := context.Background()
	snap := raftpb.Snapshot{
		Data: []byte("snap"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 2,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1},
			},
		},
	}
	hs := raftpb.HardState{Term: 1, Commit: 4}
	if err := store.Save(ctx, PersistentState{
		HardState: &hs,
		Snapshot:  &snap,
		Entries: []raftpb.Entry{
			{Index: 3, Term: 1, Type: raftpb.EntryNormal, Data: proposalString("post-snap-3")},
			{Index: 4, Term: 1, Type: raftpb.EntryNormal, Data: proposalString("post-snap-4")},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(ctx, 4); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	rt := newCompactionRuntime(t, LogCompactionConfig{Enabled: false, EnabledSet: true})
	fsm := &snapshottingStateMachine{}
	if err := rt.OpenSlot(ctx, SlotOptions{ID: 193, Storage: store, StateMachine: fsm}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		fsm.mu.Lock()
		defer fsm.mu.Unlock()
		return fsm.restoreCount == 1 && len(fsm.commands) == 2
	})
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if got := fsm.commands[0].Data; string(got) != "post-snap-3" {
		t.Fatalf("first replayed command = %q, want post-snap-3", got)
	}
	if got := fsm.commands[1].Data; string(got) != "post-snap-4" {
		t.Fatalf("second replayed command = %q, want post-snap-4", got)
	}
}

func newCompactionRuntime(t *testing.T, compaction LogCompactionConfig) *Runtime {
	t.Helper()
	rt, err := New(Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    &internalFakeTransport{},
		Raft: RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
			LogCompaction: compaction,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return rt
}

func waitForSingleNodeLeader(t *testing.T, rt *Runtime, slotID SlotID) {
	t.Helper()
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})
}

func waitForSlotWorkerWaitingOnApply(t *testing.T, rt *Runtime, slotID SlotID) {
	t.Helper()
	waitForCondition(t, func() bool {
		g := slotFor(rt, slotID)
		if g == nil {
			return false
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.processing && g.applying > 0
	})
}

type snapshottingStateMachine struct {
	mu           sync.Mutex
	commands     []Command
	restoreCount int
	restores     []Snapshot
	snapshotErr  error
}

type blockingSnapshotStateMachine struct {
	*snapshottingStateMachine
	snapshotStarted  chan struct{}
	snapshotContinue chan struct{}
	once             sync.Once
}

func newBlockingSnapshotStateMachine() *blockingSnapshotStateMachine {
	return &blockingSnapshotStateMachine{
		snapshottingStateMachine: &snapshottingStateMachine{},
		snapshotStarted:          make(chan struct{}),
		snapshotContinue:         make(chan struct{}),
	}
}

func (s *blockingSnapshotStateMachine) Snapshot(ctx context.Context) (Snapshot, error) {
	s.once.Do(func() { close(s.snapshotStarted) })
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-s.snapshotContinue:
		return s.snapshottingStateMachine.Snapshot(ctx)
	}
}

func (s *snapshottingStateMachine) Apply(_ context.Context, cmd Command) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, Command{
		SlotID:   cmd.SlotID,
		HashSlot: cmd.HashSlot,
		Index:    cmd.Index,
		Term:     cmd.Term,
		Data:     append([]byte(nil), cmd.Data...),
	})
	return append([]byte("ok:"), cmd.Data...), nil
}

func (s *snapshottingStateMachine) Restore(_ context.Context, snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restoreCount++
	s.restores = append(s.restores, Snapshot{
		Index: snap.Index,
		Term:  snap.Term,
		Data:  append([]byte(nil), snap.Data...),
	})
	return nil
}

func (s *snapshottingStateMachine) Snapshot(context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshotErr != nil {
		err := s.snapshotErr
		s.snapshotErr = nil
		return Snapshot{}, err
	}
	if len(s.commands) == 0 {
		return Snapshot{Data: []byte("empty")}, nil
	}
	last := s.commands[len(s.commands)-1]
	return Snapshot{Data: []byte(fmt.Sprintf("idx=%d data=%s", last.Index, last.Data))}, nil
}

type slotSnapshotMetadataOnlyStorage struct {
	state BootstrapState

	firstIndex uint64
	lastIndex  uint64
	terms      map[uint64]uint64

	snapshotErr     error
	firstIndexCalls int
	termCalls       int
	snapshotCalls   int
}

type failingRetainedLogStorage struct {
	*internalFakeStorage
	mu       sync.Mutex
	err      error
	calls    int
	failures int
}

type blockingRetainedLogStorage struct {
	*internalFakeStorage
	started   chan struct{}
	continued chan struct{}
	once      sync.Once
}

func (s *blockingRetainedLogStorage) TrimRetainedLog(
	ctx context.Context,
	through uint64,
) error {
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.continued:
		return s.internalFakeStorage.TrimRetainedLog(ctx, through)
	}
}

func (s *failingRetainedLogStorage) TrimRetainedLog(
	ctx context.Context,
	through uint64,
) error {
	s.mu.Lock()
	s.calls++
	if s.failures > 0 {
		s.failures--
		err := s.err
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return s.internalFakeStorage.TrimRetainedLog(ctx, through)
}

func (s *failingRetainedLogStorage) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *failingRetainedLogStorage) setFailures(failures int) {
	s.mu.Lock()
	s.failures = failures
	s.mu.Unlock()
}

func (s *slotSnapshotMetadataOnlyStorage) InitialState(context.Context) (BootstrapState, error) {
	return s.state, nil
}

func (s *slotSnapshotMetadataOnlyStorage) Entries(context.Context, uint64, uint64, uint64) ([]raftpb.Entry, error) {
	return nil, nil
}

func (s *slotSnapshotMetadataOnlyStorage) Term(_ context.Context, index uint64) (uint64, error) {
	s.termCalls++
	return s.terms[index], nil
}

func (s *slotSnapshotMetadataOnlyStorage) FirstIndex(context.Context) (uint64, error) {
	s.firstIndexCalls++
	return s.firstIndex, nil
}

func (s *slotSnapshotMetadataOnlyStorage) LastIndex(context.Context) (uint64, error) {
	return s.lastIndex, nil
}

func (s *slotSnapshotMetadataOnlyStorage) Snapshot(context.Context) (raftpb.Snapshot, error) {
	s.snapshotCalls++
	return raftpb.Snapshot{}, s.snapshotErr
}

func (s *slotSnapshotMetadataOnlyStorage) Save(context.Context, PersistentState) error {
	return nil
}

func (s *slotSnapshotMetadataOnlyStorage) MarkApplied(context.Context, uint64) error {
	return nil
}
