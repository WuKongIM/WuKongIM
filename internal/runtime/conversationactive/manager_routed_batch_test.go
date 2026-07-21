package conversationactive

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestAdmitRoutedActiveBatchesUsesExactHashSlotIndexes(t *testing.T) {
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{MaxCachedRows: 10, Observer: observer})
	err := m.AdmitRoutedActiveBatches(context.Background(), []RoutedActiveBatch{
		{
			HashSlot: 3,
			Batch: ActiveBatch{
				Kind:        metadb.ConversationKindNormal,
				SenderUID:   "alice",
				ChannelID:   "room",
				ChannelType: 2,
				MessageSeq:  7,
				ActiveAtMS:  100,
			},
		},
		{
			HashSlot: 9,
			Batch: ActiveBatch{
				Kind:        metadb.ConversationKindNormal,
				ChannelID:   "room",
				ChannelType: 2,
				MessageSeq:  8,
				ActiveAtMS:  110,
				Recipients: []ActiveEntry{
					{UID: "bob"},
					{UID: "bob"},
					{UID: "carol"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("AdmitRoutedActiveBatches() error = %v", err)
	}
	if len(observer.mutation) != 1 {
		t.Fatalf("mutation observations = %d, want one bulk cache mutation", len(observer.mutation))
	}
	if got := observer.mutation[0]; got.Result != "ok" || got.BecameDirty != 3 || got.DirtyUpdated != 0 || got.Unchanged != 0 {
		t.Fatalf("bulk mutation observation = %+v, want one three-row mutation", got)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	if got := len(m.dirtyByHashSlot[3]); got != 1 {
		t.Fatalf("slot 3 dirty rows = %d, want 1", got)
	}
	if got := len(m.dirtyByHashSlot[9]); got != 2 {
		t.Fatalf("slot 9 dirty rows = %d, want 2 coalesced recipients", got)
	}
	for address := range m.dirtyByHashSlot[3] {
		entry := m.cache[address.uid][address.key]
		if !entry.hasHashSlot || entry.hashSlot != 3 {
			t.Fatalf("slot 3 entry = %+v, want exact routed ownership", entry)
		}
	}
	for address := range m.dirtyByHashSlot[9] {
		entry := m.cache[address.uid][address.key]
		if !entry.hasHashSlot || entry.hashSlot != 9 {
			t.Fatalf("slot 9 entry = %+v, want exact routed ownership", entry)
		}
	}
}

func TestAdmitRoutedActiveBatchesRejectsCrossHashSlotAddressAtomically(t *testing.T) {
	m := NewManager(Options{MaxCachedRows: 10})
	groups := []RoutedActiveBatch{
		{HashSlot: 3, Batch: ActiveBatch{Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100, Recipients: []ActiveEntry{{UID: "same"}}}},
		{HashSlot: 9, Batch: ActiveBatch{Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, MessageSeq: 8, ActiveAtMS: 110, Recipients: []ActiveEntry{{UID: "same"}}}},
	}
	err := m.AdmitRoutedActiveBatches(context.Background(), groups)
	if !errors.Is(err, ErrHashSlotConflict) {
		t.Fatalf("AdmitRoutedActiveBatches() error = %v, want ErrHashSlotConflict", err)
	}
	if got := m.cacheObservation(); got.Rows != 0 || got.DirtyRows != 0 {
		t.Fatalf("cache after conflict = %+v, want no partial mutation", got)
	}
}

func TestAdmitRoutedActiveBatchesRejectsExistingCrossHashSlotAddress(t *testing.T) {
	m := NewManager(Options{MaxCachedRows: 10})
	patch := ActivePatch{UID: "same", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}
	if err := m.MarkActiveForHashSlot(context.Background(), 3, []ActivePatch{patch}); err != nil {
		t.Fatalf("MarkActiveForHashSlot() error = %v", err)
	}
	err := m.AdmitRoutedActiveBatches(context.Background(), []RoutedActiveBatch{{
		HashSlot: 9,
		Batch:    ActiveBatch{Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, MessageSeq: 8, ActiveAtMS: 110, Recipients: []ActiveEntry{{UID: "same"}}},
	}})
	if !errors.Is(err, ErrHashSlotConflict) {
		t.Fatalf("AdmitRoutedActiveBatches() error = %v, want ErrHashSlotConflict", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	address := activePatchAddress(patch)
	entry := m.cache[address.uid][address.key]
	if entry.hashSlot != 3 || entry.patch.MessageSeq != 7 {
		t.Fatalf("existing entry after conflict = %+v, want original slot/version", entry)
	}
	if _, ok := m.dirtyByHashSlot[3][address]; !ok {
		t.Fatalf("original slot index missing address %+v", address)
	}
	if _, ok := m.dirtyByHashSlot[9][address]; ok {
		t.Fatalf("conflicting slot index unexpectedly contains address %+v", address)
	}
}

func TestAdmitRoutedActiveBatchesCachePressureIsAtomicAcrossGroups(t *testing.T) {
	m := NewManager(Options{MaxCachedRows: 1})
	err := m.AdmitRoutedActiveBatches(context.Background(), []RoutedActiveBatch{
		{HashSlot: 3, Batch: ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "alice", ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
		{HashSlot: 9, Batch: ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []ActiveEntry{{UID: "bob"}}, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
	})
	if !errors.Is(err, ErrCachePressure) {
		t.Fatalf("AdmitRoutedActiveBatches() error = %v, want ErrCachePressure", err)
	}
	if got := m.cacheObservation(); got.Rows != 0 || got.DirtyRows != 0 {
		t.Fatalf("cache after pressure = %+v, want no partial mutation", got)
	}
}

func TestAdmitRoutedActiveBatchesOneRecipientHotPathDoesNotAllocate(t *testing.T) {
	m := NewManager(Options{MaxCachedRows: 10})
	routed := []RoutedActiveBatch{{
		HashSlot: 7,
		Batch: ActiveBatch{
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  1,
			Recipients:  []ActiveEntry{{UID: "u1"}},
		},
	}}
	if err := m.AdmitRoutedActiveBatches(context.Background(), routed); err != nil {
		t.Fatalf("seed AdmitRoutedActiveBatches() error = %v", err)
	}

	var admitErr error
	allocs := testing.AllocsPerRun(100, func() {
		routed[0].Batch.ActiveAtMS++
		admitErr = m.AdmitRoutedActiveBatches(context.Background(), routed)
	})
	if admitErr != nil {
		t.Fatalf("AdmitRoutedActiveBatches() error = %v", admitErr)
	}
	if allocs != 0 {
		t.Fatalf("AdmitRoutedActiveBatches() allocations = %.1f, want 0 for the one-recipient hot path", allocs)
	}
}

func TestAdmitRoutedActiveBatchesSmallMultiGroupHotPathDoesNotAllocate(t *testing.T) {
	m := NewManager(Options{MaxCachedRows: 10})
	routed := []RoutedActiveBatch{
		{
			HashSlot: 7,
			Batch: ActiveBatch{
				Kind:        metadb.ConversationKindNormal,
				ChannelID:   "room-1",
				ChannelType: 2,
				ActiveAtMS:  1,
				Recipients:  []ActiveEntry{{UID: "u1"}},
			},
		},
		{
			HashSlot: 9,
			Batch: ActiveBatch{
				Kind:        metadb.ConversationKindNormal,
				ChannelID:   "room-2",
				ChannelType: 2,
				ActiveAtMS:  1,
				Recipients:  []ActiveEntry{{UID: "u2"}},
			},
		},
	}
	if err := m.AdmitRoutedActiveBatches(context.Background(), routed); err != nil {
		t.Fatalf("seed AdmitRoutedActiveBatches() error = %v", err)
	}

	var admitErr error
	allocs := testing.AllocsPerRun(100, func() {
		routed[0].Batch.ActiveAtMS++
		routed[1].Batch.ActiveAtMS++
		admitErr = m.AdmitRoutedActiveBatches(context.Background(), routed)
	})
	if admitErr != nil {
		t.Fatalf("AdmitRoutedActiveBatches() error = %v", admitErr)
	}
	if allocs != 0 {
		t.Fatalf("AdmitRoutedActiveBatches() allocations = %.1f, want 0 for a small multi-group hot path", allocs)
	}
}
