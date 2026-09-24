//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifyMQTTGroupProjection uses real protection, shared copy/recovery and Slot
// intent commits. Only permission incarnation remains a controlled authority.
func verifyMQTTGroupProjection(t *testing.T, ctx context.Context, nodes []*cluster.Node, owners []*runtime.Owners, sessions []*sessioncase.App, authorization sessioncase.SubscriptionAuthorizer, ids interface{ Next() uint64 }) {
	t.Helper()
	id := ch.ChannelID{ID: "projection-group", Type: 2}
	seedGroupSendPermission(t, nodes[0], id, "alice")
	m := meta.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2}, MinISR: 2, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: metafsm.EncodeUpsertChannelRuntimeMetaCommand(m)}))
	cmd := sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "group-projection"}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }}
	first, e := sessions[0].Connect(ctx, cmd)
	require.NoError(t, e)
	projection, e := newMQTTGroupProjection(nodes[0], owners[0], authorization, ids)
	require.NoError(t, e)
	subs, e := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: nodes[0], Owners: owners[0], Authorization: authorization, Projection: projection})
	require.NoError(t, e)
	request := sessioncase.SubscriptionRequest{Topic: "wk/v1/groups/cHJvamVjdGlvbi1ncm91cA/messages", TargetKind: meta.MQTTSubscriptionGroup, TargetID: id.ID, RequestedQoS: 1}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, e = subs.Subscribe(ctx, first.Owner, request)
		require.ErrorIs(c, e, sessioncase.ErrReplayPending, "a committed anchor cannot immediately certify every replica")
	}, 10*time.Second, 30*time.Millisecond)
	intent, e := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: cmd.Key.Namespace, ClientID: cmd.Key.ClientID, SessionGeneration: first.Owner.SessionGeneration, Topic: request.Topic})
	require.NoError(t, e)
	require.Len(t, intent.Subscriptions, 1)
	require.Equal(t, meta.MQTTSubscriptionPreparing, intent.Subscriptions[0].Stage)
	cursors, e := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: cmd.Key.Namespace, ClientID: cmd.Key.ClientID, SessionGeneration: first.Owner.SessionGeneration, SubscriptionGeneration: intent.Subscriptions[0].Generation, Limit: 2})
	require.NoError(t, e)
	require.Len(t, cursors.DeliveryCursors, 1)
	original := cursors.DeliveryCursors[0]
	require.Eventually(t, func() bool {
		view, e := nodes[0].ProbeChannel(ctx, 3, id.ID, id.Type)
		return e == nil && view.ReplayReadiness != nil && view.ReplayReadiness.AnchorPosition > 0 && !view.ReplayReadiness.Covered
	}, 10*time.Second, 30*time.Millisecond, "learner must independently show missing shared content")
	_, e = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: ids.Next(), FromUID: "alice", Payload: []byte("after pending subscription"), ServerTimestampMS: time.Now().UnixMilli()}})
	require.NoError(t, e)
	resumed, e := sessions[2].Connect(ctx, cmd)
	require.NoError(t, e)
	require.Equal(t, first.Owner.SessionGeneration, resumed.Owner.SessionGeneration)
	projection, e = newMQTTGroupProjection(nodes[2], owners[2], authorization, ids)
	require.NoError(t, e)
	subs, e = sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: nodes[2], Owners: owners[2], Authorization: authorization, Projection: projection})
	require.NoError(t, e)
	var active meta.MQTTSubscription
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		active, e = subs.Reconcile(ctx, resumed.Owner, request.Topic)
		require.NoError(c, e)
	}, 15*time.Second, 30*time.Millisecond)
	require.Equal(t, meta.MQTTSubscriptionActive, active.Stage)
	require.Equal(t, intent.Subscriptions[0].Generation, active.Generation)
	after, e := nodes[0].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: original.Key})
	require.NoError(t, e)
	require.Equal(t, []meta.MQTTDeliveryCursor{original}, after.DeliveryCursors, "resume must retain the first prepared cursor")
	ready, e := nodes[0].ProbeChannel(ctx, 3, id.ID, id.Type)
	require.NoError(t, e)
	require.NotNil(t, ready.ReplayReadiness)
	require.True(t, ready.ReplayReadiness.Covered)
	existed, e := subs.Unsubscribe(ctx, resumed.Owner, request.Topic)
	require.NoError(t, e)
	require.True(t, existed)
	removed, e := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: cmd.Key.Namespace, ClientID: cmd.Key.ClientID, SessionGeneration: resumed.Owner.SessionGeneration, Topic: request.Topic})
	require.NoError(t, e)
	require.Equal(t, meta.MQTTSubscriptionRemoved, removed.Subscriptions[0].Stage)
	t.Log("mqtt_group_projection_evidence: nodes=3 hash_slots=256 real_establishment=true learner_missing_content_blocks=true owner_1_to_3=true original_cursor_preserved=true all_replica_confirmation=true real_removal=true permission_incarnation=controlled product_listener=false")
}
