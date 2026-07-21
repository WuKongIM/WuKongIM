package conversationactive

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestDirtyActiveAtIndexRemainsBoundedAcrossHotUpdates(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Options{})
	if err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "cold", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000},
		{Kind: metadb.ConversationKindNormal, UID: "hot", ChannelID: "room", ChannelType: 2, ActiveAtMS: 2_000},
	}); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}

	const updates = 4_096
	for i := 0; i < updates; i++ {
		if err := m.MarkActive(ctx, []ActivePatch{{
			Kind:        metadb.ConversationKindNormal,
			UID:         "hot",
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  int64(2_001 + i),
		}}); err != nil {
			t.Fatalf("MarkActive(update=%d) error = %v", i, err)
		}
	}

	const finalHotActiveAt = int64(2_000 + updates)
	if got := m.dirtyAge.Len(); got != 2 {
		t.Fatalf("dirty active-at buckets = %d, want exactly cold and final hot buckets after %d updates", got, updates)
	}
	if got := m.dirtyAge.Oldest(); got != 1_000 {
		t.Fatalf("oldest dirty active-at = %d, want cold bucket 1000", got)
	}
	position, ok := m.dirtyAge.positions[finalHotActiveAt]
	if !ok || m.dirtyAge.heap[position].count != 1 {
		t.Fatalf("final hot bucket %d = position %d present=%v heap=%+v, want one live row", finalHotActiveAt, position, ok, m.dirtyAge.heap)
	}
	entries := m.dirtyFlushEntries(1)
	if len(entries) != 1 || entries[0].uid != "cold" {
		t.Fatalf("first fair dirty entry = %+v, want cold row", entries)
	}
	if cleared := m.clearFlushedDirty(entries); cleared.cleared != 1 {
		t.Fatalf("clear cold row = %+v, want one clear", cleared)
	}
	if got := m.dirtyAge.Oldest(); got != finalHotActiveAt {
		t.Fatalf("oldest dirty active-at after cold clear = %d, want final hot bucket %d", got, finalHotActiveAt)
	}
	requireCacheIndexConservation(t, m)
}

func TestMarkActiveCoalescesDuplicateAddressesBeforeMutation(t *testing.T) {
	ctx := context.Background()
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{Observer: observer, MaxCachedRows: 4})
	initial := make([]ActivePatch, 0, 9)
	for index := 0; index < cap(initial); index++ {
		initial = append(initial, ActivePatch{
			Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2,
			ActiveAtMS: 1_000 + int64(index*25), ReadSeq: uint64(9 - index), MessageSeq: uint64(index + 1),
		})
	}
	if err := m.MarkActive(ctx, initial); err != nil {
		t.Fatalf("MarkActive(initial duplicates) error = %v", err)
	}
	if got := observer.lastMutation(t); got.BecameDirty != 1 || got.DirtyUpdated != 0 || got.Unchanged != 0 {
		t.Fatalf("initial mutation = %+v, want one unique row transition", got)
	}
	if m.nextVersion != 1 {
		t.Fatalf("initial version allocator=%d, want one version for one unique row", m.nextVersion)
	}
	entry, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "room", 2)
	if !ok || entry.ActiveAtMS != 1_200 || entry.ReadSeq != 9 || entry.MessageSeq != 9 {
		t.Fatalf("coalesced initial entry=%+v present=%v, want max active/read values", entry, ok)
	}

	updates := []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_300, ReadSeq: 9, MessageSeq: 10},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_250, ReadSeq: 10, MessageSeq: 12},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_400, ReadSeq: 8, MessageSeq: 11},
	}
	if err := m.MarkActive(ctx, updates); err != nil {
		t.Fatalf("MarkActive(update duplicates) error = %v", err)
	}
	if got := observer.lastMutation(t); got.BecameDirty != 0 || got.DirtyUpdated != 1 || got.Unchanged != 0 {
		t.Fatalf("update mutation = %+v, want one unique dirty update", got)
	}
	if m.nextVersion != 2 {
		t.Fatalf("updated version allocator=%d, want one new version for one unique row", m.nextVersion)
	}
	entry, ok = m.EntryForTest(metadb.ConversationKindNormal, "u1", "room", 2)
	if !ok || entry.ActiveAtMS != 1_400 || entry.ReadSeq != 10 || entry.MessageSeq != 12 {
		t.Fatalf("coalesced updated entry=%+v present=%v, want max active/read values", entry, ok)
	}
	requireCacheIndexConservation(t, m)
}

func TestBoundedFlushSelectionVisitsEveryDirtyRowBeforeRepeating(t *testing.T) {
	const rows = 64
	ctx := context.Background()
	store := &conflictingSelectionStore{seen: make(map[string]int)}
	m := NewManager(Options{Store: store})
	store.manager = m

	patches := make([]ActivePatch, 0, rows)
	for i := 0; i < rows; i++ {
		patches = append(patches, ActivePatch{
			Kind:        metadb.ConversationKindNormal,
			UID:         fmt.Sprintf("u-%02d", i),
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  int64(1_000 + i),
		})
	}
	if err := m.MarkActive(ctx, patches); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}

	for attempt := 0; attempt < rows; attempt++ {
		result, err := m.Flush(ctx, 1)
		if err != nil {
			t.Fatalf("Flush(attempt=%d) error = %v", attempt, err)
		}
		if result.Selected != 1 || result.Persisted != 1 || result.VersionConflicts != 1 || result.Requeued != 1 {
			t.Fatalf("Flush(attempt=%d) result = %+v, want one persisted version conflict retained for retry", attempt, result)
		}
	}

	if got := len(store.seen); got != rows {
		t.Fatalf("unique dirty rows selected in first %d attempts = %d, want %d before any row repeats; selections=%v", rows, got, rows, store.seen)
	}
	requireDirtyIndexConservation(t, m)
}

func TestCleanReceiverCooldownDoesNotInheritHistoricalSenderReadSeq(t *testing.T) {
	ctx := context.Background()
	const (
		initialActiveAt  int64 = 1_000
		receiverActiveAt int64 = 2_000
	)
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u1",
		ChannelID:   "room",
		ChannelType: 2,
		ActiveAtMS:  initialActiveAt,
		ReadSeq:     9,
	}}); err != nil {
		t.Fatalf("MarkActive(sender) error = %v", err)
	}
	if result, err := m.Flush(ctx, 1); err != nil || result.Persisted != 1 || result.Cleared != 1 {
		t.Fatalf("Flush(sender) = %+v, %v, want persisted sender row", result, err)
	}
	store.primary = map[metadb.ConversationStateKey]metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2}: {
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2,
			ActiveAt: initialActiveAt, ReadSeq: 9,
		},
	}

	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u1",
		ChannelID:   "room",
		ChannelType: 2,
		ActiveAtMS:  receiverActiveAt,
	}}); err != nil {
		t.Fatalf("MarkActive(receiver) error = %v", err)
	}
	result, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(receiver) error = %v", err)
	}
	if result.Selected != 0 {
		t.Fatalf("Flush(receiver) = %+v, want clean receiver update suppressed inside cooldown", result)
	}
	if got := len(store.touches); got != 1 {
		t.Fatalf("durable touch batches = %d, want only initial sender flush", got)
	}
	entry, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "room", 2)
	if !ok || entry.ActiveAtMS != receiverActiveAt || entry.ReadSeq != 9 {
		t.Fatalf("cache entry = %+v present=%v, want latest receiver view with historical sender read floor", entry, ok)
	}
	address := cacheAddress{uid: "u1", key: conversationKey{kind: metadb.ConversationKindNormal, channelID: "room", channelType: 2}}
	if baseline := m.cache[address.uid][address.key].durableActiveAtMS; baseline != initialActiveAt {
		t.Fatalf("durable baseline = %d, want %d", baseline, initialActiveAt)
	}
	requireDirtyIndexConservation(t, m)
}

func TestPersistedSenderConflictRebasesConcurrentReceiverForCooldown(t *testing.T) {
	ctx := context.Background()
	const (
		initialActiveAt  int64  = 1_000
		receiverActiveAt int64  = 2_000
		readSeq          uint64 = 9
	)
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2,
		ActiveAtMS: initialActiveAt, ReadSeq: readSeq,
	}}); err != nil {
		t.Fatalf("MarkActive(sender) error = %v", err)
	}
	store.touchHook = func() {
		store.touchHook = nil
		if err := m.MarkActive(ctx, []ActivePatch{{
			Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2,
			ActiveAtMS: receiverActiveAt,
		}}); err != nil {
			t.Fatalf("MarkActive(concurrent receiver) error = %v", err)
		}
	}
	first, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(sender snapshot) error = %v", err)
	}
	if first.Persisted != 1 || first.Cleared != 0 || first.VersionConflicts != 1 || first.Requeued != 1 {
		t.Fatalf("Flush(sender snapshot) = %+v, want persisted conflict retained", first)
	}
	store.primary = map[metadb.ConversationStateKey]metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2}: {
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2,
			ActiveAt: initialActiveAt, ReadSeq: readSeq,
		},
	}

	second, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(receiver retry) error = %v", err)
	}
	if second.Persisted != 0 || second.Skipped != 1 || second.Cleared != 1 {
		t.Fatalf("Flush(receiver retry) = %+v, want cooldown skip after persisted sender rebase", second)
	}
	if got := len(store.touches); got != 1 {
		t.Fatalf("durable touch batches = %d, want only the successful sender snapshot", got)
	}
	requireDirtyIndexConservation(t, m)
}

func TestPersistedSenderConflictRetainsConcurrentNewerReadSeqForRetry(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2,
		ActiveAtMS: 1_000, ReadSeq: 9,
	}}); err != nil {
		t.Fatalf("MarkActive(sender) error = %v", err)
	}
	store.touchHook = func() {
		store.touchHook = nil
		if err := m.MarkActive(ctx, []ActivePatch{{
			Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2,
			ActiveAtMS: 2_000, ReadSeq: 10,
		}}); err != nil {
			t.Fatalf("MarkActive(concurrent sender) error = %v", err)
		}
	}
	first, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(sender snapshot) error = %v", err)
	}
	if first.Persisted != 1 || first.Cleared != 0 || first.VersionConflicts != 1 || first.Requeued != 1 {
		t.Fatalf("Flush(sender snapshot) = %+v, want persisted conflict retained", first)
	}
	store.primary = map[metadb.ConversationStateKey]metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2}: {
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2,
			ActiveAt: 1_000, ReadSeq: 9,
		},
	}

	second, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(newer sender retry) error = %v", err)
	}
	if second.Persisted != 1 || second.Skipped != 0 || second.Cleared != 1 {
		t.Fatalf("Flush(newer sender retry) = %+v, want forced persist beyond cooldown", second)
	}
	if got := len(store.touches); got != 2 || len(store.touches[1]) != 1 || store.touches[1][0].ReadSeq != 10 {
		t.Fatalf("durable touches = %+v, want retried newer ReadSeq 10", store.touches)
	}
	requireDirtyIndexConservation(t, m)
}

func TestMixedSenderAndReceiverFlushPreservesCurrentDirtyClassification(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			{UID: "sender", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2}: {
				UID: "sender", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2,
				ActiveAt: 900, ReadSeq: 10,
			},
			{UID: "receiver", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2}: {
				UID: "receiver", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2,
				ActiveAt: 900,
			},
		},
	}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "sender", ChannelID: "room", ChannelType: 2,
		ActiveAtMS: 1_000, ReadSeq: 10,
	}}); err != nil {
		t.Fatalf("MarkActive(sender) error = %v", err)
	}
	if err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "sender", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_100},
		{Kind: metadb.ConversationKindNormal, UID: "receiver", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_100},
	}); err != nil {
		t.Fatalf("MarkActive(receiver updates) error = %v", err)
	}

	result, err := m.Flush(ctx, 0)
	if err != nil {
		t.Fatalf("Flush(mixed) error = %v", err)
	}
	if result.Selected != 2 || result.Persisted != 1 || result.Skipped != 1 || result.Cleared != 2 ||
		result.VersionConflicts != 0 || result.Superseded != 0 || result.Requeued != 0 {
		t.Fatalf("Flush(mixed) = %+v, want complete persisted/skipped conservation without retained rows", result)
	}
	if got := len(store.touches); got != 1 || len(store.touches[0]) != 1 || store.touches[0][0].UID != "sender" || store.touches[0][0].ReadSeq != 10 {
		t.Fatalf("durable touches = %+v, want only sticky sender update", store.touches)
	}
	receiver, ok := m.EntryForTest(metadb.ConversationKindNormal, "receiver", "room", 2)
	if !ok || receiver.ActiveAtMS != 1_100 {
		t.Fatalf("receiver cache entry = %+v present=%v, want latest receiver ActiveAt 1100", receiver, ok)
	}
	requireDirtyIndexConservation(t, m)
	if observation := m.cacheObservation(); observation.DirtyRows != 0 || observation.DirtyQueueRows != 0 || observation.DirtyAgeBuckets != 0 {
		t.Fatalf("post-mixed cache observation = %+v, want all dirty indexes empty", observation)
	}
}

func TestDirtyHashSlotMoveRemovesOldOwnerAndPreservesSenderClassification(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2}: {
				UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2,
				ActiveAt: 1_500, ReadSeq: 9,
			},
		},
	}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour})
	patch := ActivePatch{
		Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2,
		ActiveAtMS: 1_000, ReadSeq: 9,
	}
	if err := m.MarkActiveForHashSlot(ctx, 1, []ActivePatch{patch}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(old) error = %v", err)
	}
	patch.ActiveAtMS = 2_000
	if err := m.MarkActiveForHashSlot(ctx, 9, []ActivePatch{patch}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(new) error = %v", err)
	}
	if result, err := m.FlushHashSlot(ctx, 1, 0); err != nil || result.Selected != 0 {
		t.Fatalf("FlushHashSlot(old) = %+v, %v, want no stale selection", result, err)
	}
	result, err := m.FlushHashSlot(ctx, 9, 0)
	if err != nil {
		t.Fatalf("FlushHashSlot(new) error = %v", err)
	}
	if result.Selected != 1 || result.Persisted != 1 || result.Skipped != 0 || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot(new) = %+v, want one persisted sender row", result)
	}
	if got := len(store.touches); got != 1 || len(store.touches[0]) != 1 || store.touches[0][0].ReadSeq != 9 {
		t.Fatalf("durable touches = %+v, want sender ReadSeq preserved on new hash slot", store.touches)
	}
	requireDirtyIndexConservation(t, m)
}

func TestPressureDrainDoesNotSpinAfterZeroProgress(t *testing.T) {
	ctx := context.Background()
	pressureSignals := make(chan PressureSignal, 1)
	store := &conflictingSelectionStore{seen: make(map[string]int)}
	m := NewManager(Options{Store: store, MaxCachedRows: 10, PressureNotify: pressureSignals})
	store.manager = m
	patches := make([]ActivePatch, 0, 10)
	for i := 0; i < 10; i++ {
		patches = append(patches, ActivePatch{
			Kind: metadb.ConversationKindNormal, UID: fmt.Sprintf("u-%02d", i),
			ChannelID: "room", ChannelType: 2, ActiveAtMS: int64(1_000 + i),
		})
	}
	if err := m.MarkActive(ctx, patches); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	select {
	case <-pressureSignals:
	default:
		t.Fatal("high watermark did not start pressure drain")
	}

	result, err := m.Flush(ctx, 1)
	if err != nil || result.Cleared != 0 || result.VersionConflicts != 1 {
		t.Fatalf("Flush(conflict) = %+v, %v, want zero progress with one conflict", result, err)
	}
	select {
	case <-pressureSignals:
		t.Fatal("zero-progress pressure flush immediately re-signaled and would spin")
	default:
	}

	store.disableConflicts = true
	result, err = m.Flush(ctx, 1)
	if err != nil || result.Cleared != 1 {
		t.Fatalf("Flush(periodic recovery) = %+v, %v, want one cleared row", result, err)
	}
	select {
	case <-pressureSignals:
	default:
		t.Fatal("progressing periodic flush did not resume pressure drain")
	}
	requireDirtyIndexConservation(t, m)
}

func TestReceiverCooldownTrafficDoesNotRestartPressureDrain(t *testing.T) {
	ctx := context.Background()
	const rows = 10
	const durableActiveAt int64 = 1_000
	pressureSignals := make(chan PressureSignal, 1)
	observer := &recordingConversationActiveObserver{}
	primary := make(map[metadb.ConversationStateKey]metadb.ConversationState, rows)
	initial := make([]ActivePatch, 0, rows)
	for index := 0; index < rows; index++ {
		uid := fmt.Sprintf("u-%02d", index)
		key := metadb.ConversationStateKey{
			UID:         uid,
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "room",
			ChannelType: 2,
		}
		primary[key] = metadb.ConversationState{
			UID:         uid,
			Kind:        key.Kind,
			ChannelID:   key.ChannelID,
			ChannelType: key.ChannelType,
			ActiveAt:    durableActiveAt,
		}
		initial = append(initial, ActivePatch{
			Kind:        key.Kind,
			UID:         uid,
			ChannelID:   key.ChannelID,
			ChannelType: uint8(key.ChannelType),
			ActiveAtMS:  durableActiveAt + int64(30*time.Minute/time.Millisecond),
			MessageSeq:  1,
		})
	}
	m := NewManager(Options{
		Store:          &recordingActiveStore{primary: primary},
		ActiveCooldown: time.Hour,
		MaxCachedRows:  rows,
		PressureNotify: pressureSignals,
		Observer:       observer,
	})
	if err := m.MarkActive(ctx, initial); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	select {
	case <-pressureSignals:
	default:
		t.Fatal("initial dirty rows did not start pressure drain")
	}
	if result, err := m.Flush(ctx, 0); err != nil || result.Skipped != rows || result.Cleared != rows {
		t.Fatalf("Flush(initial) = %+v, %v, want all receiver rows skipped and cleared", result, err)
	}
	if got := m.cacheObservation(); got.DirtyRows != 0 || got.PressureDraining {
		t.Fatalf("post-flush cache = %+v, want clean rows below pressure watermark", got)
	}

	for iteration := 0; iteration < 100; iteration++ {
		updates := append([]ActivePatch(nil), initial...)
		for index := range updates {
			updates[index].ActiveAtMS = durableActiveAt + int64(45*time.Minute/time.Millisecond) + int64(iteration)
			updates[index].MessageSeq = uint64(iteration + 2)
		}
		if err := m.MarkActive(ctx, updates); err != nil {
			t.Fatalf("MarkActive(iteration=%d) error = %v", iteration, err)
		}
	}
	if got := m.cacheObservation(); got.DirtyRows != 0 || got.DirtyQueueRows != 0 || got.DirtyAgeBuckets != 0 || got.PressureDraining {
		t.Fatalf("steady receiver cooldown cache = %+v, want clean pressure-free cache", got)
	}
	lastActiveAt := durableActiveAt + int64(45*time.Minute/time.Millisecond) + 99
	for index := 0; index < rows; index++ {
		uid := fmt.Sprintf("u-%02d", index)
		entry, ok := m.EntryForTest(metadb.ConversationKindNormal, uid, "room", 2)
		if !ok || entry.ActiveAtMS != lastActiveAt || entry.MessageSeq != 101 {
			t.Fatalf("steady receiver row %s = %+v present=%t, want latest view active=%d seq=101", uid, entry, ok, lastActiveAt)
		}
	}
	if mutation := observer.lastMutation(t); mutation.CooldownSuppressed != rows || mutation.Unchanged != 0 || mutation.BecameDirty != 0 || mutation.DirtyUpdated != 0 {
		t.Fatalf("steady receiver mutation = %+v, want %d cooldown-suppressed rows", mutation, rows)
	}
	select {
	case signal := <-pressureSignals:
		t.Fatalf("receiver cooldown traffic restarted pressure drain: %+v", signal)
	default:
	}
	requireDirtyIndexConservation(t, m)
}

func TestAdmissionCacheObservationsCoalesceButFlushForcesRefresh(t *testing.T) {
	ctx := context.Background()
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{
		Store:                    &recordingActiveStore{},
		Observer:                 observer,
		CacheObservationInterval: time.Hour,
	})
	for index := 0; index < 2; index++ {
		if err := m.MarkActive(ctx, []ActivePatch{{
			Kind: metadb.ConversationKindNormal, UID: fmt.Sprintf("u-%d", index),
			ChannelID: "room", ChannelType: 2, ActiveAtMS: int64(1_000 + index),
		}}); err != nil {
			t.Fatalf("MarkActive(index=%d) error = %v", index, err)
		}
	}
	if got := len(observer.cache); got != 1 {
		t.Fatalf("admission cache observations = %d, want one coalesced sample", got)
	}
	if got := len(observer.mutation); got != 2 {
		t.Fatalf("mutation observations = %d, want every admission retained", got)
	}

	if result, err := m.Flush(ctx, 0); err != nil || result.Cleared != 2 {
		t.Fatalf("Flush() = %+v, %v, want two cleared rows", result, err)
	}
	if got := observer.lastCache(t); got.DirtyRows != 0 || got.DirtyQueueRows != 0 || got.DirtyAgeBuckets != 0 {
		t.Fatalf("post-flush cache observation = %+v, want all dirty indexes empty", got)
	}
}

func TestRepeatedHardLimitRejectionsCoalesceCacheObservations(t *testing.T) {
	ctx := context.Background()
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{
		MaxCachedRows:            1,
		Observer:                 observer,
		CacheObservationInterval: time.Hour,
	})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u-0",
		ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000,
	}}); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	for index := 1; index <= 2; index++ {
		err := m.MarkActive(ctx, []ActivePatch{{
			Kind: metadb.ConversationKindNormal, UID: fmt.Sprintf("u-%d", index),
			ChannelID: "room", ChannelType: 2, ActiveAtMS: int64(1_000 + index),
		}})
		if !errors.Is(err, ErrCachePressure) {
			t.Fatalf("MarkActive(rejection=%d) error = %v, want %v", index, err, ErrCachePressure)
		}
	}
	if got := len(observer.cache); got != 1 {
		t.Fatalf("cache observations after repeated hard-limit rejection = %d, want one pressure-transition sample", got)
	}
	if got := len(observer.mutation); got != 3 {
		t.Fatalf("admission lock observations = %d, want initial plus two rejected attempts", got)
	}
	for index, mutation := range observer.mutation[1:] {
		if mutation.Result != "cache_pressure" || mutation.BecameDirty != 0 || mutation.DirtyUpdated != 0 || mutation.Unchanged != 0 {
			t.Fatalf("rejected admission observation[%d] = %+v, want timing-only cache_pressure result", index, mutation)
		}
	}
}

func TestHighWatermarkTransitionForcesCacheObservationInsideCoalesceWindow(t *testing.T) {
	ctx := context.Background()
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{
		Store:                    &recordingActiveStore{},
		MaxCachedRows:            10,
		Observer:                 observer,
		CacheObservationInterval: time.Hour,
	})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u-0",
		ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000,
	}}); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	patches := make([]ActivePatch, 0, 7)
	for index := 1; index < 8; index++ {
		patches = append(patches, ActivePatch{
			Kind: metadb.ConversationKindNormal, UID: fmt.Sprintf("u-%d", index),
			ChannelID: "room", ChannelType: 2, ActiveAtMS: int64(1_000 + index),
		})
	}
	if err := m.MarkActive(ctx, patches); err != nil {
		t.Fatalf("MarkActive(high watermark) error = %v", err)
	}
	if got := len(observer.cache); got != 2 {
		t.Fatalf("cache observations across high-watermark transition = %d, want initial plus forced transition", got)
	}
	last := observer.lastCache(t)
	if last.Rows != 8 || last.DirtyRows != 8 || !last.PressureDraining {
		t.Fatalf("high-watermark cache observation = %+v, want rows=dirty=8 and draining", last)
	}
	if result, err := m.Flush(ctx, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("Flush(to low watermark) = %+v, %v, want one clear", result, err)
	}
	last = observer.lastCache(t)
	if last.Rows != 8 || last.DirtyRows != 7 || last.PressureDraining {
		t.Fatalf("low-watermark cache observation = %+v, want rows=8 dirty=7 and draining=false", last)
	}
}

type conflictingSelectionStore struct {
	recordingActiveStore
	manager          *Manager
	seen             map[string]int
	next             int64
	disableConflicts bool
}

func (s *conflictingSelectionStore) TouchConversationActiveAt(ctx context.Context, patches []metadb.ConversationActivePatch) error {
	for _, patch := range patches {
		s.seen[patch.UID]++
		if s.disableConflicts {
			continue
		}
		s.next++
		if err := s.manager.MarkActive(ctx, []ActivePatch{{
			Kind:        patch.Kind,
			UID:         patch.UID,
			ChannelID:   patch.ChannelID,
			ChannelType: uint8(patch.ChannelType),
			ActiveAtMS:  patch.ActiveAt + 10_000 + s.next,
			ReadSeq:     patch.ReadSeq,
		}}); err != nil {
			return err
		}
	}
	return nil
}

func requireDirtyIndexConservation(t *testing.T, manager *Manager) {
	t.Helper()
	observation := manager.cacheObservation()
	if observation.DirtyQueueRows != observation.DirtyRows {
		t.Fatalf("dirty queue rows=%d, dirty rows=%d", observation.DirtyQueueRows, observation.DirtyRows)
	}
	if observation.DirtyAgeBuckets > observation.DirtyRows {
		t.Fatalf("dirty age buckets=%d, dirty rows=%d", observation.DirtyAgeBuckets, observation.DirtyRows)
	}
}

func requireCacheIndexConservation(t *testing.T, manager *Manager) {
	t.Helper()
	requireDirtyIndexConservation(t, manager)

	manager.mu.RLock()
	defer manager.mu.RUnlock()
	var indexedHashSlotRows int
	for hashSlot, byAddress := range manager.cleanByHashSlot {
		if len(byAddress) == 0 {
			t.Fatalf("clean hash-slot index %d contains an empty address set", hashSlot)
		}
		for address := range byAddress {
			indexedHashSlotRows++
			byChannel := manager.cache[address.uid]
			entry, ok := byChannel[address.key]
			if !ok {
				t.Fatalf("clean hash-slot index %d contains missing cache address %+v", hashSlot, address)
			}
			if entry.dirty || !entry.hasHashSlot || entry.hashSlot != hashSlot {
				t.Fatalf("clean hash-slot index %d contains invalid cache entry %+v for address %+v", hashSlot, entry, address)
			}
		}
	}
	var wantHashSlotRows int
	for uid, byChannel := range manager.cache {
		for key, entry := range byChannel {
			if entry.dirty || !entry.hasHashSlot {
				continue
			}
			wantHashSlotRows++
			address := cacheAddress{uid: uid, key: key}
			if _, ok := manager.cleanByHashSlot[entry.hashSlot][address]; !ok {
				t.Fatalf("clean cache address %+v is missing from hash-slot index %d", address, entry.hashSlot)
			}
		}
	}
	if indexedHashSlotRows != wantHashSlotRows {
		t.Fatalf("clean hash-slot index rows=%d, clean hash-slot cache rows=%d", indexedHashSlotRows, wantHashSlotRows)
	}
	if manager.maxCachedRows <= 0 {
		if manager.cleanIndex != nil {
			t.Fatalf("unbounded manager clean index is initialized with %d rows", len(manager.cleanIndex))
		}
		return
	}
	wantCleanRows := manager.totalRows - manager.dirtyRows
	if got := len(manager.cleanIndex); got != wantCleanRows {
		t.Fatalf("clean index rows=%d, total-clean rows=%d", got, wantCleanRows)
	}
	if manager.totalRows > manager.maxCachedRows {
		t.Fatalf("cached rows=%d exceed bound=%d", manager.totalRows, manager.maxCachedRows)
	}

	var cachedRows int
	for uid, byChannel := range manager.cache {
		for key, entry := range byChannel {
			cachedRows++
			address := cacheAddress{uid: uid, key: key}
			_, indexedClean := manager.cleanIndex[address]
			if entry.dirty && indexedClean {
				t.Fatalf("dirty cache address %+v is also indexed clean", address)
			}
			if !entry.dirty && !indexedClean {
				t.Fatalf("clean cache address %+v is missing from clean index", address)
			}
		}
	}
	if cachedRows != manager.totalRows {
		t.Fatalf("cache iteration rows=%d, tracked total rows=%d", cachedRows, manager.totalRows)
	}
	for address := range manager.cleanIndex {
		byChannel := manager.cache[address.uid]
		entry, ok := byChannel[address.key]
		if !ok {
			t.Fatalf("clean index contains missing cache address %+v", address)
		}
		if entry.dirty {
			t.Fatalf("clean index contains dirty cache address %+v", address)
		}
	}
}
