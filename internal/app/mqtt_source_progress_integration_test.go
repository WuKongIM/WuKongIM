//go:build integration

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// verifyMQTTConsumerProgress uses controlled subscription/window admission to
// exercise real Slot commits and fresh cross-node completion projection. It does
// not claim the still-pending product delivery or SUBACK path is operational.
func verifyMQTTConsumerProgress(t *testing.T, ctx context.Context, nodes []*cluster.Node, owner contract.Owner, prepared sessioncase.PreparedGroupSource, ids interface{ Next() uint64 }) *sessioncase.SourceProgress {
	t.Helper()
	progress, err := newMQTTSourceProgress(nodes[0])
	require.NoError(t, err)
	sessionKey, err := proxy.MQTTSessionRoutingKey(owner.Key.Namespace, owner.Key.ClientID)
	require.NoError(t, err)
	sourceKey, err := proxy.MQTTSourceRoutingKey(prepared.Binding.Key.Owner)
	require.NoError(t, err)
	sessionRoute, err := nodes[0].RouteKey(sessionKey)
	require.NoError(t, err)
	sourceRoute, err := nodes[0].RouteKey(sourceKey)
	require.NoError(t, err)
	require.NotEqual(t, sessionRoute.HashSlot, sourceRoute.HashSlot)
	read := func() meta.MQTTReadResult {
		r, e := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: prepared.Cursor.Key})
		require.NoError(t, e)
		require.NotNil(t, r.Session)
		require.Len(t, r.DeliveryCursors, 1)
		return r
	}
	subs, err := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: owner.Key.Namespace, ClientID: owner.Key.ClientID, SessionGeneration: owner.SessionGeneration, Topic: prepared.Binding.Topic})
	require.NoError(t, err)
	require.Len(t, subs.Subscriptions, 1)
	sub := subs.Subscriptions[0]
	require.Equal(t, meta.MQTTSubscriptionPreparing, sub.Stage)
	sub.Stage, sub.RecoveryAtMS = meta.MQTTSubscriptionActive, 0
	sub.GrantedQoS, sub.Revision, sub.UpdatedAtMS = 1, subs.Session.Revision+1, time.Now().UnixMilli()
	activated, err := nodes[2].MutateMQTTSubscription(ctx, meta.MQTTSubscriptionMutation{ExpectedRevision: subs.Session.Revision, OwnerGeneration: owner.OwnerGeneration, OwnerNodeID: owner.NodeID, OwnerBootID: owner.BootID, ConnectionID: owner.ConnectionID, Subscription: sub})
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, activated.Status)
	accounted, err := nodes[2].MutateMQTTDeliveryCursor(ctx, meta.MQTTDeliveryCursorMutation{Key: prepared.Cursor.Key, ExpectedRevision: read().Session.Revision, OwnerGeneration: owner.OwnerGeneration, OwnerNodeID: owner.NodeID, OwnerBootID: owner.BootID, ConnectionID: owner.ConnectionID, Op: meta.MQTTCursorAccount, Topic: prepared.Cursor.Topic, AuthorizationVersion: prepared.Cursor.AuthorizationVersion, Through: prepared.Cursor.StartAfter + 2, AddedMessages: 2, AddedBytes: 2, UpdatedAtMS: time.Now().UnixMilli()})
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, accounted.Status)
	mutate := func(m meta.MQTTWindowMutation) meta.MQTTWindowResult {
		m.Key, m.ExpectedRevision = prepared.Cursor.Key, read().Session.Revision
		m.OwnerGeneration, m.OwnerNodeID, m.OwnerBootID, m.ConnectionID = owner.OwnerGeneration, owner.NodeID, owner.BootID, owner.ConnectionID
		m.UpdatedAtMS = time.Now().UnixMilli()
		r, e := nodes[2].MutateMQTTWindow(ctx, m)
		require.NoError(t, e)
		require.Equal(t, meta.MQTTWindowApplied, r.Status)
		return r
	}
	var pending []meta.MQTTWindowResult
	for i, id := range []uint64{20001, 20003} {
		position := prepared.Cursor.StartAfter + uint64(i) + 1
		pending = append(pending, mutate(meta.MQTTWindowMutation{Op: meta.MQTTWindowAdmit, Publication: meta.MQTTInflightPublication{Position: position, MessageID: id, MessageSeq: position, ContentVersion: 1, ContentHash: strings.Repeat("a", 64), Bytes: 1}}))
	}
	mutate(meta.MQTTWindowMutation{Op: meta.MQTTWindowAck, PacketID: pending[1].PacketID, DeliveryOrder: pending[1].DeliveryOrder})
	gap, err := progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.False(t, gap.Changed)
	require.Equal(t, prepared.Binding, gap.Binding)
	verifyMQTTReplayRetention(t, ctx, nodes, prepared, prepared.Cursor.StartAfter, false, ids)
	mutate(meta.MQTTWindowMutation{Op: meta.MQTTWindowAck, PacketID: pending[0].PacketID, DeliveryOrder: pending[0].DeliveryOrder})
	completed, err := progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.True(t, completed.Changed)
	require.False(t, completed.NeedsRemoval)
	require.Equal(t, prepared.Cursor.StartAfter+2, completed.Binding.CompletedThrough)
	require.Equal(t, prepared.Binding.Revision+1, completed.Binding.Revision)
	require.Equal(t, prepared.Binding.ProtectionRevision, completed.Binding.ProtectionRevision)
	stored, err := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: prepared.Binding.Key})
	require.NoError(t, err)
	require.Equal(t, []meta.MQTTSourceBinding{completed.Binding}, stored.Bindings)
	again, err := progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.False(t, again.Changed)
	require.Equal(t, completed.Binding, again.Binding)
	verifyMQTTReplayRetention(t, ctx, nodes, prepared, completed.Binding.CompletedThrough, true, ids)
	t.Log("mqtt_consumer_progress_evidence: consumer_progress_cross_hash_slot=true ack_gap_preserved=true coalesced_projection=true independent_remote_read=true admission=controlled product_listener=false")
	return progress
}
