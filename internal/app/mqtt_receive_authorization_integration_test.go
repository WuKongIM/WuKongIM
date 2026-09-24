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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifyMQTTReceiveRejoin uses native membership, established protection,
// accounting, sender and exact-owner termination. Only the wire sink is controlled.
func verifyMQTTReceiveRejoin(t *testing.T, ctx context.Context, nodes []*cluster.Node, owners []*runtime.Owners, sessions []*sessioncase.App, workers []*runtime.ReplayWorker, ids interface{ Next() uint64 }) {
	t.Helper()
	id := ch.ChannelID{ID: "projection-group", Type: 2}
	cmd := sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "receive-rejoin"}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }}
	old, err := sessions[2].Connect(ctx, cmd)
	require.NoError(t, err)
	authority, err := newMQTTReceiveAuthorization(nodes[2])
	require.NoError(t, err)
	projection, err := newMQTTGroupProjection(nodes[2], owners[2], authority, ids)
	require.NoError(t, err)
	subs, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: nodes[2], Owners: owners[2], Authorization: authority, Projection: projection})
	require.NoError(t, err)
	request := sessioncase.SubscriptionRequest{Topic: "wk/v1/groups/cHJvamVjdGlvbi1ncm91cA/messages", TargetID: id.ID, TargetKind: meta.MQTTSubscriptionGroup, RequestedQoS: 1}
	var active meta.MQTTSubscription
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		active, err = subs.Subscribe(ctx, old.Owner, request)
		require.NoError(c, err)
	}, 15*time.Second, 30*time.Millisecond)
	require.Greater(t, active.AuthorizationVersion, uint64(1))
	cursors, err := nodes[0].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: cmd.Key.Namespace, ClientID: cmd.Key.ClientID, SessionGeneration: old.Owner.SessionGeneration, SubscriptionGeneration: active.Generation, Limit: 2})
	require.NoError(t, err)
	require.Len(t, cursors.DeliveryCursors, 1)
	key := cursors.DeliveryCursors[0].Key
	// Earlier migration assertions stopped these workers to inspect disk state.
	// Resume their real discovery/copy loops for new publications in this case.
	for _, w := range workers {
		require.NoError(t, w.Start(ctx))
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, w := range workers {
			require.NoError(t, w.Stop(cleanup))
		}
	}()

	for _, body := range []string{"begun exchange", "unadmitted backlog"} {
		_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: ids.Next(), FromUID: "alice", Payload: []byte(body), ServerTimestampMS: time.Now().UnixMilli()}})
		require.NoError(t, err)
	}
	account, err := newMQTTAccounting(nodes[2], authority)
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = account.Account(ctx, key)
		require.NoError(c, err)
		row, e := nodes[0].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: cmd.Key.Namespace, ClientID: cmd.Key.ClientID})
		require.NoError(c, e)
		require.NotNil(c, row.Session)
		require.EqualValues(c, 2, row.Session.PendingMessages)
	}, 15*time.Second, 30*time.Millisecond)
	sender, err := newMQTTSender(nodes[2], owners[2], authority, sessions[2])
	require.NoError(t, err)
	queued := 0
	stream, err := sender.Open(ctx, old, mqttProgressDeliverySink{enqueue: func(_ context.Context, d sessioncase.PreparedDelivery, dup bool) (sessioncase.DeliveryDisposition, error) {
		queued++
		require.False(t, dup)
		require.EqualValues(t, 1, d.QoS)
		return sessioncase.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	var turn sessioncase.DeliveryTurn
	for i := 0; i < 16 && !turn.Enqueued; i++ {
		turn, err = stream.Turn(ctx, key)
		require.NoError(t, err)
		require.True(t, turn.Advanced || turn.Enqueued, "each turn must skip a native control or enqueue the first publication")
	}
	require.True(t, turn.Enqueued)
	require.Equal(t, 1, queued)
	read := func() meta.MQTTReadResult {
		t.Helper()
		r, e := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: key})
		require.NoError(t, e)
		return r
	}
	before := read()
	require.EqualValues(t, 1, before.Session.OutboundInflight)
	require.EqualValues(t, 2, before.Session.PendingMessages)
	require.NoError(t, nodes[0].RemoveChannelSubscribers(ctx, id.ID, 2, []string{"alice"}, 2))
	require.NoError(t, nodes[1].AddChannelSubscribers(ctx, id.ID, 2, []string{"alice"}, 2))
	resumed, err := sessions[0].Connect(ctx, cmd)
	require.NoError(t, err)
	require.True(t, resumed.SessionPresent)
	currentAuthority, err := newMQTTReceiveAuthorization(nodes[0])
	require.NoError(t, err)
	sender, err = newMQTTSender(nodes[0], owners[0], currentAuthority, sessions[0])
	require.NoError(t, err)
	stream, err = sender.Open(ctx, resumed, mqttProgressDeliverySink{enqueue: func(context.Context, sessioncase.PreparedDelivery, bool) (sessioncase.DeliveryDisposition, error) {
		t.Fatal("revoked old exchange or backlog reached wire")
		return sessioncase.DeliveryQueued, nil
	}})
	require.NoError(t, err)
	turn, err = stream.Turn(ctx, key)
	require.ErrorIs(t, err, sessioncase.ErrSubscriptionRevoked)
	require.True(t, turn.Ended)
	ended := read()
	require.Equal(t, meta.MQTTSessionEnded, ended.Session.State)
	require.Equal(t, meta.MQTTSessionRevoked, ended.Session.TerminationReason)
	require.Equal(t, before.Session.PendingMessages, ended.Session.PendingMessages)
	require.Equal(t, before.Session.PendingBytes, ended.Session.PendingBytes)
	require.Equal(t, before.Session.OutboundInflight, ended.Session.OutboundInflight)
	require.Equal(t, before.DeliveryCursors, ended.DeliveryCursors)
	fresh, err := sessions[0].Connect(ctx, cmd)
	require.NoError(t, err)
	require.False(t, fresh.SessionPresent)
	require.Greater(t, fresh.Owner.SessionGeneration, resumed.Owner.SessionGeneration)
	projection, err = newMQTTGroupProjection(nodes[0], owners[0], currentAuthority, ids)
	require.NoError(t, err)
	subs, err = sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: nodes[0], Owners: owners[0], Authorization: currentAuthority, Projection: projection})
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		next, e := subs.Subscribe(ctx, fresh.Owner, request)
		require.NoError(c, e)
		if e == nil {
			require.Greater(c, next.AuthorizationVersion, active.AuthorizationVersion)
		}
	}, 15*time.Second, 30*time.Millisecond)
	t.Log("mqtt_receive_authority_evidence: nodes=3 hash_slots=256 membership=real same_version_rejoin=true resume_before_revocation_check=true old_exchange_and_backlog_denied=true exact_session_ended=true ending_debt_retained=true fresh_lifetime_and_subscription=true projection=real sender=real sink=controlled product_listener=false")
}
