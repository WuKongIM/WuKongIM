package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func repairPlanFixture(t *testing.T) *replayTransferFixture {
	t.Helper()
	f := newReplayTransferFixture(t)
	first, records, _ := replayAnchorProposal(t, f, 3)
	for _, s := range []*ChannelStore{f.source, f.target} {
		appendMQTTActivation(t, s, first, records, 5)
	}
	r, err := compatibilityRecordFromRow(messageRow{MessageID: 800, ChannelID: "activation", ChannelType: 1, FromUID: "bob", ServerTimestampMS: 6000, Payload: []byte("next business")})
	require.NoError(t, err)
	r.Epoch = 1
	next := sealCompatProposalManifest(t, DurableProposalManifest{Version: 1, ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 2, CommandID: quorumlog.CommandID{4}, BaseOffset: 5, LastOffset: 6, PreviousIndex: 5, PreviousTerm: 2, PreviousDigest: first.Digest}, []channel.Record{r})
	for _, s := range []*ChannelStore{f.source, f.target} {
		appendMQTTActivation(t, s, next, []channel.Record{r}, 6)
	}
	_, err = f.source.log.CopyMQTTReplaySource(context.Background(), f.generation, 5, 6, replayTransferBudget)
	require.NoError(t, err)
	prefix, _, err := f.source.log.LoadMQTTReplayState(context.Background())
	require.NoError(t, err)
	a := quorumlog.MQTTReplayAnchor{SourceCommand: f.activation.CommandID, StartAfter: prefix.StartAfter, Through: prefix.Through, TotalBytes: prefix.TotalBytes, TotalStoredBytes: prefix.TotalStoredBytes, Digest: prefix.Digest}
	body, err := a.MarshalBinary()
	require.NoError(t, err)
	r, err = compatibilityRecordFromRow(messageRow{MessageID: 905, ChannelID: "activation", ChannelType: 1, FramerFlags: 4, ServerTimestampMS: 6001, Payload: body})
	require.NoError(t, err)
	r.Epoch = 1
	last := sealCompatProposalManifest(t, DurableProposalManifest{Version: 5, ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 2, CommandID: quorumlog.CommandID{5}, BaseOffset: 6, LastOffset: 7, PreviousIndex: 6, PreviousTerm: 2, PreviousDigest: next.Digest}, []channel.Record{r})
	for _, s := range []*ChannelStore{f.source, f.target} {
		appendMQTTActivation(t, s, last, []channel.Record{r}, 7)
	}
	return f
}

func TestMQTTRepairPlanSelectsBoundedIntervalsAfterTrimAndRestart(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	for _, s := range []*ChannelStore{f.source, f.target} {
		original, _, err := s.log.LoadMQTTSourceState(ctx)
		require.NoError(t, err)
		released := original
		released.Revision = 2
		released.CopiedThrough = 6
		released.ReceiptDigest = [32]byte{9}
		require.NoError(t, s.log.ApplyMQTTSourceState(ctx, original.Revision, released))
		trimmed, err := s.log.TrimPrefixThrough(ctx, 6)
		require.NoError(t, err)
		require.Equal(t, 6, trimmed.Deleted)
	}
	before, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	first, err := f.target.PlanMQTTReplayRepair(ctx, f.generation, 7, 0, 1)
	require.NoError(t, err)
	require.True(t, first.HasNext)
	require.False(t, first.Complete)
	require.Equal(t, uint64(0), first.Current.Through)
	require.Equal(t, uint64(5), first.Next.Manifest.LastOffset)
	require.Equal(t, uint64(7), first.Target.Manifest.LastOffset)
	p, err := f.source.ExportMQTTReplayAnchor(ctx, first.Next.Manifest.LastOffset, first.Current.Through+1, replayTransferBudget)
	require.NoError(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, first.Next.Manifest.LastOffset, p)
	require.NoError(t, err)
	bounded, err := f.target.PlanMQTTReplayRepair(ctx, f.generation, 7, 0, 1)
	require.NoError(t, err)
	require.False(t, bounded.HasNext)
	require.False(t, bounded.Complete)
	require.Equal(t, uint64(5), bounded.ScanAfter)
	require.Equal(t, uint64(4), bounded.Current.Through)
	// Continuation survives process storage reopen and never represents a write.
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	next, err := f.target.PlanMQTTReplayRepair(ctx, f.generation, 7, bounded.ScanAfter, 1)
	require.NoError(t, err)
	require.True(t, next.HasNext)
	require.Equal(t, uint64(7), next.Next.Manifest.LastOffset)
	require.Equal(t, uint64(4), next.Current.Through)
	p, err = f.source.ExportMQTTReplayAnchor(ctx, 7, next.Current.Through+1, replayTransferBudget)
	require.NoError(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 7, p)
	require.NoError(t, err)
	done, err := f.target.PlanMQTTReplayRepair(ctx, f.generation, 7, 0, 1)
	require.NoError(t, err)
	require.True(t, done.Complete)
	require.False(t, done.HasNext)
	require.Zero(t, done.Next)
	require.Zero(t, done.ScanAfter)
	require.Equal(t, uint64(6), done.Current.Through)
	earlier, err := f.target.PlanMQTTReplayRepair(ctx, f.generation, 5, 0, 1)
	require.NoError(t, err)
	require.True(t, earlier.Complete)
	require.Equal(t, uint64(5), earlier.Target.Manifest.LastOffset)
	require.Equal(t, uint64(6), earlier.Current.Through)
	after, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	cp, err := f.target.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, uint64(7), cp.HW)
}

func TestMQTTRepairPlanRejectsCursorThatSkipsMissingContent(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	for _, after := range []uint64{1, 4, 5, 6, 7, 8, ^uint64(0)} {
		_, err := f.target.log.PlanMQTTReplayRepair(ctx, f.generation, 7, after, 1)
		require.Error(t, err)
	}
	_, err := f.target.log.CopyMQTTReplaySource(ctx, f.generation, 1, 2, replayTransferBudget)
	require.NoError(t, err)
	partial, err := f.target.log.PlanMQTTReplayRepair(ctx, f.generation, 7, 0, 64)
	require.NoError(t, err)
	require.True(t, partial.HasNext)
	require.Equal(t, uint64(2), partial.Current.Through)
	require.Equal(t, uint64(4), partial.Next.Anchor.Through)
	p, err := f.source.ExportMQTTReplayAnchor(ctx, 5, 3, replayTransferBudget)
	require.NoError(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 5, p)
	require.NoError(t, err)
	_, err = f.target.log.PlanMQTTReplayRepair(ctx, f.generation, 7, 7, 64)
	require.Error(t, err, "uncovered target cannot be skipped by a cursor")
	plan, err := f.target.log.PlanMQTTReplayRepair(ctx, f.generation, 7, 5, 64)
	require.NoError(t, err)
	require.True(t, plan.HasNext)
	require.Equal(t, uint64(7), plan.Next.Manifest.LastOffset)
}

func TestMQTTRepairPlanRejectsBrokenLocalEvidenceAndBudgets(t *testing.T) {
	for _, fault := range []string{"target", "checkpoint", "activation", "target_entry", "pending", "generation", "tail", "meter", "covered_digest", "cursor"} {
		t.Run(fault, func(t *testing.T) {
			f := repairPlanFixture(t)
			ctx := context.Background()
			key := f.target.log.key
			gen, after := f.generation, uint64(0)
			switch fault {
			case "target":
				deletePhysicalTestKey(t, f.targetEngine, mqttReplayAnchorKey(key, 7))
			case "checkpoint":
				deletePhysicalTestKey(t, f.targetEngine, encodeCheckpointKey(key))
			case "activation":
				deletePhysicalTestKey(t, f.targetEngine, mqttActivationKey(key))
			case "target_entry":
				deletePhysicalTestKey(t, f.targetEngine, encodeEntryIdentityKey(key, 7))
			case "pending":
				setPhysicalTestValue(t, f.targetEngine, encodeCheckpointKey(key), encodeCheckpoint(Checkpoint{HW: 6}))
			case "generation":
				gen = quorumlog.MQTTSourceGeneration(quorumlog.CommandID{9})
			case "tail", "meter", "covered_digest", "cursor":
				_, err := f.target.log.CopyMQTTReplaySource(ctx, f.generation, 1, 6, replayTransferBudget)
				require.NoError(t, err)
				switch fault {
				case "tail":
					deletePhysicalTestKey(t, f.targetEngine, mqttReplayRowKey(key, gen, 6))
				case "meter":
					deletePhysicalTestKey(t, f.targetEngine, mqttReplayMeterKey(key, gen, 6))
				case "cursor":
					after = 6
				case "covered_digest":
					state, _, err := f.target.log.LoadMQTTReplayState(ctx)
					require.NoError(t, err)
					// A different covered target must not become completion just because the
					// durable frontier is beyond its position.
					state.Digest[0]++
					setPhysicalTestValue(t, f.targetEngine, mqttReplayStateKey(key), encodeMQTTReplayState(key, state))
				}
			}
			p, err := f.target.log.PlanMQTTReplayRepair(ctx, gen, 7, after, 64)
			require.Error(t, err)
			require.Zero(t, p)
		})
	}
	f := repairPlanFixture(t)
	for _, limit := range []int{-1, 0, 65} {
		_, err := f.target.log.PlanMQTTReplayRepair(context.Background(), f.generation, 7, 0, limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	_, err := f.target.log.PlanMQTTReplayRepair(context.Background(), f.generation, 0, 0, 1)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = f.target.log.PlanMQTTReplayRepair(ctx, f.generation, 7, 0, 1)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, f.target.Close())
	_, err = f.target.PlanMQTTReplayRepair(context.Background(), f.generation, 7, 0, 1)
	require.Error(t, err)
}
