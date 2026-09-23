package store

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMessageDBAnchorRepairUsesReceiverProofAndPreservesContent(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "anchor-repair", Type: 1}
	var stores []ChannelStore
	for i := 0; i < 2; i++ {
		factory := NewMessageDBFactory(t.TempDir())
		t.Cleanup(func() { require.NoError(t, factory.Close()) })
		s, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		stores = append(stores, s)
	}
	appendProposal := func(version uint16, command byte, previous ch.ProposalManifest, records []ch.Record, hw uint64) ch.ProposalManifest {
		t.Helper()
		m, _, valid := ch.SealProposalManifest(ch.ProposalManifest{Version: version, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1,
			CommandID: ch.CommandID{command}, BaseOffset: previous.LastOffset, LastOffset: previous.LastOffset + uint64(len(records)),
			PreviousIndex: previous.LastOffset, PreviousTerm: previous.LeaderTerm, PreviousDigest: previous.Digest}, records)
		require.True(t, valid)
		for _, s := range stores {
			_, err := s.AppendLeader(ctx, AppendLeaderRequest{Records: records, Proposal: m, ExpectedBaseOffset: previous.LastOffset, ExactBaseOffset: true, Committed: hw})
			require.NoError(t, err)
		}
		return m
	}
	activation := appendProposal(quorumlog.MQTTSourceProposalManifestVersion, 1, ch.ProposalManifest{}, []ch.Record{{ID: 1, Epoch: 1, SyncOnce: true,
		ServerTimestampMS: 100, Payload: []byte(quorumlog.MQTTSourceActivationPayload)}}, 1)
	business := appendProposal(ch.ProposalManifestVersion, 2, activation, []ch.Record{{ID: 2, Epoch: 1, FromUID: "sender", ClientMsgNo: "business", RedDot: true,
		ServerTimestampMS: 101, Payload: []byte("original body")}}, 2)
	rangeReq := ch.MQTTReplayRange{Generation: quorumlog.MQTTSourceGeneration(activation.CommandID), From: 1, Through: 2, Limit: 256, MaxBytes: 1 << 20}
	page, err := stores[0].(MQTTReplayPreparer).PrepareMQTTReplay(ctx, rangeReq)
	require.NoError(t, err)
	require.True(t, page.ValidFor(rangeReq))
	a := quorumlog.MQTTReplayAnchor{SourceCommand: activation.CommandID, StartAfter: page.After.StartAfter, Through: page.After.Through,
		TotalBytes: page.After.TotalBytes, TotalStoredBytes: page.After.TotalStoredBytes, Digest: page.After.Digest}
	payload, err := a.MarshalBinary()
	require.NoError(t, err)
	appendProposal(quorumlog.MQTTReplayAnchorProposalManifestVersion, 3, business, []ch.Record{{ID: 3, Epoch: 1, SyncOnce: true, ServerTimestampMS: 102, Payload: payload}}, 2)
	donor, ok := stores[0].(MQTTReplayAnchorTransfer)
	require.True(t, ok)
	target, ok := stores[1].(MQTTReplayAnchorTransfer)
	require.True(t, ok)
	_, err = donor.ExportMQTTReplayAnchor(ctx, 3, rangeReq)
	require.Error(t, err, "pending donor proof cannot authorize an export")
	require.NoError(t, stores[0].StoreCheckpoint(ctx, ch.Checkpoint{HW: 3}))
	transferred, err := donor.ExportMQTTReplayAnchor(ctx, 3, rangeReq)
	require.NoError(t, err)
	require.Equal(t, page, transferred)
	_, err = target.ImportMQTTReplayAnchor(ctx, 3, transferred)
	require.Error(t, err, "donor commitment cannot replace receiver commitment")
	require.NoError(t, stores[1].StoreCheckpoint(ctx, ch.Checkpoint{HW: 3}))
	selector, ok := stores[1].(MQTTReplayRetirementSelector)
	require.True(t, ok)
	retirementScan := ch.MQTTReplayRetirementScan{Generation: rangeReq.Generation, CapturedAnchor: 3, Through: 2, Limit: 1}
	selection, err := selector.SelectMQTTReplayRetirementAnchor(ctx, retirementScan)
	require.NoError(t, err)
	require.True(t, selection.ValidFor(retirementScan))
	require.True(t, selection.HasCandidate, "committed proof suffices without local replay content")
	require.Equal(t, a, selection.Candidate.Anchor)
	below := retirementScan
	below.Through = 1
	selection, err = selector.SelectMQTTReplayRetirementAnchor(ctx, below)
	require.NoError(t, err)
	require.True(t, selection.ValidFor(below))
	require.True(t, selection.Done)
	require.False(t, selection.HasCandidate)
	badRetirementScan := retirementScan
	badRetirementScan.Limit = 65
	_, err = selector.SelectMQTTReplayRetirementAnchor(ctx, badRetirementScan)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	readiness, ok := stores[1].(MQTTReplayReadinessReader)
	require.True(t, ok)
	lagging, err := readiness.ReadMQTTReplayReadiness(ctx, 3)
	require.NoError(t, err)
	require.True(t, lagging.ValidFor(3))
	require.False(t, lagging.Covered)
	release, ok := stores[1].(MQTTSourceReleaser)
	require.True(t, ok)
	require.Error(t, release.ReleaseMQTTSourceAtAnchor(ctx, rangeReq.Generation, 3), "anchor without content cannot release originals")
	planner, ok := stores[1].(MQTTReplayRepairPlanner)
	require.True(t, ok)
	scan := ch.MQTTReplayRepairScan{Generation: rangeReq.Generation, TargetAnchor: 3, Limit: 1}
	plan, err := planner.PlanMQTTReplayRepair(ctx, scan)
	require.NoError(t, err)
	require.True(t, plan.ValidFor(scan))
	require.True(t, plan.HasNext)
	selected, more, err := plan.NextRange()
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, uint64(1), selected.From)
	require.Equal(t, uint64(2), selected.Through)
	before, err := stores[1].Load(ctx)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		got, err := target.ImportMQTTReplayAnchor(ctx, 3, transferred)
		require.NoError(t, err)
		require.Equal(t, page.After, got)
	}
	complete, err := planner.PlanMQTTReplayRepair(ctx, scan)
	require.NoError(t, err)
	require.True(t, complete.ValidFor(scan))
	require.True(t, complete.Complete)
	require.Equal(t, page.After, complete.Current)
	after, err := stores[1].Load(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	for range 2 {
		require.NoError(t, release.ReleaseMQTTSourceAtAnchor(ctx, rangeReq.Generation, 3))
	}
	_, err = stores[1].AdoptRetentionBoundary(ctx, 3, ch.RetentionCursorCommitted)
	require.NoError(t, err)
	trim, err := stores[1].TrimMessagesThrough(ctx, 3, RetentionTrimOptions{MaxMessages: 256, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Equal(t, 2, trim.Deleted)
	state, err := stores[1].LoadRetentionState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.PhysicalRetentionThroughSeq)
	ready, err := readiness.ReadMQTTReplayReadiness(ctx, 3)
	require.NoError(t, err)
	require.True(t, ready.ValidFor(3))
	require.True(t, ready.Covered)
	require.Equal(t, uint64(2), ready.RequiredThrough)
	recovered, err := target.ExportMQTTReplayAnchor(ctx, 3, rangeReq)
	require.NoError(t, err)
	require.Equal(t, page, recovered)
	clear(recovered.Records[0].Content)
	again, err := target.ExportMQTTReplayAnchor(ctx, 3, rangeReq)
	require.NoError(t, err)
	require.Equal(t, page, again, "returned bytes must not alias durable content")
	for _, change := range []string{"generation", "through", "limit", "bytes"} {
		bad := rangeReq
		switch change {
		case "generation":
			bad.Generation = quorumlog.MQTTSourceGeneration(ch.CommandID{8})
		case "through":
			bad.Through++
		case "limit":
			bad.Limit = 257
		case "bytes":
			bad.MaxBytes = 16<<20 + 1
		}
		got, err := donor.ExportMQTTReplayAnchor(ctx, 3, bad)
		require.Error(t, err, change)
		require.Empty(t, got.Records)
	}
	bad := transferred
	bad.Records = make([]ch.MQTTReplayRecord, 257)
	_, err = target.ImportMQTTReplayAnchor(ctx, 3, bad)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	badScan := scan
	badScan.Limit = 65
	_, err = planner.PlanMQTTReplayRepair(ctx, badScan)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.NoError(t, stores[1].Close())
	_, err = selector.SelectMQTTReplayRetirementAnchor(ctx, retirementScan)
	require.ErrorIs(t, err, ch.ErrClosed)
	require.ErrorIs(t, release.ReleaseMQTTSourceAtAnchor(ctx, rangeReq.Generation, 3), ch.ErrClosed)
	_, err = planner.PlanMQTTReplayRepair(ctx, scan)
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = target.ImportMQTTReplayAnchor(ctx, 3, transferred)
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = target.ExportMQTTReplayAnchor(ctx, 3, rangeReq)
	require.ErrorIs(t, err, ch.ErrClosed)
}
