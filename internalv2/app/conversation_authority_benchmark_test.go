package app

import (
	"context"
	"fmt"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

func BenchmarkConversationAuthorityDrainTarget128Of10KDirtyRows(b *testing.B) {
	benchmarkConversationAuthorityDrainTargetRows(b, 10_000, 128)
}

func BenchmarkConversationAuthorityDrainTarget128Of100KDirtyRows(b *testing.B) {
	benchmarkConversationAuthorityDrainTargetRows(b, 100_000, 128)
}

func benchmarkConversationAuthorityDrainTargetRows(b *testing.B, totalRows int, targetRows int) {
	ctx := context.Background()
	targetA := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	targetB := conversationusecase.RouteTarget{HashSlot: 9, SlotID: 10, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		store := &recordingConversationAuthorityStore{}
		authority := newConversationAuthority(conversationAuthorityOptions{
			LocalNodeID:    1,
			Store:          store,
			MaxRows:        totalRows + targetRows + 1,
			FlushBatchRows: 64,
		})
		authority.markActive(targetA)
		authority.markActive(targetB)
		seedAuthorityDirtyRows(b, ctx, authority, targetA, "target", targetRows)
		seedAuthorityDirtyRows(b, ctx, authority, targetB, "other", totalRows-targetRows)
		b.StartTimer()

		result, err := authority.DrainAuthority(ctx, targetA)

		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if result != conversationDrainResultDrained {
			b.Fatalf("DrainAuthority() result = %q, want %q", result, conversationDrainResultDrained)
		}
		if len(store.touched) != targetRows {
			b.Fatalf("flushed rows = %d, want target rows %d", len(store.touched), targetRows)
		}
		for _, patch := range store.touched {
			if patch.ChannelID[:6] != "target" {
				b.Fatalf("flushed non-target row: %+v", patch)
			}
		}
	}
}

func seedAuthorityDirtyRows(b *testing.B, ctx context.Context, authority *conversationAuthority, target conversationusecase.RouteTarget, prefix string, rows int) {
	b.Helper()
	const batchSize = 512
	patches := make([]conversationusecase.ActivePatch, 0, batchSize)
	for i := 0; i < rows; i++ {
		patches = append(patches, conversationusecase.ActivePatch{
			UID:         fmt.Sprintf("%s-u-%06d", prefix, i),
			ChannelID:   fmt.Sprintf("%s-g-%06d", prefix, i),
			ChannelType: 2,
			ActiveAt:    int64(1000 + i),
			MessageSeq:  uint64(i + 1),
		})
		if len(patches) == batchSize {
			if err := authority.AdmitPatches(ctx, target, patches); err != nil {
				b.Fatal(err)
			}
			patches = patches[:0]
		}
	}
	if len(patches) > 0 {
		if err := authority.AdmitPatches(ctx, target, patches); err != nil {
			b.Fatal(err)
		}
	}
}
