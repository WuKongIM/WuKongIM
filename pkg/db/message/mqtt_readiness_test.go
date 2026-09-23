package message

import (
	"context"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

func TestMQTTReplayReadinessRequiresLocalContentAtCommittedAnchor(t *testing.T) {
	ctx := context.Background()
	f := repairPlanFixture(t)
	missing, err := f.target.ReadMQTTReplayReadiness(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, uint64(7), missing.CommittedThrough)
	require.Equal(t, uint64(7), missing.AnchorPosition)
	require.Equal(t, uint64(6), missing.RequiredThrough)
	require.False(t, missing.Covered)
	donor, err := f.source.ReadMQTTReplayReadiness(ctx, 7)
	require.NoError(t, err)
	require.True(t, donor.Covered)
	older, err := f.source.ReadMQTTReplayReadiness(ctx, 5)
	require.NoError(t, err)
	require.True(t, older.Covered)
	require.Equal(t, uint64(4), older.RequiredThrough)
	first, err := f.source.ExportMQTTReplayAnchor(ctx, 5, 1, replayTransferBudget)
	require.NoError(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 5, first)
	require.NoError(t, err)
	partial, err := f.target.ReadMQTTReplayReadiness(ctx, 7)
	require.NoError(t, err)
	require.False(t, partial.Covered)
	prefix, err := f.target.ReadMQTTReplayReadiness(ctx, 5)
	require.NoError(t, err)
	require.True(t, prefix.Covered)
	next, err := f.source.ExportMQTTReplayAnchor(ctx, 7, 5, replayTransferBudget)
	require.NoError(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 7, next)
	require.NoError(t, err)
	before, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	complete, err := f.target.ReadMQTTReplayReadiness(ctx, 7)
	require.NoError(t, err)
	require.True(t, complete.Covered)
	after, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	released := before
	released.Revision++
	released.CopiedThrough = 6
	released.ReceiptDigest = [32]byte{9}
	require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, before.Revision, released))
	_, err = f.target.log.TrimPrefixThrough(ctx, 6)
	require.NoError(t, err)
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	restored, err := f.target.ReadMQTTReplayReadiness(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, complete, restored)
	_, err = f.target.ReadMQTTReplayReadiness(ctx, 8)
	require.Error(t, err)
}

func TestMQTTReplayReadinessPreservesNativeAndPendingFrontiers(t *testing.T) {
	db, err := Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	s := mustForChannel(t, db, "native:1", channel.ChannelID{ID: "native", Type: 1})
	defer s.Close()
	empty, err := s.ReadMQTTReplayReadiness(context.Background(), 0)
	require.NoError(t, err)
	require.True(t, empty.Covered)
	require.Zero(t, empty.AnchorPosition)
	_, err = s.ReadMQTTReplayReadiness(context.Background(), 1)
	require.Error(t, err)
	f := newReplayTransferFixture(t)
	m, records, _ := replayAnchorProposal(t, f, 3)
	appendMQTTActivation(t, f.target, m, records, 4)
	pending, err := f.target.ReadMQTTReplayReadiness(context.Background(), 4)
	require.NoError(t, err)
	require.True(t, pending.Covered)
	require.Zero(t, pending.AnchorPosition)
	require.NoError(t, f.target.StoreCheckpointHWMonotonic(context.Background(), 5))
	committed, err := f.target.ReadMQTTReplayReadiness(context.Background(), 5)
	require.NoError(t, err)
	require.False(t, committed.Covered)
}

func TestMQTTReplayReadinessRejectsBrokenProofsAndCancellation(t *testing.T) {
	for _, mode := range []string{"checkpoint", "activation", "journal", "entry", "meter", "tail", "release_ahead", "cancel", "closed"} {
		t.Run(mode, func(t *testing.T) {
			f := repairPlanFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			position := uint64(7)
			switch mode {
			case "checkpoint", "activation", "journal", "entry", "meter", "tail":
				batch := f.sourceEngine.engine.NewBatch()
				defer batch.Close()
				var key []byte
				switch mode {
				case "checkpoint":
					key = encodeCheckpointKey(f.source.log.key)
				case "activation":
					key = mqttActivationKey(f.source.log.key)
				case "journal":
					key = mqttReplayAnchorKey(f.source.log.key, 7)
				case "entry":
					key = encodeEntryIdentityKey(f.source.log.key, 7)
				case "meter":
					position = 5
					key = mqttReplayMeterKey(f.source.log.key, f.generation, 4)
				case "tail":
					key = mqttReplayRowKey(f.source.log.key, f.generation, 6)
				}
				require.NoError(t, batch.Delete(key))
				require.NoError(t, batch.Commit(true))
			case "release_ahead":
				current, _, err := f.source.log.LoadMQTTSourceState(ctx)
				require.NoError(t, err)
				next := current
				next.Revision++
				next.CopiedThrough = 7
				next.ReceiptDigest = [32]byte{9}
				require.NoError(t, f.source.log.ApplyMQTTSourceState(ctx, current.Revision, next))
			case "cancel":
				cancel()
			case "closed":
				require.NoError(t, f.source.Close())
			}
			result, err := f.source.ReadMQTTReplayReadiness(ctx, position)
			if mode == "journal" {
				require.True(t, err != nil || !result.Covered, "missing latest journal must not claim the captured tail is complete")
			} else {
				require.Error(t, err)
			}
			if mode == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
			}
			if mode == "closed" {
				require.ErrorIs(t, err, channel.ErrClosed)
			}
		})
	}
}
