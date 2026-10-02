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

// Product composition discovers these old intents without a caller cleanup
// proposal. TCP/disk authority is real; this is not a process-level MQTT test.
func verifyMQTTSessionReclamation(t *testing.T, ctx context.Context, nodes []*cluster.Node, owners []*runtime.Owners, sessions []*sessioncase.App, auth sessioncase.SubscriptionAuthorizer) {
	t.Helper()
	var queries []meta.MQTTRead
	var successor sessioncase.Connection
	for _, mode := range []string{"ended", "successor"} {
		cmd := sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "reclamation-" + mode}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }}
		conn, e := sessions[0].Connect(ctx, cmd)
		require.NoError(t, e)
		projection := &mqttSubscriptionProjectionFixture{establish: func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
			return sessioncase.SubscriptionProjectionReceipt{}, sessioncase.ErrReplayPending
		}}
		subs, e := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: nodes[0], Owners: owners[0], Authorization: auth, Projection: projection})
		require.NoError(t, e)
		topic := "wk/v1/groups/Z3JvdXA/messages"
		_, e = subs.Subscribe(ctx, conn.Owner, sessioncase.SubscriptionRequest{Topic: topic, TargetKind: meta.MQTTSubscriptionGroup, TargetID: "group", RequestedQoS: 1})
		require.ErrorIs(t, e, sessioncase.ErrReplayPending)
		q := meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: conn.Owner.Key.Namespace, ClientID: conn.Owner.Key.ClientID, SessionGeneration: conn.Owner.SessionGeneration, Topic: topic}
		before, e := nodes[2].ReadMQTT(ctx, q)
		require.NoError(t, e)
		require.Len(t, before.Subscriptions, 1)
		require.NoError(t, sessions[0].End(ctx, sessioncase.EndCommand{Owner: conn.Owner, Reason: meta.MQTTSessionExplicit}))
		if mode == "successor" {
			successor, e = sessions[0].Connect(ctx, cmd)
			require.NoError(t, e)
		}
		queries = append(queries, q)
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
		groups, err := newMQTTGroupProjection(n, owners[i], auth, ids)
		require.NoError(t, err)
		inbox, err := newMQTTInboxEstablishment(n, owners[i], auth, ids)
		require.NoError(t, err)
		worker, err := a.wireMQTTConsumers(n, auth, sessions[i], groups, inbox)
		require.NoError(t, err)
		workers = append(workers, worker)
		require.NoError(t, worker.Start(ctx))
	}

	require.Eventually(t, func() bool {
		for _, q := range queries {
			call, done := context.WithTimeout(ctx, time.Second)
			r, e := nodes[2].ReadMQTT(call, q)
			done()
			if e != nil || r.Session == nil || r.Session.UID != "alice" || r.Session.ReclaimedThroughGeneration < q.SessionGeneration || len(r.Subscriptions) != 0 {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond, "composed worker did not autonomously reclaim old intents")
	scope, e := owners[0].Begin(ctx, successor.Owner)
	require.NoError(t, e)
	scope.Done()
	t.Log("mqtt_session_reclamation_evidence: nodes=3 hash_slots=256 old_intents_removed=true ended_and_successor=true successor_running=true product_worker_composition=true cleanup_proposals=automatic product_listener=false")
}
