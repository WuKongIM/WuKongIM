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
	read, err := origin.ReadMQTT(ctx, query)
	require.NoError(t, err)
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
	t.Logf("MQTT metadata verified: hash_slots=256 physical_slots=2 replicas=3 hash_slot=%d slot=%d original_leader=%d new_leader=%d revision=2; restart preserved state; isolated reads and writes rejected", route.HashSlot, route.SlotID, route.Leader, origin.NodeID())
}
