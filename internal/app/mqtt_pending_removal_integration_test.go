//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

// verifyMQTTPendingRemoval uses real product consumer composition on all three
// routed nodes. Only the interruption before initial projection is controlled;
// background recovery invokes no foreground Unsubscribe or live Owner retry.
func verifyMQTTPendingRemoval(t *testing.T, ctx context.Context, nodes []*cluster.Node, owners []*runtime.Owners, sessions []*sessioncase.App, auth sessioncase.SubscriptionAuthorizer) {
	t.Helper()
	queries := []meta.MQTTRead{}
	for _, target := range []string{"inbox", "group"} {
		conn, err := sessions[0].Connect(ctx, sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "pending-no-binding-" + target}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }})
		require.NoError(t, err)
		projection := &mqttSubscriptionProjectionFixture{
			establish: func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
				return sessioncase.SubscriptionProjectionReceipt{}, sessioncase.ErrReplayPending
			},
			remove: func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
				return sessioncase.SubscriptionProjectionReceipt{}, sessioncase.ErrSourceDrainPending
			},
		}
		subs, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: nodes[0], Owners: owners[0], Authorization: auth, Projection: projection})
		require.NoError(t, err)
		request := sessioncase.SubscriptionRequest{Topic: "wk/v1/groups/Z3JvdXA/messages", TargetKind: meta.MQTTSubscriptionGroup, TargetID: "group", RequestedQoS: 1}
		if target == "inbox" {
			request.Topic = "wk/v1/users/YWxpY2U/inbox"
			request.TargetKind = meta.MQTTSubscriptionUserInbox
			request.TargetID = "alice"
		}
		_, err = subs.Subscribe(ctx, conn.Owner, request)
		require.ErrorIs(t, err, sessioncase.ErrReplayPending)
		_, err = subs.Unsubscribe(ctx, conn.Owner, request.Topic)
		require.ErrorIs(t, err, sessioncase.ErrSourceDrainPending)
		require.NoError(t, sessions[0].Disconnect(ctx, sessioncase.DisconnectCommand{Owner: conn.Owner, Normal: true}))
		query := meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: conn.Owner.Key.Namespace, ClientID: conn.Owner.Key.ClientID, SessionGeneration: conn.Owner.SessionGeneration, Topic: request.Topic}
		r, err := nodes[1].ReadMQTT(ctx, query)
		require.NoError(t, err)
		require.Len(t, r.Subscriptions, 1)
		require.Equal(t, meta.MQTTSubscriptionRemoving, r.Subscriptions[0].Stage)
		require.Equal(t, meta.MQTTSessionOffline, r.Session.State)
		queries = append(queries, query)
	}
	workers := []*runtime.ConsumerWorker{}
	defer func() {
		for _, w := range workers {
			stop, done := context.WithTimeout(context.Background(), 5*time.Second)
			require.NoError(t, w.Stop(stop))
			done()
		}
	}()
	for i, n := range nodes {
		ids, err := newNodeMessageIDs(uint64(i + 1))
		require.NoError(t, err)
		a := &App{cfg: Config{NodeID: uint64(i + 1)}, messageIDs: ids, goroutines: gr.New()}
		worker, err := a.wireMQTTConsumers(n, auth, sessions[i])
		require.NoError(t, err)
		workers = append(workers, worker)
		require.NoError(t, worker.Start(ctx))
	}
	require.Eventually(t, func() bool {
		for _, q := range queries {
			call, done := context.WithTimeout(ctx, time.Second)
			r, err := nodes[2].ReadMQTT(call, q)
			done()
			if err != nil || len(r.Subscriptions) != 1 || r.Subscriptions[0].Stage != meta.MQTTSubscriptionRemoved {
				return false
			}
			if r.Session == nil || r.Session.State != meta.MQTTSessionOffline || r.Session.PendingMessages != 0 {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond, "product pending-index workers did not finish disconnected intents")
	t.Log("mqtt_pending_removal_evidence: nodes=3 hash_slots=256 targets=inbox,group before_binding=true owner_disconnected=true product_worker_composition=true subscription_removed=true interruption=controlled product_listener=false")
}
