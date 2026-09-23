package app

import (
	"context"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestNewWiresConfiguredOnlineDeliveryWorkerConcurrency(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	app, err := newTestApp(t,
		Config{
			Cluster: clusterpkg.Config{NodeID: 1},
			Delivery: DeliveryConfig{
				Enabled:                    true,
				RecipientWorkerConcurrency: 7,
			},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.onlineDelivery == nil {
		t.Fatal("online delivery runtime was not wired")
	}
	if got := app.onlineDelivery.WorkerCapacity(); got != 7 {
		t.Fatalf("online delivery worker capacity = %d, want 7", got)
	}
}

func TestNewWiresDeliveryWhenEnabled(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterpkg.Config{NodeID: 1},
			Delivery: DeliveryConfig{Enabled: true},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if app.onlineDelivery == nil {
		t.Fatal("online delivery runtime was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.DeliveryPushRPCServiceID]; !ok {
		t.Fatalf("delivery push rpc service was not registered")
	}
	if app.deliveryWorker == nil {
		t.Fatal("delivery worker was not wired")
	}
	if app.onlineDelivery.PendingAckCount() != 0 {
		t.Fatal("online delivery runtime was not initialized with empty ack state")
	}
	if app.deliveryWorker != app.onlineDelivery {
		t.Fatalf("delivery worker = %T, want online delivery runtime", app.deliveryWorker)
	}
	if _, ok := cluster.registeredHandlers[clusternet.RPCDeliveryFanout]; ok {
		t.Fatalf("retired delivery fanout rpc service was registered")
	}
}

func waitAppDeliveryPendingAckCount(t *testing.T, app *App, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got int
	for time.Now().Before(deadline) {
		if app.onlineDelivery != nil {
			got = app.onlineDelivery.PendingAckCount()
		}
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending ack count = %d, want %d", got, want)
}

func TestGatewayFeedbackWiringPreservesExactSessionOwnership(t *testing.T) {
	t.Parallel()

	tracker := runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
		ShardCount: 4,
		Now:        func() int64 { return 1_788_323_400 },
	})
	runtime := runtimedelivery.NewRuntime(runtimedelivery.RuntimeOptions{
		LocalNodeID: 1,
		Acks:        tracker,
	})
	app := &App{onlineDelivery: runtime, logger: wklog.NewNop()}
	app.wireGatewayHandler(1)
	sess := session.New(session.Config{ID: 10})
	sess.SetValue(coregateway.SessionValueUID, "u1")
	ctx := coregateway.Context{Session: sess, RequestContext: context.Background()}
	for _, pending := range []runtimedelivery.PendingRecvAck{
		{UID: "u1", SessionID: 10, MessageID: 100, MessageSeq: 1},
		{UID: "u1", SessionID: 10, MessageID: 101, MessageSeq: 2},
		{UID: "u1", SessionID: 11, MessageID: 100, MessageSeq: 3},
		{UID: "u2", SessionID: 10, MessageID: 100, MessageSeq: 4},
	} {
		require.True(t, tracker.Bind(pending))
	}

	// Unknown feedback must leave all exact-session bindings intact.
	require.NoError(t, app.handler.OnFrame(ctx, &frame.RecvackPacket{MessageID: 999, MessageSeq: 1}))
	require.Equal(t, 4, tracker.PendingCount())
	require.NoError(t, app.handler.OnFrame(ctx, &frame.RecvackPacket{MessageID: 100, MessageSeq: 1}))
	require.Equal(t, 3, tracker.PendingCount())
	_, found := tracker.Ack(runtimedelivery.Recvack{
		UID: "u1", SessionID: 10, MessageID: 100,
	})
	require.False(t, found, "the acknowledged identity must be removed")

	require.NoError(t, app.handler.OnSessionClose(ctx))
	require.Equal(t, 2, tracker.PendingCount())
	_, found = tracker.Ack(runtimedelivery.Recvack{
		UID: "u1", SessionID: 10, MessageID: 101,
	})
	require.False(t, found, "the closed session must be removed")
	for _, ack := range []runtimedelivery.Recvack{
		{UID: "u1", SessionID: 11, MessageID: 100},
		{UID: "u2", SessionID: 10, MessageID: 100},
	} {
		_, found = tracker.Ack(ack)
		require.True(t, found, "unrelated owner session must remain pending")
	}
	require.Zero(t, tracker.PendingCount())
}

func TestGatewayFeedbackWiringAllowsDisabledDelivery(t *testing.T) {
	app := &App{logger: wklog.NewNop()}
	app.wireDelivery()
	require.Nil(t, app.onlineDelivery)
	app.wireGatewayHandler(1)
	sess := session.New(session.Config{ID: 10})
	sess.SetValue(coregateway.SessionValueUID, "u1")
	ctx := coregateway.Context{Session: sess, RequestContext: context.Background()}
	require.NoError(t, app.handler.OnFrame(ctx, &frame.RecvackPacket{MessageID: 100, MessageSeq: 1}))
	require.NoError(t, app.handler.OnSessionClose(ctx))
}
