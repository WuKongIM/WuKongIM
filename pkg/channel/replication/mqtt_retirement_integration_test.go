//go:build integration

package replication

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

// Consumer admission is controlled here; the real sequencer, wire codec and
// replica stores must preserve the explicit decision and reject business retries.
func verifyMQTTRetirementQuorum(t *testing.T, ctx context.Context, a Authority, runtimes map[ch.NodeID]*Runtime, factories map[ch.NodeID]*channelstore.MessageDBFactory) func(ch.NodeID) bool {
	t.Helper()
	st, err := factories[a.Leader].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	anchor, found, err := st.(channelstore.MQTTReplayAnchorReader).LoadMQTTReplayAnchor(ctx, 3)
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, st.Close())
	r := quorumlog.MQTTReplayRetirement{Anchor: anchor.Anchor, AnchorPosition: anchor.Manifest.LastOffset, AnchorDigest: anchor.Manifest.Digest}
	body, err := r.MarshalBinary()
	require.NoError(t, err)
	proposal := Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{29}, MQTTReplayRetirement: true, Records: []ch.Record{{ID: 2900, Epoch: 1, ServerTimestampMS: 29000, SyncOnce: true, Payload: body, SizeBytes: len(body)}}}
	receipt, err := runtimes[a.Leader].Log().Commit(ctx, proposal)
	require.NoError(t, err)
	again, err := runtimes[a.Leader].Log().Commit(ctx, proposal)
	require.NoError(t, err)
	require.Equal(t, receipt, again)
	wrong := proposal
	wrong.MQTTReplayRetirement = false
	_, err = runtimes[a.Leader].Log().Commit(ctx, wrong)
	require.ErrorIs(t, err, ch.ErrLogConflict)
	for _, activation := range []bool{false, true} {
		wrong = proposal
		wrong.MQTTSourceActivation, wrong.MQTTReplayAnchor = activation, !activation
		_, err = runtimes[a.Leader].Log().Commit(ctx, wrong)
		require.ErrorIs(t, err, ch.ErrInvalidConfig)
	}
	st, err = factories[a.Leader].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	require.NoError(t, st.StoreCheckpoint(ctx, ch.Checkpoint{HW: receipt.HW}))
	require.NoError(t, st.Close())
	require.NoError(t, runtimes[a.Leader].Log().(CommittedReplicaRefresher).RequestCommittedReplicaRefresh(ctx, a))
	check := func(node ch.NodeID) bool {
		st, err := factories[node].ChannelStore(a.Key, a.ChannelID)
		if err != nil {
			return false
		}
		defer st.Close()
		proof, found, err := st.(channelstore.MQTTReplayRetirementReader).LoadMQTTReplayRetirement(ctx, receipt.Last)
		return err == nil && found && proof.Retirement == r && proof.Manifest.CommandID == proposal.CommandID && proof.Manifest.Version == quorumlog.MQTTReplayRetirementProposalManifestVersion
	}
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		require.Eventually(t, func() bool { return check(node) }, 3*time.Second, time.Millisecond)
	}
	t.Log("mqtt_retirement_evidence: voters=3 learner=1 real_disk=true wire_codec=true explicit_control=true business_retry_rejected=true committed_journal=true consumer_admission=controlled physical_gc=false product_listener=false")
	return check
}
