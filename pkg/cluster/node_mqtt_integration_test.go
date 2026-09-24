//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// This verifies metadata consensus, not MQTT client behavior or owner isolation.
func TestMQTTMetadataThreeNodeAuthorityAndRecovery(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-metadata", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.HealthReport.Interval = 200 * time.Millisecond
		n, err := New(cfg)
		require.NoError(t, err)
		nodes = append(nodes, n)
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	key, err := slotproxy.MQTTSessionRoutingKey("main", "client")
	require.NoError(t, err)
	route := waitRouteKeyLeaderConverged(t, nodes, key)
	origin := firstNonLeaderNode(t, nodes, route.Leader)
	s := metadb.MQTTSession{Namespace: "main", ClientID: "client", UID: "alice", Generation: 1, Revision: 1, OwnerGeneration: 1, OwnerNodeID: origin.NodeID(), OwnerBootID: "boot", ConnectionID: 1, LeaseUntilMS: 5000, State: metadb.MQTTSessionActive, SessionExpirySec: 86400, ReceiveMaximum: 64, MaxPacketBytes: 1 << 20, NextPacketID: 1, NextDeliveryOrder: 1, QuotaMessages: 1000, QuotaBytes: 1 << 20, UpdatedAtMS: 1000}
	created, err := origin.ApplyMQTTLifecycle(ctx, metadb.MQTTLifecycleMutation{Event: metadb.MQTTLifecycleConnect, Session: s})
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, created.Status)
	query := metadb.MQTTRead{Kind: metadb.MQTTReadSession, Namespace: "main", ClientID: "client"}
	for _, n := range nodes {
		read, err := n.ReadMQTT(ctx, query)
		require.NoError(t, err)
		require.NotNil(t, read.Session)
		require.Equal(t, uint64(1), read.Session.Revision)
	}
	// Multiple obligations share one source job, while generations stay distinct.
	sourceQuery := metadb.MQTTRead{Kind: metadb.MQTTReadReplaySources, Limit: 1}
	var sourceOwners []metadb.MQTTBindingOwner
	var departing []metadb.MQTTSourceBinding
	for _, generation := range []string{"g1", "g2"} {
		owner := metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingChannel, ID: "2:" + key, Generation: generation}
		sourceOwners = append(sourceOwners, owner)
		for _, client := range []string{"a", "b"} {
			binding := metadb.MQTTSourceBinding{Key: metadb.MQTTSourceBindingKey{Owner: owner, Namespace: "main", ClientID: client, SessionGeneration: 1, SubscriptionGeneration: 2}, UID: "alice", Topic: "topic", Revision: 1, IntentRevision: 2, AuthorizationVersion: 1, OperationID: "subscribe", Stage: metadb.MQTTBindingPreparing, RecoveryAtMS: 1000, UpdatedAtMS: 1000}
			written, e := origin.CompareAndSwapMQTTSourceBinding(ctx, 0, binding)
			require.NoError(t, e)
			require.Equal(t, metadb.MQTTSessionCASApplied, written.Status)
			if generation == "g1" {
				departing = append(departing, binding)
			}
		}
	}
	checkSources := func(n *Node) {
		query := sourceQuery
		for i, owner := range sourceOwners {
			page, e := n.ReadMQTTRecovery(ctx, route.HashSlot, query)
			require.NoError(t, e)
			require.Equal(t, []metadb.MQTTBindingOwner{owner}, page.SourceOwners)
			require.Equal(t, i == len(sourceOwners)-1, page.Done)
			query.After = page.After
		}
	}
	for _, n := range nodes {
		checkSources(n)
	}
	// Controlled binding transitions exercise the catalog, not product removal
	// permission. The final tombstone must survive routing and disk reconstruction.
	for _, binding := range departing {
		binding.Revision, binding.Stage, binding.ProgressRevision, binding.ReleaseReason = 2, metadb.MQTTBindingRemoving, 3, metadb.MQTTBindingSessionEnded
		written, e := origin.CompareAndSwapMQTTSourceBinding(ctx, 1, binding)
		require.NoError(t, e)
		require.Equal(t, metadb.MQTTSessionCASApplied, written.Status)
		binding.Revision, binding.Stage, binding.RecoveryAtMS, binding.ProtectionRevision = 3, metadb.MQTTBindingRemoved, 0, 2
		written, e = origin.CompareAndSwapMQTTSourceBinding(ctx, 2, binding)
		require.NoError(t, e)
		require.Equal(t, metadb.MQTTSessionCASApplied, written.Status)
	}
	for _, n := range nodes {
		checkSources(n)
		active, e := n.ReadMQTTRecovery(ctx, route.HashSlot, metadb.MQTTRead{Kind: metadb.MQTTReadSourceOwners, Limit: 64})
		require.NoError(t, e)
		require.Equal(t, sourceOwners[1:], active.SourceOwners)
		floor, e := n.ReadMQTT(ctx, metadb.MQTTRead{Kind: metadb.MQTTReadSourceRetention, Owner: sourceOwners[0], Limit: 1})
		require.NoError(t, e)
		require.Empty(t, floor.Bindings)
		require.True(t, floor.Done)
	}
	transferSlotLeaderAndWait(t, nodes, route.SlotID, origin.NodeID())
	waitUntil(t, func() bool {
		for _, n := range nodes {
			r, e := n.RouteKey(key)
			if e != nil || r.Leader != origin.NodeID() {
				return false
			}
		}
		return true
	})
	victim := nodes[route.Leader-1]
	stopNodes(t, victim)
	// Stopping a node can also change Controller/other Slot leadership. The
	// earlier route observation is not proof that fresh barriers are ready now.
	var read metadb.MQTTReadResult
	require.Eventually(t, func() bool {
		probe, done := context.WithTimeout(ctx, 500*time.Millisecond)
		defer done()
		read, err = origin.ReadMQTT(probe, query)
		return err == nil
	}, 5*time.Second, 20*time.Millisecond, "the surviving metadata quorum must restore authoritative reads")
	updated := *read.Session
	updated.Revision = 2
	updated.LeaseUntilMS = 6000
	updated.UpdatedAtMS = 2000
	result, err := origin.CompareAndSwapMQTTSession(ctx, 1, updated)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, result.Status)
	// Reconstruct the stopped node from its exact durable directory.
	restarted, err := New(victim.cfg)
	require.NoError(t, err)
	nodes[victim.NodeID()-1] = restarted
	startNode(t, restarted)
	waitClusterReady(t, nodes...)
	read, err = restarted.ReadMQTT(ctx, query)
	require.NoError(t, err)
	require.Equal(t, &updated, read.Session)
	page, err := restarted.ReadMQTTRecovery(ctx, route.HashSlot, metadb.MQTTRead{Kind: metadb.MQTTReadSessionDeadlines, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []metadb.MQTTSession{updated}, page.Sessions)
	checkSources(restarted)
	// Preserve the current leader but remove its quorum. A cached successful
	// read above must not make either presence or absence authoritative now.
	for _, n := range nodes {
		if n.NodeID() != origin.NodeID() {
			stopNodes(t, n)
		}
	}
	blocked, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	_, err = origin.ReadMQTT(blocked, query)
	done()
	require.Error(t, err)
	blocked, done = context.WithTimeout(context.Background(), 300*time.Millisecond)
	updated.Revision = 3
	updated.LeaseUntilMS = 7000
	updated.UpdatedAtMS = 3000
	_, err = origin.CompareAndSwapMQTTSession(blocked, 2, updated)
	done()
	require.Error(t, err)
	blocked, done = context.WithTimeout(context.Background(), 300*time.Millisecond)
	_, err = origin.ReadMQTTRecovery(blocked, route.HashSlot, sourceQuery)
	done()
	require.Error(t, err)
	t.Log("mqtt_source_discovery_evidence: nodes=3 hash_slots=256 tcp=true disk=true distinct_sources=true paginated=true leader_transfer=true restart=true isolated_rejected=true removed_source_retained=true removed_consumer_excluded=true binding_release=controlled scheduler=false")
	t.Logf("MQTT metadata verified: hash_slots=256 physical_slots=2 replicas=3 hash_slot=%d slot=%d original_leader=%d new_leader=%d revision=2; restart preserved state; isolated reads and writes rejected", route.HashSlot, route.SlotID, route.Leader, origin.NodeID())
}
