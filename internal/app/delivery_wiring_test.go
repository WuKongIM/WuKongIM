package app

import (
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestNewWiresIndependentRecipientDeliveryWorkerConcurrency(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	app, err := newTestApp(t,
		Config{
			Cluster: clusterpkg.Config{NodeID: 1},
			ChannelAppend: ChannelAppendConfig{
				RecipientAuthorityDispatchConcurrency: 3,
			},
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
	if app.channelAppendDeliveryWorker == nil {
		t.Fatal("channelappend recipient delivery worker was not wired")
	}
	if got := app.channelAppendDeliveryWorker.WorkerCapacity(); got != 7 {
		t.Fatalf("recipient delivery worker capacity = %d, want 7", got)
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
	if app.deliveryManager == nil {
		t.Fatal("delivery manager was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.DeliveryPushRPCServiceID]; !ok {
		t.Fatalf("delivery push rpc service was not registered")
	}
	if app.deliveryWorker == nil {
		t.Fatal("delivery worker was not wired")
	}
	if app.channelAppendDeliveryWorker == nil {
		t.Fatal("channelappend recipient delivery worker was not wired")
	}
	if app.deliveryManager == nil || app.deliveryManager.PendingAckCount() != 0 {
		t.Fatal("delivery manager was not initialized for async runtime")
	}
	group, ok := app.deliveryWorker.(deliveryWorkerGroup)
	if !ok {
		t.Fatalf("delivery worker = %T, want deliveryWorkerGroup", app.deliveryWorker)
	}
	if len(group) != 3 {
		t.Fatalf("delivery worker count = %d, want recipient worker, retry scheduler, and manager", len(group))
	}
	if group[0] != app.deliveryRetry {
		t.Fatalf("delivery worker[0] = %T, want retry scheduler", group[0])
	}
	if group[1] != app.deliveryManager {
		t.Fatalf("delivery worker[1] = %T, want manager", group[1])
	}
	if _, ok := group[2].(*channelappend.RecipientDeliveryWorker); !ok {
		t.Fatalf("delivery worker[2] = %T, want recipient delivery worker", group[2])
	}
	if group[2] != app.channelAppendDeliveryWorker {
		t.Fatalf("delivery worker[2] = %T, want app channelappend recipient delivery worker", group[2])
	}
	if app.deliveryRetry == nil {
		t.Fatal("delivery retry scheduler was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.DeliveryFanoutRPCServiceID]; !ok {
		t.Fatalf("delivery fanout rpc service was not registered")
	}
}

func waitAppDeliveryPendingAckCount(t *testing.T, app *App, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got int
	for time.Now().Before(deadline) {
		got = app.deliveryManager.PendingAckCount()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending ack count = %d, want %d", got, want)
}
