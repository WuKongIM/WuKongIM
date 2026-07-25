package backup_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMessageLogSourceRestartReconcilesCommittedRowsWithoutWakeHint(t *testing.T) {
	node := &fakeContinuousMessageNode{
		meta: metadb.ChannelRuntimeMeta{
			ChannelID: "room-a", ChannelType: 2, ChannelEpoch: 7,
			LeaderEpoch: 3, Leader: 2, MinISR: 2,
		},
		hw: 2,
	}
	resolver := &fakeMessageBoundaryResolver{}
	source, err := backupinfra.NewMessageLogSource(node, resolver)
	require.NoError(t, err)

	initial, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{})
	require.NoError(t, err)
	require.Equal(t, uint64(2), initial.Position)
	first, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: initial.Position, ThroughCursor: initial.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.True(t, first.Done)
	require.Len(t, first.Records, 2)
	require.Equal(t, uint64(2), first.MessageCursors[0].HW)

	cursorHead := validContinuousTestReference("d")
	resolver.boundaries = append([]backupartifact.ChannelBoundary(nil), first.MessageCursors...)
	restarted := backupcontract.StreamFrontier{
		Sequence: 1, CursorHead: &cursorHead, SourceCursor: first.NextCursor,
		SourceHighWatermark: initial.Position,
	}
	node.hw = 4 // No Wake call exists here; the authoritative scan must discover it.
	next := observeNextMessageCut(t, source, restarted)
	require.Equal(t, uint64(4), next.Position)
	second, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", CursorSequence: restarted.Sequence,
		CursorSourceCursor: restarted.SourceCursor, CursorHead: restarted.CursorHead,
		AfterCursor: restarted.SourceCursor, ThroughPosition: next.Position, ThroughCursor: next.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.True(t, second.Done)
	require.Len(t, second.Records, 2)
	for index, body := range second.Records {
		record, err := backupartifact.LoadMessageLogRecord(body)
		require.NoError(t, err)
		require.Equal(t, uint64(index+3), record.MessageSeq)
	}
	require.Equal(t, backupinfra.MessageCursorResolveRequest{
		Head: cursorHead, HashSlot: 17, Generation: "slot-generation-1",
		Sequence: 1, SourceCursor: first.NextCursor, SourceHighWatermark: 2,
	}, resolver.last)
}

func TestMessageLogSourceResumesAfterMaterializedBaselineWithoutReplay(t *testing.T) {
	node := &fakeContinuousMessageNode{
		meta: validMessageRuntimeMeta("room-a"),
		hw:   4,
	}
	resolver := &fakeMessageBoundaryResolver{
		baseline: []backupartifact.ChannelBoundary{{
			ChannelID: "room-a", ChannelType: 2, Epoch: 7, HW: 3,
		}},
	}
	source, err := backupinfra.NewMessageLogSource(node, resolver)
	require.NoError(t, err)
	baselineHead := validContinuousTestReference("e")
	frontier := backupcontract.StreamFrontier{
		BaselineCursorHead: &baselineHead, WatermarkAtUnixMillis: 1_753_400_000_000,
	}

	watermark, err := source.HighWatermark(
		context.Background(), 17, "rebase-00017-00000000000000000002", frontier,
	)
	require.NoError(t, err)
	require.Equal(t, uint64(1), watermark.Position, "source position advances by one newly committed message")
	page, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation:         "rebase-00017-00000000000000000002",
		BaselineCursorHead: &baselineHead,
		ThroughPosition:    watermark.Position, ThroughCursor: watermark.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Len(t, page.Records, 1)
	record, err := backupartifact.LoadMessageLogRecord(page.Records[0])
	require.NoError(t, err)
	require.Equal(t, uint64(4), record.MessageSeq)
	require.Equal(t, 1, resolver.baselineCalls, "the exact previous boundary travels in the pinned cut cursor")
}

func TestMessageLogSourcePinsOneExactChannelFromMetadataPage(t *testing.T) {
	node := &fakeContinuousMessageNode{
		metas: []metadb.ChannelRuntimeMeta{
			validMessageRuntimeMeta("room-a"),
			validMessageRuntimeMeta("room-b"),
			validMessageRuntimeMeta("room-c"),
		},
		hw: 1,
	}
	source, err := backupinfra.NewMessageLogSource(node, &fakeMessageBoundaryResolver{})
	require.NoError(t, err)

	watermark, err := source.HighWatermark(
		context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{},
	)
	require.NoError(t, err)
	require.Equal(t, uint64(1), watermark.Position)
	require.True(t, watermark.ReconcilePending)
	require.True(t, watermark.DiscoveryPending)
	page, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: watermark.Position, ThroughCursor: watermark.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Len(t, page.Records, 1)
	record, err := backupartifact.LoadMessageLogRecord(page.Records[0])
	require.NoError(t, err)
	require.Equal(t, "room-a", record.ChannelID)
	require.Equal(t, 1, node.listCalls)
}

func TestMessageLogSourceBatchesHighWatermarkObservationsPerMetadataPage(t *testing.T) {
	node := &fakeContinuousMessageNode{
		metas: []metadb.ChannelRuntimeMeta{
			validMessageRuntimeMeta("room-a"),
			validMessageRuntimeMeta("room-b"),
			validMessageRuntimeMeta("room-c"),
		},
		hw: 1,
	}
	source, err := backupinfra.NewMessageLogSource(node, &fakeMessageBoundaryResolver{})
	require.NoError(t, err)

	watermark, err := source.HighWatermark(
		context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{},
	)
	require.NoError(t, err)
	require.Equal(t, uint64(1), watermark.Position)
	require.Equal(t, 1, node.listCalls)
	require.Equal(t, 1, node.batchCalls, "one bounded leader-batched observation must cover the metadata page")
}

func TestMessageLogSourceReportsReconciliationUntilBoundedSweepCompletes(t *testing.T) {
	metas := make([]metadb.ChannelRuntimeMeta, 300)
	for index := range metas {
		metas[index] = validMessageRuntimeMeta(fmt.Sprintf("room-%03d", index))
	}
	node := &fakeContinuousMessageNode{metas: metas}
	source, err := backupinfra.NewMessageLogSource(node, &fakeMessageBoundaryResolver{})
	require.NoError(t, err)

	first, err := source.HighWatermark(
		context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{},
	)
	require.NoError(t, err)
	require.Zero(t, first.Position)
	require.True(t, first.ReconcilePending)
	require.True(t, first.DiscoveryPending)
	require.Equal(t, 1, node.listCalls)

	second, err := source.HighWatermark(
		context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{},
	)
	require.NoError(t, err)
	require.Zero(t, second.Position)
	require.False(t, second.ReconcilePending)
	require.Equal(t, 2, node.listCalls)
}

func TestMessageLogSourceHintSkipsPagedSweepButRemainsExact(t *testing.T) {
	node := &fakeContinuousMessageNode{
		metas: []metadb.ChannelRuntimeMeta{
			validMessageRuntimeMeta("room-a"),
			validMessageRuntimeMeta("room-z"),
		},
		hwByChannel: map[string]uint64{"room-a": 1, "room-z": 3},
	}
	source, err := backupinfra.NewMessageLogSource(node, &fakeMessageBoundaryResolver{})
	require.NoError(t, err)
	require.True(t, source.HintChannel(17, "room-z", 2))

	watermark, err := source.HighWatermark(
		context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{},
	)
	require.NoError(t, err)
	require.Equal(t, uint64(3), watermark.Position)
	require.Zero(t, node.listCalls, "the in-memory hint should bypass the periodic metadata page")
	require.Zero(t, node.batchCalls)

	node.hwByChannel["room-z"] = 4
	page, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: watermark.Position, ThroughCursor: watermark.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Len(t, page.Records, 3, "the post-observation fourth row must remain outside this cut")
	require.Equal(t, uint64(3), page.MessageCursors[0].HW)
}

func TestMessageLogSourceCursorSupportsMaximumChannelID(t *testing.T) {
	channelID := strings.Repeat("c", 4<<10)
	node := &fakeContinuousMessageNode{
		meta: validMessageRuntimeMeta(channelID),
		hw:   1,
	}
	source, err := backupinfra.NewMessageLogSource(node, &fakeMessageBoundaryResolver{})
	require.NoError(t, err)
	watermark, err := source.HighWatermark(
		context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, watermark.CutCursor)
	require.LessOrEqual(t, len(watermark.CutCursor), 8<<10)
	page, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: watermark.Position, ThroughCursor: watermark.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.True(t, page.Done)
}

func TestMessageLogSourceHintDoesNotAdvanceAuthoritativeSweepCursor(t *testing.T) {
	node := &fakeContinuousMessageNode{
		metas: []metadb.ChannelRuntimeMeta{
			validMessageRuntimeMeta("room-a"),
			validMessageRuntimeMeta("room-b"),
			validMessageRuntimeMeta("room-c"),
		},
		hw: 1,
	}
	source, err := backupinfra.NewMessageLogSource(node, &fakeMessageBoundaryResolver{})
	require.NoError(t, err)
	frontier := backupcontract.StreamFrontier{}

	first, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	firstPage, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation:      "slot-generation-1",
		ThroughPosition: first.Position, ThroughCursor: first.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	source.AcknowledgeSourcePage(17, backupartifact.SegmentStreamMessages, first.CutCursor)

	require.True(t, source.HintChannel(17, "room-c", 2))
	hinted, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	hintedPage, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", AfterCursor: firstPage.NextCursor,
		ThroughPosition: hinted.Position, ThroughCursor: hinted.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	source.AcknowledgeSourcePage(17, backupartifact.SegmentStreamMessages, hinted.CutCursor)

	next, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	page, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", AfterCursor: hintedPage.NextCursor,
		ThroughPosition: next.Position, ThroughCursor: next.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	record, err := backupartifact.LoadMessageLogRecord(page.Records[0])
	require.NoError(t, err)
	require.Equal(t, "room-b", record.ChannelID)
}

func TestMessageLogSourceRestartPinsIncompleteCutBeforeNewCommits(t *testing.T) {
	node := &fakeContinuousMessageNode{
		meta: validMessageRuntimeMeta("room-a"),
		hw:   2,
	}
	resolver := &fakeMessageBoundaryResolver{}
	source, err := backupinfra.NewMessageLogSource(node, resolver)
	require.NoError(t, err)

	cut, err := source.HighWatermark(
		context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{},
	)
	require.NoError(t, err)
	require.Equal(t, uint64(2), cut.Position)
	first, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: cut.Position, ThroughCursor: cut.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 1,
	})
	require.NoError(t, err)
	require.False(t, first.Done)
	require.Equal(t, uint64(1), first.NextPosition)
	require.Len(t, first.MessageCursors, 1)

	cursorHead := validContinuousTestReference("e")
	resolver.boundaries = append([]backupartifact.ChannelBoundary(nil), first.MessageCursors...)
	partial := backupcontract.StreamFrontier{
		Sequence: 1, CursorHead: &cursorHead, SourceCursor: first.NextCursor,
		SourceHighWatermark: first.NextPosition,
	}
	node.hw = 3 // New work arrives after the first half of the old cut was made durable.
	pinned, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", partial)
	require.NoError(t, err)
	require.Equal(t, uint64(2), pinned.Position, "restart must finish the original cut before extending it")

	second, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", CursorSequence: partial.Sequence,
		CursorSourceCursor: partial.SourceCursor, CursorHead: partial.CursorHead,
		AfterCursor: partial.SourceCursor, ThroughPosition: pinned.Position, ThroughCursor: pinned.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 1,
	})
	require.NoError(t, err)
	require.True(t, second.Done)
	require.Equal(t, uint64(2), second.NextPosition)
	record, err := backupartifact.LoadMessageLogRecord(second.Records[0])
	require.NoError(t, err)
	require.Equal(t, "room-a", record.ChannelID)
	require.Equal(t, uint64(2), record.MessageSeq)
}

func TestMessageLogSourceExactCutDoesNotLetHotChannelDisplaceNextChannel(t *testing.T) {
	node := &fakeContinuousMessageNode{
		metas: []metadb.ChannelRuntimeMeta{
			validMessageRuntimeMeta("room-a"),
			validMessageRuntimeMeta("room-b"),
		},
		hwByChannel: map[string]uint64{"room-a": 1, "room-b": 1},
	}
	resolver := &fakeMessageBoundaryResolver{}
	source, err := backupinfra.NewMessageLogSource(node, resolver)
	require.NoError(t, err)

	watermark, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{})
	require.NoError(t, err)
	require.Equal(t, uint64(1), watermark.Position)
	node.hwByChannel["room-a"] = 2 // This row was not part of the pinned cut.
	first, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: watermark.Position, ThroughCursor: watermark.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.True(t, first.Done)
	require.Len(t, first.Records, 1)
	require.Equal(t, "room-a", first.MessageCursors[0].ChannelID)
	require.Equal(t, uint64(1), first.MessageCursors[0].HW)

	cursorHead := validContinuousTestReference("f")
	resolver.boundaries = append([]backupartifact.ChannelBoundary(nil), first.MessageCursors...)
	frontier := backupcontract.StreamFrontier{
		Sequence: 1, CursorHead: &cursorHead, SourceCursor: first.NextCursor,
		SourceHighWatermark: first.NextPosition,
	}
	next := observeNextMessageCut(t, source, frontier)
	require.Equal(t, uint64(2), next.Position)
	second, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", CursorSequence: frontier.Sequence,
		CursorSourceCursor: frontier.SourceCursor, CursorHead: frontier.CursorHead,
		AfterCursor: frontier.SourceCursor, ThroughPosition: next.Position, ThroughCursor: next.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.True(t, second.Done)
	record, err := backupartifact.LoadMessageLogRecord(second.Records[0])
	require.NoError(t, err)
	require.Equal(t, "room-b", record.ChannelID)
	require.Equal(t, uint64(1), record.MessageSeq)
}

func TestMessageLogSourceInvalidationRetriesFromDurableFrontier(t *testing.T) {
	node := &fakeContinuousMessageNode{
		metas: []metadb.ChannelRuntimeMeta{
			validMessageRuntimeMeta("room-a"),
			validMessageRuntimeMeta("room-b"),
		},
		hw: 1,
	}
	source, err := backupinfra.NewMessageLogSource(node, &fakeMessageBoundaryResolver{})
	require.NoError(t, err)
	frontier := backupcontract.StreamFrontier{}

	firstCut, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	firstPage, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: firstCut.Position, ThroughCursor: firstCut.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	source.AcknowledgeSourcePage(17, backupartifact.SegmentStreamMessages, firstCut.CutCursor)

	secondCut, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	require.NotEqual(t, firstCut.CutCursor, secondCut.CutCursor)
	source.InvalidateSourceState(17)

	retriedCut, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	require.Equal(t, firstCut.CutCursor, retriedCut.CutCursor)
	retriedPage, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: retriedCut.Position, ThroughCursor: retriedCut.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	require.Equal(t, firstPage.Records, retriedPage.Records)
}

func TestMessageLogSourceAdvancesTransientSweepOnlyAfterPageAdmission(t *testing.T) {
	node := &fakeContinuousMessageNode{
		metas: []metadb.ChannelRuntimeMeta{
			validMessageRuntimeMeta("room-a"),
			validMessageRuntimeMeta("room-b"),
		},
		hw: 1,
	}
	source, err := backupinfra.NewMessageLogSource(node, &fakeMessageBoundaryResolver{})
	require.NoError(t, err)
	frontier := backupcontract.StreamFrontier{}
	firstCut, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	firstPage, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", ThroughPosition: firstCut.Position, ThroughCursor: firstCut.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)

	stillPinned, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	require.Equal(t, firstCut.CutCursor, stillPinned.CutCursor, "a read alone must not acknowledge admission")

	source.AcknowledgeSourcePage(17, backupartifact.SegmentStreamMessages, firstCut.CutCursor)
	secondCut, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", frontier)
	require.NoError(t, err)
	require.Equal(t, uint64(2), secondCut.Position)
	secondPage, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMessages,
		Generation: "slot-generation-1", AfterCursor: firstPage.NextCursor,
		ThroughPosition: secondCut.Position, ThroughCursor: secondCut.CutCursor,
		MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 16,
	})
	require.NoError(t, err)
	record, err := backupartifact.LoadMessageLogRecord(secondPage.Records[0])
	require.NoError(t, err)
	require.Equal(t, "room-b", record.ChannelID)
}

func observeNextMessageCut(
	t *testing.T,
	source *backupinfra.MessageLogSource,
	frontier backupcontract.StreamFrontier,
) runtimebackup.SourceWatermark {
	t.Helper()
	for attempt := 0; attempt < 4; attempt++ {
		watermark, err := source.HighWatermark(
			context.Background(), 17, "slot-generation-1", frontier,
		)
		require.NoError(t, err)
		if watermark.Position > frontier.SourceHighWatermark {
			return watermark
		}
	}
	t.Fatal("paged reconciliation did not find the next message cut")
	return runtimebackup.SourceWatermark{}
}

type fakeMessageBoundaryResolver struct {
	boundaries    []backupartifact.ChannelBoundary
	baseline      []backupartifact.ChannelBoundary
	last          backupinfra.MessageCursorResolveRequest
	baselineCalls int
}

func (r *fakeMessageBoundaryResolver) Resolve(_ context.Context, request backupinfra.MessageCursorResolveRequest) ([]backupartifact.ChannelBoundary, error) {
	r.last = request
	return append([]backupartifact.ChannelBoundary(nil), r.boundaries...), nil
}

func (r *fakeMessageBoundaryResolver) ResolveBaseline(
	_ context.Context,
	_ uint16,
	_ backupartifact.SegmentReference,
) (*backupinfra.ResolvedBaseline, error) {
	r.baselineCalls++
	return &backupinfra.ResolvedBaseline{
		Boundaries: append([]backupartifact.ChannelBoundary(nil), r.baseline...),
	}, nil
}

type fakeContinuousMessageNode struct {
	meta        metadb.ChannelRuntimeMeta
	metas       []metadb.ChannelRuntimeMeta
	hw          uint64
	hwByChannel map[string]uint64
	listCalls   int
	batchCalls  int
}

func (n *fakeContinuousMessageNode) ListBackupChannelRuntimeMetaPage(_ context.Context, _ uint16, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	n.listCalls++
	metas := n.metas
	if len(metas) == 0 {
		metas = []metadb.ChannelRuntimeMeta{n.meta}
	}
	start := 0
	for start < len(metas) {
		current := metadb.ChannelRuntimeMetaCursor{ChannelID: metas[start].ChannelID, ChannelType: metas[start].ChannelType}
		if current.ChannelID > after.ChannelID ||
			(current.ChannelID == after.ChannelID && current.ChannelType > after.ChannelType) {
			break
		}
		start++
	}
	if start == len(metas) {
		return nil, after, true, nil
	}
	end := min(start+limit, len(metas))
	page := append([]metadb.ChannelRuntimeMeta(nil), metas[start:end]...)
	next := metadb.ChannelRuntimeMetaCursor{
		ChannelID:   page[len(page)-1].ChannelID,
		ChannelType: page[len(page)-1].ChannelType,
	}
	return page, next, end == len(metas), nil
}

func (n *fakeContinuousMessageNode) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	metas := n.metas
	if len(metas) == 0 {
		metas = []metadb.ChannelRuntimeMeta{n.meta}
	}
	for _, meta := range metas {
		if meta.ChannelID == channelID && meta.ChannelType == channelType {
			return meta, nil
		}
	}
	return metadb.ChannelRuntimeMeta{}, fmt.Errorf("missing Channel runtime metadata")
}

func (n *fakeContinuousMessageNode) ObserveBackupMessageChannel(_ context.Context, request clusterpkg.BackupMessageChannelRequest) (clusterpkg.BackupMessageChannelBoundary, error) {
	hw := n.hw
	if n.hwByChannel != nil {
		hw = n.hwByChannel[request.ChannelID]
	}
	return clusterpkg.BackupMessageChannelBoundary{
		HashSlot: request.HashSlot, ChannelID: request.ChannelID, ChannelType: request.ChannelType,
		Epoch: request.ChannelEpoch, HW: hw, ObservedAtUnixMillis: 1_753_400_100_000,
	}, nil
}

func (n *fakeContinuousMessageNode) ObserveBackupMessageChannels(ctx context.Context, requests []clusterpkg.BackupMessageChannelRequest) ([]clusterpkg.BackupMessageChannelBoundary, error) {
	n.batchCalls++
	out := make([]clusterpkg.BackupMessageChannelBoundary, len(requests))
	for index, request := range requests {
		boundary, err := n.ObserveBackupMessageChannel(ctx, request)
		if err != nil {
			return nil, err
		}
		out[index] = boundary
	}
	return out, nil
}

func (n *fakeContinuousMessageNode) ReadBackupMessageLogPage(_ context.Context, request clusterpkg.BackupMessageLogPageRequest) (clusterpkg.BackupMessageLogPage, error) {
	page := clusterpkg.BackupMessageLogPage{
		Boundary: clusterpkg.BackupMessageChannelBoundary{
			HashSlot: request.Channel.HashSlot, ChannelID: request.Channel.ChannelID,
			ChannelType: request.Channel.ChannelType, Epoch: request.Channel.ChannelEpoch,
			HW: request.ThroughSeq, ObservedAtUnixMillis: 1_753_400_100_000,
		},
	}
	through := request.ThroughSeq
	if maxThrough := request.FromSeq + uint64(request.MaxRecords) - 1; maxThrough < through {
		through = maxThrough
	}
	for seq := request.FromSeq; seq <= through; seq++ {
		body, err := backupartifact.MarshalMessageLogRecord(backupartifact.MessageLogRecord{
			Kind: backupartifact.MessageLogRecordMessage, HashSlot: request.Channel.HashSlot,
			ChannelID: request.Channel.ChannelID, ChannelType: request.Channel.ChannelType,
			Epoch: request.Channel.ChannelEpoch, HW: request.ThroughSeq,
			MessageSeq: seq, MessageID: 100 + seq, Payload: []byte(fmt.Sprintf("message-%d", seq)),
		})
		if err != nil {
			return clusterpkg.BackupMessageLogPage{}, err
		}
		page.Records = append(page.Records, body)
		page.NextSeq = seq + 1
	}
	page.Done = page.NextSeq > request.ThroughSeq
	page.Boundary.HW = page.NextSeq - 1
	return page, nil
}

func validContinuousTestReference(char string) backupartifact.SegmentReference {
	value := ""
	for len(value) < 64 {
		value += char
	}
	return backupartifact.SegmentReference{
		SegmentID: value, CommitKey: "segments/" + value + "/commit.json",
		CommitSHA256: value, PlaintextBytes: 1,
	}
}

func validMessageRuntimeMeta(channelID string) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID: channelID, ChannelType: 2, ChannelEpoch: 7,
		LeaderEpoch: 3, Leader: 2, MinISR: 2,
	}
}
