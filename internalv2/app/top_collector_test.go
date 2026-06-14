package app

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestTopCollectorSnapshotDoesNotRequireMetrics(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          2,
		NodeName:        "node-2",
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{
				NodeID:        2,
				RoutesReady:   true,
				SlotsReady:    true,
				ChannelsReady: true,
			}
		},
		MetricsEnabled: false,
	})

	collector.recordSampleAt(time.Unix(100, 0))
	collector.ObserveGatewaySend("wkproto", 128)
	collector.ObserveGatewaySendack("success", "gateway", "send")
	collector.ObserveMessageAppend("channelv2", "ok", 10*time.Millisecond)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewAll,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Sources.Metrics.Required {
		t.Fatalf("Metrics.Required = true, want false")
	}
	if snapshot.Node.ID != 2 || !snapshot.Node.Ready {
		t.Fatalf("Node = %#v, want id 2 ready", snapshot.Node)
	}
	if snapshot.Traffic == nil {
		t.Fatalf("Traffic = nil")
	}
	if math.Abs(snapshot.Traffic.SendPerSec-0.1) > 0.000001 {
		t.Fatalf("SendPerSec = %v, want 0.1", snapshot.Traffic.SendPerSec)
	}
}

func TestTopCollectorWarmingUp(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{})

	_, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewAll,
		Limit:  10,
	})
	if !errors.Is(err, accessapi.ErrTopWarmingUp) {
		t.Fatalf("SnapshotTop() error = %v, want %v", err, accessapi.ErrTopWarmingUp)
	}
}

func TestTopCollectorPressureVerdict(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID: 2,
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{
				NodeID:        2,
				RoutesReady:   true,
				SlotsReady:    true,
				ChannelsReady: true,
			}
		},
	})
	collector.SetQueue("channelv2", "store_append", "write", "none", 86, 100)
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewAll,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Verdict.Level != "degraded" {
		t.Fatalf("Verdict.Level = %q, want degraded", snapshot.Verdict.Level)
	}
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) == 0 {
		t.Fatalf("Pressure = %#v, want top pressure item", snapshot.Pressure)
	}
	if snapshot.Pressure.Top[0].Component != "channelv2" {
		t.Fatalf("top pressure component = %q, want channelv2", snapshot.Pressure.Top[0].Component)
	}
}
