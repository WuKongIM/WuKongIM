//go:build integration

package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// This exercises real metadata consensus and reconstruction. The lifetime
// decisions are controlled inputs; automatic reclamation scheduling is separate.
func TestMQTTReclamationThreeNodePartialPageRestart(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-reclamation", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.HealthReport.Interval = 200 * time.Millisecond
		n, e := New(cfg)
		require.NoError(t, e)
		nodes = append(nodes, n)
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	key, e := slotproxy.MQTTSessionRoutingKey("main", "cleanup")
	require.NoError(t, e)
	route := waitRouteKeyLeaderConverged(t, nodes, key)
	origin := firstNonLeaderNode(t, nodes, route.Leader)
	s := metadb.MQTTSession{Namespace: "main", ClientID: "cleanup", UID: "alice", Generation: 1, Revision: 1, OwnerGeneration: 1, OwnerNodeID: origin.NodeID(), OwnerBootID: "boot", ConnectionID: 1, LeaseUntilMS: 9000, State: metadb.MQTTSessionActive, SessionExpirySec: 86400, ReceiveMaximum: 64, MaxPacketBytes: 1 << 20, NextPacketID: 1, NextDeliveryOrder: 1, QuotaMessages: 1000, QuotaBytes: 1 << 20, UpdatedAtMS: 1000}
	created, e := origin.CompareAndSwapMQTTSession(ctx, 0, s)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTSessionCASApplied, created.Status)
	for i := 0; i < 65; i++ {
		sub := metadb.MQTTSubscription{Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: 1, Topic: fmt.Sprintf("topic-%03d", i), Generation: s.Revision + 1, Revision: s.Revision + 1, TargetKind: metadb.MQTTSubscriptionGroup, TargetID: "group", GrantedQoS: 1, Stage: metadb.MQTTSubscriptionPreparing, OperationID: fmt.Sprintf("op-%03d", i), RecoveryAtMS: 1000, UpdatedAtMS: 1000}
		r, e := origin.MutateMQTTSubscription(ctx, metadb.MQTTSubscriptionMutation{ExpectedRevision: s.Revision, OwnerGeneration: s.OwnerGeneration, OwnerNodeID: s.OwnerNodeID, OwnerBootID: s.OwnerBootID, ConnectionID: s.ConnectionID, Subscription: sub})
		require.NoError(t, e)
		require.Equal(t, metadb.MQTTSessionCASApplied, r.Status)
		s.Revision = r.CurrentRevision
	}
	// Clean Start supersedes the old generation while preserving the stable binding.
	oldRevision := s.Revision
	s.Revision++
	s.Generation++
	s.OwnerGeneration++
	s.ConnectionID++
	changed, e := origin.CompareAndSwapMQTTSession(ctx, oldRevision, s)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTSessionCASApplied, changed.Status)
	m := metadb.MQTTSessionReclamation{Namespace: s.Namespace, ClientID: s.ClientID, ExpectedRevision: s.Revision, ThroughGeneration: 1, UpdatedAtMS: 2000}
	first, e := origin.ReclaimMQTTSession(ctx, m)
	require.NoError(t, e)
	require.False(t, first.Done)
	require.Equal(t, 64, first.RemovedSubscriptions)
	stopNodes(t, nodes...)
	for i, n := range nodes {
		restarted, e := New(n.cfg)
		require.NoError(t, e)
		nodes[i] = restarted
	}
	startNodes(t, nodes...)
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	origin = nodes[0]
	q := metadb.MQTTRead{Kind: metadb.MQTTReadSubscriptions, Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: 1, Limit: 64}
	page, e := origin.ReadMQTT(ctx, q)
	require.NoError(t, e)
	require.Len(t, page.Subscriptions, 1)
	require.Zero(t, page.Session.ReclaimedThroughGeneration)
	require.EqualValues(t, 2, page.Session.Generation)
	m.ExpectedRevision = page.Session.Revision
	m.UpdatedAtMS++
	last, e := origin.ReclaimMQTTSession(ctx, m)
	require.NoError(t, e)
	require.True(t, last.Done)
	require.Equal(t, 1, last.RemovedSubscriptions)
	for _, n := range nodes {
		page, e := n.ReadMQTT(ctx, q)
		require.NoError(t, e)
		require.Empty(t, page.Subscriptions)
		require.True(t, page.Done)
		require.EqualValues(t, 1, page.Session.ReclaimedThroughGeneration)
		require.EqualValues(t, 2, page.Session.Generation)
		require.Equal(t, "alice", page.Session.UID)
	}
	retry, e := origin.ReclaimMQTTSession(ctx, m)
	require.NoError(t, e)
	require.Equal(t, metadb.MQTTSessionCASUnchanged, retry.Status)
	require.True(t, retry.Done)
	t.Log("mqtt_session_reclamation_evidence: nodes=3 hash_slots=256 tcp=true disk=true controlled_clean_start=true subscription_pages=64,1 full_cluster_restart_between_pages=true current_lifetime_preserved=true completion_retry=true automatic_discovery=false")
}
