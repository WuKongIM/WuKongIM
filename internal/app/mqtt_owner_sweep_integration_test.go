//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

func TestMQTTOwnerSweepProductSingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	cfg.MQTT = MQTTConfig{Enabled: true, ListenAddr: freeSendackSmokeTCPAddr(t)}
	cfg.Observability.MetricsEnabled = true
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	generation := a.mqtt.current.Load()
	require.NotNil(t, generation)
	closed := make(chan struct{})
	_, err = generation.owners.Reserve(runtime.Claim{Key: contract.Key{Namespace: "main", ClientID: "abandoned"}, UID: "alice", SessionGeneration: 1, OwnerGeneration: 1}, func(context.Context) error { close(closed); return nil })
	require.NoError(t, err)
	require.Equal(t, 1, generation.owners.Snapshot().Pending)
	require.Zero(t, generation.connections.Snapshot().Tracked)
	select {
	case <-closed:
	case <-time.After(7 * time.Second):
		t.Fatal("product never swept an expired unregistered reservation")
	}
	require.Eventually(t, func() bool { return generation.owners.Snapshot().Held == 0 }, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		families, err := a.metrics.Gather()
		if err != nil {
			return false
		}
		for _, f := range families {
			if f.GetName() != "wukongim_mqtt_owner_sweep_total" {
				continue
			}
			for _, m := range f.Metric {
				for _, l := range m.Label {
					if l.GetName() == "event" && l.GetValue() == "visited" && m.GetCounter().GetValue() >= 1 {
						return true
					}
				}
			}
		}
		return false
	}, time.Second, 10*time.Millisecond, "app must publish aggregate cleanup observations")
	require.NoError(t, a.Stop(ctx))
	require.NoError(t, a.goroutines.Group(gr.ModuleMQTT).Wait(ctx))
	require.Zero(t, generation.owners.Snapshot().Deadlines)
	t.Log("mqtt_owner_sweep: hash_slots=256 product_pending_cleanup=true connection_registration=false joined_shutdown=true")
}
