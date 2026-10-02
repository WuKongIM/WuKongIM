//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// verifyMQTTConsumerProgress uses controlled subscription activation and real window admission to
// exercise real Slot commits and fresh cross-node completion projection. It does
// not claim the still-pending product delivery or SUBACK path is operational.
func verifyMQTTConsumerProgress(t *testing.T, ctx context.Context, nodes []*cluster.Node, owners *runtime.Owners, connection sessioncase.Connection, sessions *sessioncase.App, prepared sessioncase.PreparedGroupSource, ids interface{ Next() uint64 }, expected ch.MQTTReplayAnchorProof, authorization sessioncase.SubscriptionAuthorizer) *sessioncase.SourceProgress {
	t.Helper()
	owner := connection.Owner
	progress, err := newMQTTSourceProgress(nodes[0])
	require.NoError(t, err)
	acknowledgements, err := newMQTTAcknowledgements(nodes[2], owners)
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
	require.Zero(t, read().Session.PendingMessages)
	placement, err := channels.NewSlotMetaSource(nodes[0]).ResolveChannelMetaFresh(ctx, ch.ChannelID{ID: "group", Type: 2})
	require.NoError(t, err)
	content, err := nodes[0].ReadChannelMQTTReplay(ctx, ch.MQTTReplayConsumerRequest{AnchorPosition: expected.Manifest.LastOffset, Request: ch.MQTTReplayRequest{ChannelID: placement.ID, ExpectedChannelEpoch: placement.Epoch, ExpectedLeaderEpoch: placement.LeaderEpoch, ExpectedRouteGeneration: placement.RouteGeneration, Range: ch.MQTTReplayRange{Generation: prepared.Cursor.Key.SourceGeneration, From: prepared.Cursor.StartAfter + 1, Through: expected.Anchor.Through, Limit: 256, MaxBytes: 16 << 20}}})
	require.NoError(t, err)
	require.Len(t, content.Records, 2)
	coordinator, err := newMQTTDeliveryCoordinator(nodes[2], owners, authorization, sessions, 128)
	require.NoError(t, err)
	var pending []meta.MQTTInflight
	var publications []sessioncase.PreparedDelivery
	var duplicates []bool
	stream, err := coordinator.Open(ctx, connection, mqttProgressDeliverySink{enqueue: func(_ context.Context, d sessioncase.PreparedDelivery, dup bool) (sessioncase.DeliveryDisposition, error) {
		pending = append(pending, d.Exchange)
		publications = append(publications, d)
		duplicates = append(duplicates, dup)
		return sessioncase.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	scheduler, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: owners, Workers: 2})
	require.NoError(t, err)
	require.NoError(t, scheduler.Start(ctx))
	stopScheduler := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, scheduler.Stop(stopCtx))
	}
	defer stopScheduler()
	require.NoError(t, scheduler.Register(owner, stream))
	require.Eventually(t, func() bool { return scheduler.Snapshot().Turns >= 3 }, 10*time.Second, time.Millisecond)
	stopScheduler() // Join before inspecting callback-owned observations.
	require.Zero(t, scheduler.Snapshot().Failures)
	require.Len(t, publications, 2)
	require.Equal(t, []bool{false, false}, duplicates)
	for i, d := range publications {
		require.Equal(t, content.Records[i], d.Publication)
		require.EqualValues(t, 1, d.QoS)
	}
	require.EqualValues(t, 2, read().Session.OutboundInflight)
	require.EqualValues(t, 2, read().Session.PendingMessages)
	require.Equal(t, content.Records[0].AccountedBytes+content.Records[1].AccountedBytes, read().Session.PendingBytes)
	require.Equal(t, expected.Anchor.Through, read().DeliveryCursors[0].AccountedThrough)
	require.Len(t, pending, 2)
	acknowledge := func(i int) sessioncase.AcknowledgementResult {
		result, e := acknowledgements.Acknowledge(ctx, sessioncase.AcknowledgementCommand{Owner: owner, Key: prepared.Cursor.Key, PacketID: pending[i].PacketID, DeliveryOrder: pending[i].DeliveryOrder})
		require.NoError(t, e)
		return result
	}
	wrong := sessioncase.AcknowledgementCommand{Owner: owner, Key: prepared.Cursor.Key, PacketID: pending[0].PacketID, DeliveryOrder: pending[0].DeliveryOrder + 1}
	_, err = acknowledgements.Acknowledge(ctx, wrong)
	require.ErrorIs(t, err, sessioncase.ErrConflict)
	require.True(t, acknowledge(1).Changed)
	require.True(t, acknowledge(1).Absent)
	gap, err := progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.False(t, gap.Changed)
	require.Equal(t, prepared.Binding, gap.Binding)
	verifyMQTTReplayRetention(t, ctx, nodes, prepared, prepared.Cursor.StartAfter, false, ids, expected)
	require.True(t, acknowledge(0).Changed)
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
	verifyMQTTReplayRetention(t, ctx, nodes, prepared, completed.Binding.CompletedThrough, true, ids, expected)
	t.Log("mqtt_consumer_progress_evidence: consumer_progress_cross_hash_slot=true ack_gap_preserved=true exact_ack_usecase=true duplicate_ack_no_write=true coalesced_projection=true independent_remote_read=true accounting=real_scheduled_anchored_content sender=real_anchored_content delivery_scheduler=real delivery_workers=2 source_discovery=real_session_pages delivery_sink=controlled product_listener=false")
	return progress
}

type mqttProgressDeliverySink struct {
	enqueue func(context.Context, sessioncase.PreparedDelivery, bool) (sessioncase.DeliveryDisposition, error)
}

func (s mqttProgressDeliverySink) Enqueue(ctx context.Context, d sessioncase.PreparedDelivery, dup bool) (sessioncase.DeliveryDisposition, error) {
	return s.enqueue(ctx, d, dup)
}
func (s mqttProgressDeliverySink) Close(context.Context, meta.MQTTSessionEndReason) error { return nil }
