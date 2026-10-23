package app

import (
	"context"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
)

func newAckGaugeCollector() *topCollector {
	return newTopCollector(topCollectorOptions{ResourceSampler: func() topResourceSample { return topResourceSample{} }})
}

func requireAckGauge(t *testing.T, collector *topCollector, want int64) {
	t.Helper()
	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{Window: time.Minute, View: accessapi.TopViewAll})
	if err != nil {
		t.Fatalf("SnapshotTop: %v", err)
	}
	if snapshot.Delivery == nil || snapshot.Delivery.AckBindings != want {
		t.Fatalf("delivery snapshot = %+v, want ACK bindings %d", snapshot.Delivery, want)
	}
}

func TestTopAckGaugeSnapshotCloneAndZero(t *testing.T) {
	collector := newAckGaugeCollector()
	collector.recordSampleAt(time.Unix(100, 0))
	collector.recordSampleAt(time.Unix(101, 0))
	requireAckGauge(t, collector, 0)
	collector.SetDeliveryAckBindings(7)
	copy := collector.clone()
	// A live update must not rewrite already retained samples.
	requireAckGauge(t, collector, 0)
	copy.recordSampleAt(time.Unix(102, 0))
	requireAckGauge(t, copy, 7)
	collector.recordSampleAt(time.Unix(102, 0))
	requireAckGauge(t, collector, 7)
	collector.SetDeliveryAckBindings(0)
	collector.recordSampleAt(time.Unix(103, 0))
	requireAckGauge(t, collector, 0)
	copy.recordSampleAt(time.Unix(103, 0))
	requireAckGauge(t, copy, 7)
	zeroCopy := collector.clone()
	zeroCopy.recordSampleAt(time.Unix(104, 0))
	requireAckGauge(t, zeroCopy, 0)
}
