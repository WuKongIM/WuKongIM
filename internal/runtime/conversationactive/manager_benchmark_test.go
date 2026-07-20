package conversationactive

import (
	"context"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

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
