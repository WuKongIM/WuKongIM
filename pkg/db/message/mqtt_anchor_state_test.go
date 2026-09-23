package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTReplayAnchorStateUsesCommittedPinnedJournal(t *testing.T) {
	ctx := context.Background()
	f := newReplayTransferFixture(t)
	m, records, a := replayAnchorProposal(t, f, 3)
	appendMQTTActivation(t, f.target, m, records, 4)
	state, err := f.target.log.ReadMQTTReplayAnchors(ctx, 4, m.CommandID)
	require.NoError(t, err)
	require.False(t, state.HasLatest)
	require.False(t, state.HasRequested)
	require.Equal(t, f.generation, state.Source.Generation)
	require.Equal(t, uint64(4), state.CommittedThrough)
	_, err = f.target.log.ReadMQTTReplayAnchors(ctx, 5, m.CommandID)
	require.Error(t, err, "caller cannot advance committed evidence")
	require.NoError(t, f.target.StoreCheckpointHWMonotonic(ctx, 5))
	state, err = f.target.log.ReadMQTTReplayAnchors(ctx, 5, m.CommandID)
	require.NoError(t, err)
	require.True(t, state.HasLatest)
	require.True(t, state.HasRequested)
	require.Equal(t, a, state.Latest.Anchor)
	require.Equal(t, state.Latest, state.Requested)
	// A bounded older view must exclude a newer committed journal.
	earlier, err := f.target.log.ReadMQTTReplayAnchors(ctx, 4, m.CommandID)
	require.NoError(t, err)
	require.False(t, earlier.HasLatest)
	require.False(t, earlier.HasRequested)
	// Command lookup must reject an ordinary proposal in the anchor domain.
	_, err = f.target.log.ReadMQTTReplayAnchors(ctx, 5, f.business.CommandID)
	require.Error(t, err)
	// Exact proof survives original-body reclamation, and missing journal fails.
	source, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	released := source
	released.Revision++
	released.CopiedThrough = 5
	released.ReceiptDigest = [32]byte{9}
	require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, source.Revision, released))
	_, err = f.target.log.TrimPrefixThrough(ctx, 5)
	require.NoError(t, err)
	state, err = f.target.log.ReadMQTTReplayAnchors(ctx, 5, m.CommandID)
	require.NoError(t, err)
	require.Equal(t, a, state.Requested.Anchor)
	batch := f.targetEngine.engine.NewBatch()
	defer batch.Close()
	require.NoError(t, batch.Delete(mqttReplayAnchorKey(f.target.log.key, 5)))
	require.NoError(t, batch.Commit(true))
	_, err = f.target.log.ReadMQTTReplayAnchors(ctx, 5, m.CommandID)
	require.Error(t, err)
	_, err = f.target.log.ReadMQTTReplayAnchors(ctx, 5, quorumlog.CommandID{})
	require.Error(t, err)
}
