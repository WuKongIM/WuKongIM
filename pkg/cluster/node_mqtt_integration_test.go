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
	checkAccounting := prepareMQTTAccountingRecovery(t, ctx, nodes, origin, s, route.SlotID, route.HashSlot)
	for _, n := range nodes {
		checkAccounting(n, false)
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
	checkAccounting(origin, false)
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
	checkAccounting(restarted, true)
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
	t.Log("mqtt_qualified_accounting_evidence: nodes=3 hash_slots=256 tcp=true disk=true linked_ranges=true empty_coverage=true exact_retry=true quota_end_preserved=true leader_transfer=true restart=true qualification=controlled product_scheduler=false")
	t.Log("mqtt_source_discovery_evidence: nodes=3 hash_slots=256 tcp=true disk=true distinct_sources=true paginated=true leader_transfer=true restart=true isolated_rejected=true removed_source_retained=true removed_consumer_excluded=true binding_release=controlled scheduler=false")
	t.Logf("MQTT metadata verified: hash_slots=256 physical_slots=2 replicas=3 hash_slot=%d slot=%d original_leader=%d new_leader=%d revision=2; restart preserved state; isolated reads and writes rejected", route.HashSlot, route.SlotID, route.Leader, origin.NodeID())
}

// prepareMQTTAccountingRecovery creates controlled metadata obligations in the
// same physical Slot as the transfer test, but outside its deadline-scan shard.
// Qualification remains a caller assertion; this proves distributed durability.
func prepareMQTTAccountingRecovery(t *testing.T, ctx context.Context, nodes []*Node, origin *Node, template metadb.MQTTSession, slotID uint32, excludedHashSlot uint16) func(*Node, bool) {
	t.Helper()
	type expected struct {
		query  metadb.MQTTRead
		result metadb.MQTTReadResult
	}
	var saved []expected
	for _, quota := range []uint64{1000, 2} {
		session := template
		session.QuotaMessages = quota
		session.LeaseUntilMS = 9000
		matched := false
		for i := 0; i < 1024; i++ {
			session.ClientID = fmt.Sprintf("qualified-%d-%d", quota, i)
			key, err := slotproxy.MQTTSessionRoutingKey(session.Namespace, session.ClientID)
			require.NoError(t, err)
			route, err := origin.RouteKey(key)
			require.NoError(t, err)
			if route.SlotID == slotID && route.HashSlot != excludedHashSlot {
				matched = true
				waitRouteKeyLeaderConverged(t, nodes, key)
				break
			}
		}
		require.True(t, matched)
		created, err := origin.ApplyMQTTLifecycle(ctx, metadb.MQTTLifecycleMutation{Event: metadb.MQTTLifecycleConnect, Session: session})
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, created.Status)
		sub := metadb.MQTTSubscription{Namespace: session.Namespace, ClientID: session.ClientID, SessionGeneration: 1, Topic: "wk/v1/groups/Zw/messages", Generation: 2, Revision: 2, TargetKind: metadb.MQTTSubscriptionGroup, TargetID: "g", GrantedQoS: 1, Stage: metadb.MQTTSubscriptionPreparing, OperationID: "qualified", RecoveryAtMS: 1000, UpdatedAtMS: 1000}
		written, err := origin.MutateMQTTSubscription(ctx, metadb.MQTTSubscriptionMutation{ExpectedRevision: 1, OwnerGeneration: session.OwnerGeneration, OwnerNodeID: session.OwnerNodeID, OwnerBootID: session.OwnerBootID, ConnectionID: session.ConnectionID, Subscription: sub})
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, written.Status)
		cursor := metadb.MQTTDeliveryCursorMutation{Key: metadb.MQTTDeliveryCursorKey{Namespace: session.Namespace, ClientID: session.ClientID, SessionGeneration: 1, SubscriptionGeneration: 2, SourceKind: metadb.MQTTSourceChannel, SourceID: "group:g", SourceGeneration: "source-1"}, ExpectedRevision: 2, OwnerGeneration: session.OwnerGeneration, OwnerNodeID: session.OwnerNodeID, OwnerBootID: session.OwnerBootID, ConnectionID: session.ConnectionID, Topic: sub.Topic, Op: metadb.MQTTCursorInit, Through: 100, UpdatedAtMS: 1000}
		initialized, err := origin.MutateMQTTDeliveryCursor(ctx, cursor)
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, initialized.Status)
		cursor.ExpectedRevision, cursor.Op, cursor.Through, cursor.AddedMessages, cursor.AddedBytes = 3, metadb.MQTTCursorAccountQualified, 104, 2, 80
		cursor.Qualified = &metadb.MQTTQualifiedAccounting{From: 101, SubscriptionRevision: 2, Items: []metadb.MQTTAccountingItem{{Position: 101, Bytes: 30}, {Position: 103, Bytes: 50}}}
		first, err := origin.MutateMQTTDeliveryCursor(ctx, cursor)
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, first.Status)
		// A zero-charge page preserves coverage without allocating a receipt.
		cursor.ExpectedRevision, cursor.Through, cursor.AddedMessages, cursor.AddedBytes = 4, 108, 0, 0
		cursor.Qualified = &metadb.MQTTQualifiedAccounting{From: 105, SubscriptionRevision: 2}
		empty, err := origin.MutateMQTTDeliveryCursor(ctx, cursor)
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, empty.Status)
		cursor.ExpectedRevision, cursor.Through, cursor.AddedMessages, cursor.AddedBytes = 5, 112, 1, 70
		cursor.Qualified = &metadb.MQTTQualifiedAccounting{From: 109, SubscriptionRevision: 2, Items: []metadb.MQTTAccountingItem{{Position: 111, Bytes: 70}}}
		second, err := origin.MutateMQTTDeliveryCursor(ctx, cursor)
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, second.Status)
		if quota == 2 {
			require.Equal(t, metadb.MQTTSessionEnded, second.SessionState)
			require.Equal(t, metadb.MQTTSessionQuota, second.TerminationReason)
		}
		retry, err := origin.MutateMQTTDeliveryCursor(ctx, cursor)
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASUnchanged, retry.Status)
		query := metadb.MQTTRead{Kind: metadb.MQTTReadAccounting, CursorKey: cursor.Key}
		got, err := origin.ReadMQTT(ctx, query)
		require.NoError(t, err)
		require.NotNil(t, got.Accounting)
		require.EqualValues(t, 109, got.Accounting.NextFrom)
		require.EqualValues(t, 101, got.DeliveryCursors[0].AccountingHead)
		require.EqualValues(t, 109, got.DeliveryCursors[0].AccountingTail)
		require.EqualValues(t, 3, got.Session.PendingMessages)
		require.EqualValues(t, 150, got.Session.PendingBytes)
		saved = append(saved, expected{query: query, result: got})
	}
	return func(n *Node, consume bool) {
		t.Helper()
		for _, want := range saved {
			got, err := n.ReadMQTT(ctx, want.query)
			require.NoError(t, err)
			require.Equal(t, want.result, got)
			if consume && got.Session.State == metadb.MQTTSessionActive {
				// Recover and consume both linked receipts through the restarted
				// facade; reading only the first would not check the stored tail.
				owner := got.Session
				advance := metadb.MQTTWindowMutation{Key: want.query.CursorKey, ExpectedRevision: owner.Revision, OwnerGeneration: owner.OwnerGeneration, OwnerNodeID: owner.OwnerNodeID, OwnerBootID: owner.OwnerBootID, ConnectionID: owner.ConnectionID, Op: metadb.MQTTWindowAdvance, Through: 108, ReleasedMessages: 2, ReleasedBytes: 80, UpdatedAtMS: 2000}
				first, err := n.MutateMQTTWindow(ctx, advance)
				require.NoError(t, err)
				require.Equal(t, metadb.MQTTWindowApplied, first.Status)
				head, err := n.ReadMQTT(ctx, want.query)
				require.NoError(t, err)
				require.NotNil(t, head.Accounting)
				require.EqualValues(t, 109, head.Accounting.From)
				require.Equal(t, []metadb.MQTTAccountingItem{{Position: 111, Bytes: 70}}, head.Accounting.Items)
				advance.ExpectedRevision, advance.Through, advance.ReleasedMessages, advance.ReleasedBytes = first.CurrentRevision, 112, 1, 70
				last, err := n.MutateMQTTWindow(ctx, advance)
				require.NoError(t, err)
				require.Equal(t, metadb.MQTTWindowApplied, last.Status)
				empty, err := n.ReadMQTT(ctx, want.query)
				require.NoError(t, err)
				require.Nil(t, empty.Accounting)
				require.Zero(t, empty.Session.PendingMessages)
				require.Zero(t, empty.Session.PendingBytes)
				require.EqualValues(t, 112, empty.DeliveryCursors[0].CompletedThrough)
			}
		}
	}
}
