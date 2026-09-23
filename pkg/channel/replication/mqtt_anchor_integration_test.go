//go:build integration

package replication

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestMQTTReplayAnchorQuorumRestartRecoveryAndLearner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer)}
	paths := map[ch.NodeID]string{}
	runtimes := map[ch.NodeID]*Runtime{}
	factories := map[ch.NodeID]*channelstore.MessageDBFactory{}
	stores := map[ch.NodeID]ReplicaStore{}
	open := func() {
		for _, node := range []ch.NodeID{1, 2, 3, 4} {
			if paths[node] == "" {
				paths[node] = t.TempDir()
			}
			factory := channelstore.NewMessageDBFactory(paths[node])
			st, e := NewStoreAdapter(StoreAdapterConfig{Factory: factory, MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
			require.NoError(t, e)
			runtime, e := NewRuntime(RuntimeConfig{LocalNode: node, Store: st, Link: mqttActivationWireLink{runtimeTestLink{from: node, router: router}}, Goroutines: goruntimeregistry.New()})
			require.NoError(t, e)
			factories[node], stores[node], runtimes[node] = factory, st, runtime
			router.register(node, runtime.ExchangeServer())
		}
	}
	closeAll := func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		for n, r := range runtimes {
			require.NoError(t, r.Close(closeCtx))
			delete(runtimes, n)
		}
		for n, f := range factories {
			require.NoError(t, f.Close())
			delete(factories, n)
		}
	}
	t.Cleanup(closeAll)
	open()
	a := Authority{Key: "1:mqtt-anchor", ChannelID: ch.ChannelID{ID: "mqtt-anchor", Type: 1}, ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Learners: []ch.NodeID{4}, WriteQuorum: 2}
	_, err := runtimes[1].Log().Install(ctx, a)
	require.NoError(t, err)
	source := Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{1}, MQTTSourceActivation: true, Records: []ch.Record{{ID: 100, Epoch: 1, ServerTimestampMS: 1000, SyncOnce: true, Payload: []byte(quorumlog.MQTTSourceActivationPayload), SizeBytes: len(quorumlog.MQTTSourceActivationPayload)}}}
	_, err = runtimes[1].Log().Commit(ctx, source)
	require.NoError(t, err)
	_, err = runtimes[1].Log().Commit(ctx, Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{2}, Records: []ch.Record{{ID: 200, Epoch: 1, FromUID: "sender", Payload: []byte("message"), SizeBytes: 7, ServerTimestampMS: 2000}}})
	require.NoError(t, err)
	st, err := factories[1].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	require.NoError(t, st.StoreCheckpoint(ctx, ch.Checkpoint{HW: 2}))
	page, err := st.(channelstore.MQTTReplayPreparer).PrepareMQTTReplay(ctx, ch.MQTTReplayRange{Generation: quorumlog.MQTTSourceGeneration(source.CommandID), From: 1, Through: 2, Limit: 256, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.NoError(t, st.Close())
	anchor := quorumlog.MQTTReplayAnchor{SourceCommand: source.CommandID, StartAfter: page.After.StartAfter, Through: page.After.Through, TotalBytes: page.After.TotalBytes, TotalStoredBytes: page.After.TotalStoredBytes, Digest: page.After.Digest}
	payload, err := anchor.MarshalBinary()
	require.NoError(t, err)
	proposal := Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{3}, MQTTReplayAnchor: true, Records: []ch.Record{{ID: 300, Epoch: 1, ServerTimestampMS: 3000, SyncOnce: true, Payload: payload, SizeBytes: len(payload)}}}
	receipt, err := runtimes[1].Log().Commit(ctx, proposal)
	require.NoError(t, err)
	require.Equal(t, uint64(3), receipt.HW)
	wrong := proposal
	wrong.MQTTReplayAnchor = false
	_, err = runtimes[1].Log().Commit(ctx, wrong)
	require.ErrorIs(t, err, ch.ErrLogConflict)
	wrong = proposal
	wrong.MQTTSourceActivation = true
	_, err = runtimes[1].Log().Commit(ctx, wrong)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	st, err = factories[1].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	require.NoError(t, st.StoreCheckpoint(ctx, ch.Checkpoint{HW: receipt.HW}))
	require.NoError(t, st.Close())
	require.NoError(t, runtimes[1].Log().(CommittedReplicaRefresher).RequestCommittedReplicaRefresh(ctx, a))
	check := func(node ch.NodeID) bool {
		handle, e := factories[node].ChannelStore(a.Key, a.ChannelID)
		if e != nil {
			return false
		}
		defer handle.Close()
		proof, found, e := handle.(channelstore.MQTTReplayAnchorReader).LoadMQTTReplayAnchor(ctx, 3)
		return e == nil && found && proof.Anchor == anchor && proof.Manifest.CommandID == proposal.CommandID
	}
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		require.Eventually(t, func() bool { return check(node) }, 3*time.Second, time.Millisecond, "replica %d must retain its committed anchor journal", node)
	}
	// The native quorum transfer installs independent journals, but does not
	// imply shared-content readiness. A learner verifies the donor's complete
	// page against its own committed journal before retaining replay content.
	rangeReq := ch.MQTTReplayRange{Generation: page.After.Generation, From: 1, Through: 2, Limit: 256, MaxBytes: 1 << 20}
	donor, err := factories[1].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	transfer, err := donor.(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, 3, rangeReq)
	require.NoError(t, err)
	require.Equal(t, page, transfer)
	require.NoError(t, donor.Close())
	learner, err := factories[4].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	repair := learner.(channelstore.MQTTReplayAnchorTransfer)
	_, err = repair.ExportMQTTReplayAnchor(ctx, 3, rangeReq)
	require.Error(t, err, "a replicated journal alone is not shared content")
	beforeRepair, err := learner.Load(ctx)
	require.NoError(t, err)
	repaired, err := repair.ImportMQTTReplayAnchor(ctx, 3, transfer)
	require.NoError(t, err)
	require.Equal(t, page.After, repaired)
	afterRepair, err := learner.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeRepair, afterRepair, "content import must not publish a log frontier")
	require.NoError(t, learner.Close())
	closeAll()
	open()
	learner, err = factories[4].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	transfer, err = learner.(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, 3, rangeReq)
	require.NoError(t, err)
	require.Equal(t, page, transfer, "accepted content and its proof survive restart")
	require.NoError(t, learner.Close())
	a.Leader = 2
	a.ID.LeaderTerm++
	installed, err := runtimes[2].Log().Install(ctx, a)
	require.NoError(t, err)
	require.GreaterOrEqual(t, installed.HW, uint64(3))
	require.True(t, check(2))
	retirementCheck := verifyMQTTRetirementQuorum(t, ctx, a, runtimes, factories)
	closeAll()
	open()
	a.ID.LeaderTerm++
	_, err = runtimes[2].Log().Install(ctx, a)
	require.NoError(t, err)
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		require.Eventually(t, func() bool { return retirementCheck(node) }, 3*time.Second, time.Millisecond)
	}
	for _, node := range []ch.NodeID{1, 3, 4} {
		router.register(node, nil)
	}
	blocked, done := context.WithTimeout(ctx, 100*time.Millisecond)
	defer done()
	proposal.Expected = a.ID
	proposal.CommandID = ch.CommandID{4}
	proposal.Records[0].ID = 400
	failed, err := runtimes[2].Log().Commit(blocked, proposal)
	require.Error(t, err)
	require.Zero(t, failed.HW)
	t.Log("mqtt_anchor_evidence: voters=3 learner=1 real_disk=true wire_codec=true committed_journal=true independent_learner_content_import=true restart=true authority_recovery=true absent_quorum_rejected=true receipt_admission=false source_release=false product_listener=false")
}
