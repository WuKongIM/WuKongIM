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
	if snapshot.Verdict.Summary != "runtime pressure detected" {
		t.Fatalf("Verdict.Summary = %q, want runtime pressure detected", snapshot.Verdict.Summary)
	}
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) == 0 {
		t.Fatalf("Pressure = %#v, want top pressure item", snapshot.Pressure)
	}
	if snapshot.Pressure.Top[0].Component != "channelv2" {
		t.Fatalf("top pressure component = %q, want channelv2", snapshot.Pressure.Top[0].Component)
	}
	if len(snapshot.Verdict.Reasons) == 0 || snapshot.Verdict.Reasons[0] != "channelv2/store_append pressure" {
		t.Fatalf("Verdict.Reasons = %#v, want channelv2/store_append pressure", snapshot.Verdict.Reasons)
	}
}

func TestTopCollectorVerdictStrings(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewOverview,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Verdict.Summary != "runtime healthy" {
		t.Fatalf("ok summary = %q, want runtime healthy", snapshot.Verdict.Summary)
	}

	notReady := buildTopVerdict(clusterv2.Snapshot{RoutesReady: true, SlotsReady: true}, nil, nil)
	if notReady.Summary != "cluster runtime is not ready" {
		t.Fatalf("not-ready summary = %q, want cluster runtime is not ready", notReady.Summary)
	}
	if len(notReady.Reasons) != 1 || notReady.Reasons[0] != "channelv2 not ready" {
		t.Fatalf("not-ready reasons = %#v, want channelv2 not ready", notReady.Reasons)
	}
	sendack := buildTopVerdict(
		clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true},
		&accessapi.TopTraffic{SendackErrorRate: 0.05},
		nil,
	)
	if len(sendack.Reasons) != 1 || sendack.Reasons[0] != "sendack error rate >= 5%" {
		t.Fatalf("sendack reasons = %#v, want sendack error rate >= 5%%", sendack.Reasons)
	}
	if sendack.Summary != "sendack error rate is high" {
		t.Fatalf("sendack summary = %q, want sendack error rate is high", sendack.Summary)
	}
}

func TestTopCollectorEmptyViewDoesNotIncludeOptionalSections(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Traffic != nil || snapshot.Pressure != nil {
		t.Fatalf("Traffic/Pressure = %#v/%#v, want nil for empty view", snapshot.Traffic, snapshot.Pressure)
	}
}

func TestTopCollectorRingWindowIteratesOldestToNewest(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		CollectInterval: time.Second,
		HistoryWindow:   2 * time.Second,
	})
	for i := 0; i < 5; i++ {
		collector.recordSampleAt(time.Unix(int64(100+i), 0))
	}

	window := collector.windowLocked(10 * time.Second)
	if collector.count != len(collector.ring) {
		t.Fatalf("count = %d, want ring capacity %d", collector.count, len(collector.ring))
	}
	if len(window) != len(collector.ring) {
		t.Fatalf("window len = %d, want %d", len(window), len(collector.ring))
	}
	for i, sample := range window {
		want := time.Unix(int64(101+i), 0).UTC()
		if !sample.at.Equal(want) {
			t.Fatalf("window[%d] = %s, want %s", i, sample.at, want)
		}
	}
}
