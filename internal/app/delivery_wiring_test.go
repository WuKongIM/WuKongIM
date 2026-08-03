package app

import (
	"context"
	"errors"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
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

	if app.Delivery() == nil {
		t.Fatal("delivery usecase was not wired")
	}
	if err := app.Delivery().SubmitCommitted(context.Background(), messageevents.MessageCommitted{}); !errors.Is(err, errOnlineDeliveryCommittedSubmitUnsupported) {
		t.Fatalf("delivery compatibility SubmitCommitted() error = %v, want canonical-plan requirement", err)
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
