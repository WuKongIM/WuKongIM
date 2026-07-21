package conversationactive

import (
	"context"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func BenchmarkMarkActiveHotRowsAtFullCache(b *testing.B) {
	const (
		totalRows = 100_000
		batchRows = 2
	)
	ctx := context.Background()
	m := NewManager(Options{MaxCachedRows: totalRows})
	seed := make([]ActivePatch, 0, totalRows)
	for i := 0; i < totalRows; i++ {
		seed = append(seed, ActivePatch{
			Kind:        metadb.ConversationKindNormal,
			UID:         fmt.Sprintf("u-%06d", i),
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  1_000,
		})
	}
	if err := m.MarkActiveForHashSlot(ctx, 7, seed); err != nil {
		b.Fatal(err)
	}
	patches := make([]ActivePatch, batchRows)
	for i := range patches {
		patches[i] = seed[i]
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		activeAtMS := int64(2_000 + i)
		for j := range patches {
			patches[j].ActiveAtMS = activeAtMS
		}
		if err := m.MarkActiveForHashSlot(ctx, 7, patches); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAdmitActiveBatchOneRecipient(b *testing.B) {
	ctx := context.Background()
	m := NewManager(Options{MaxCachedRows: 100_000})
	batch := ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "room",
		ChannelType: 2,
		Recipients:  []ActiveEntry{{UID: "u1"}},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch.ActiveAtMS = int64(i + 1)
		if err := m.AdmitActiveBatchForHashSlot(ctx, 7, batch); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAdmitActiveBatch512Recipients(b *testing.B) {
	ctx := context.Background()
	recipients := make([]ActiveEntry, 0, 512)
	for i := 0; i < cap(recipients); i++ {
		recipients = append(recipients, ActiveEntry{UID: fmt.Sprintf("u-%04d", i)})
	}
	batch := ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "room",
		ChannelType: 2,
		ActiveAtMS:  1_000,
		Recipients:  recipients,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := NewManager(Options{MaxCachedRows: 100_000})
		if err := m.AdmitActiveBatchForHashSlot(ctx, 7, batch); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAdmitActiveBatch512HotRecipients(b *testing.B) {
	ctx := context.Background()
	recipients := make([]ActiveEntry, 0, 512)
	for i := 0; i < cap(recipients); i++ {
		recipients = append(recipients, ActiveEntry{UID: fmt.Sprintf("u-%04d", i)})
	}
	batch := ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "room",
		ChannelType: 2,
		ActiveAtMS:  1_000,
		Recipients:  recipients,
	}
	m := NewManager(Options{MaxCachedRows: 100_000})
	if err := m.AdmitActiveBatchForHashSlot(ctx, 7, batch); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch.ActiveAtMS = int64(2_000 + i)
		if err := m.AdmitActiveBatchForHashSlot(ctx, 7, batch); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAdmitOneRowWithOneCleanVictimInFullCache(b *testing.B) {
	const totalRows = 100_000
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: totalRows})
		seed := make([]ActivePatch, 0, totalRows)
		for row := 0; row < totalRows; row++ {
			seed = append(seed, ActivePatch{
				Kind:        metadb.ConversationKindNormal,
				UID:         fmt.Sprintf("u-%06d", row),
				ChannelID:   "room",
				ChannelType: 2,
				ActiveAtMS:  1_000,
			})
		}
		if err := m.MarkActiveForHashSlot(ctx, 1, seed[:1]); err != nil {
			b.Fatal(err)
		}
		if err := m.MarkActiveForHashSlot(ctx, 2, seed[1:]); err != nil {
			b.Fatal(err)
		}
		if result, err := m.FlushHashSlot(ctx, 1, 1); err != nil || result.Cleared != 1 {
			b.Fatalf("FlushHashSlot() = %+v, %v", result, err)
		}
		incoming := []ActivePatch{{
			Kind:        metadb.ConversationKindNormal,
			UID:         "incoming",
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  2_000,
		}}
		b.StartTimer()
		if err := m.MarkActiveForHashSlot(ctx, 3, incoming); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFlushHashSlotSelected128Of10KDirtyRows(b *testing.B) {
	benchmarkFlushHashSlotSelectedRows(b, 10_000, 128)
}

func BenchmarkFlushHashSlotSelected128Of100KDirtyRows(b *testing.B) {
	benchmarkFlushHashSlotSelectedRows(b, 100_000, 128)
}

func BenchmarkMarkActiveCoalesces100KUpdates(b *testing.B) {
	ctx := context.Background()
	patches := make([]ActivePatch, 0, 100_000)
	for i := 0; i < cap(patches); i++ {
		patches = append(patches, ActivePatch{
			Kind:        metadb.ConversationKindNormal,
			UID:         fmt.Sprintf("u-%04d", i%1000),
			ChannelID:   fmt.Sprintf("g-%04d", i%100),
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
			ReadSeq:     uint64(i),
		})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := NewManager(Options{})
		if err := m.MarkActiveForHashSlot(ctx, 7, patches); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkFlushHashSlotSelectedRows(b *testing.B, totalRows int, targetRows int) {
	const targetHashSlot uint16 = 7
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		store := &recordingActiveStore{}
		m := NewManager(Options{Store: store})
		seedDirtyHashSlots(b, ctx, m, totalRows, targetHashSlot, targetRows)
		b.StartTimer()

		result, err := m.FlushHashSlot(ctx, targetHashSlot, 0)

		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if result.Selected != targetRows || result.Persisted != targetRows {
			b.Fatalf("FlushHashSlot() result = %+v, want selected=%d persisted=%d", result, targetRows, targetRows)
		}
		if len(store.touches) != 1 || len(store.touches[0]) != targetRows {
			touchedRows := 0
			if len(store.touches) > 0 {
				touchedRows = len(store.touches[0])
			}
			b.Fatalf("touch batches = %d rows=%d, want one batch with %d rows", len(store.touches), touchedRows, targetRows)
		}
	}
}

func seedDirtyHashSlots(b *testing.B, ctx context.Context, m *Manager, totalRows int, targetHashSlot uint16, targetRows int) {
	b.Helper()
	const hashSlotCount = 128
	bySlot := make(map[uint16][]ActivePatch, hashSlotCount)
	for i := 0; i < totalRows; i++ {
		hashSlot := uint16(i % hashSlotCount)
		if i < targetRows {
			hashSlot = targetHashSlot
		} else if hashSlot == targetHashSlot {
			hashSlot = (hashSlot + 1) % hashSlotCount
		}
		bySlot[hashSlot] = append(bySlot[hashSlot], ActivePatch{
			Kind:        metadb.ConversationKindNormal,
			UID:         fmt.Sprintf("u-%06d", i),
			ChannelID:   fmt.Sprintf("g-%06d", i),
			ChannelType: 2,
			ActiveAtMS:  int64(1000 + i),
		})
	}
	for hashSlot, patches := range bySlot {
		if err := m.MarkActiveForHashSlot(ctx, hashSlot, patches); err != nil {
			b.Fatal(err)
		}
	}
}
