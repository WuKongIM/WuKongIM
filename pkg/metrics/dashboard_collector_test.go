package metrics

import (
	"testing"
	"time"
)

func TestNewDashboardCollector(t *testing.T) {
	reg := newTestRegistry()
	c := NewDashboardCollector(reg)
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	if c.capacity != 3600 {
		t.Fatalf("expected capacity 3600, got %d", c.capacity)
	}
	if len(c.ring) != 3600 {
		t.Fatalf("expected ring length 3600, got %d", len(c.ring))
	}
}

func newTestRegistry() *Registry {
	return New(1, "test-node")
}

func TestDashboardCollectorSample(t *testing.T) {
	reg := newTestRegistry()
	c := NewDashboardCollector(reg)

	// Simulate some activity
	reg.Gateway.MessageReceived("tcp", 100)
	reg.Gateway.MessageReceived("tcp", 200)
	reg.Gateway.MessageDelivered("tcp", 150)
	reg.Gateway.ConnectionOpened("tcp")
	reg.Gateway.ConnectionOpened("tcp")
	reg.Channel.SetActiveChannels(42)
	reg.Message.SetCommittedDispatchQueueDepth("shard-0", 3)
	reg.Message.SetCommittedDispatchQueueDepth("shard-1", 5)
	reg.Message.ObserveAppend("local", "ok", 50*time.Millisecond)
	reg.Message.ObserveAppend("local", "error", 10*time.Millisecond)
	reg.Delivery.ObservePushRPC("node-2", "ok", 100*time.Millisecond, 10)
	reg.Delivery.ObservePushRPC("node-2", "error", 200*time.Millisecond, 0)
	reg.Delivery.ObserveResolve("group", "ok", 5*time.Millisecond, 1, 25)

	sample := c.sample()

	if sample.SendCount != 2 {
		t.Errorf("SendCount: got %v, want 2", sample.SendCount)
	}
	if sample.DeliverCount != 1 {
		t.Errorf("DeliverCount: got %v, want 1", sample.DeliverCount)
	}
	if sample.Connections != 2 {
		t.Errorf("Connections: got %v, want 2", sample.Connections)
	}
	if sample.ActiveChannels != 42 {
		t.Errorf("ActiveChannels: got %v, want 42", sample.ActiveChannels)
	}
	if sample.RetryQueueDepth != 8 {
		t.Errorf("RetryQueueDepth: got %v, want 8", sample.RetryQueueDepth)
	}
	if sample.SendTotalCount != 2 {
		t.Errorf("SendTotalCount: got %v, want 2", sample.SendTotalCount)
	}
	if sample.SendFailCount != 1 {
		t.Errorf("SendFailCount: got %v, want 1", sample.SendFailCount)
	}
	if sample.DeliverTotalCount != 2 {
		t.Errorf("DeliverTotalCount: got %v, want 2", sample.DeliverTotalCount)
	}
	if sample.DeliverFailCount != 1 {
		t.Errorf("DeliverFailCount: got %v, want 1", sample.DeliverFailCount)
	}
	if sample.ResolveRoutesCount != 25 {
		t.Errorf("ResolveRoutesCount: got %v, want 25", sample.ResolveRoutesCount)
	}
	if sample.SendLatencyP99 <= 0 {
		t.Errorf("SendLatencyP99: got %v, want > 0", sample.SendLatencyP99)
	}
	if sample.DeliveryLatencyP99 <= 0 {
		t.Errorf("DeliveryLatencyP99: got %v, want > 0", sample.DeliveryLatencyP99)
	}
}

func TestDashboardCollectorStartStop(t *testing.T) {
	reg := newTestRegistry()
	c := NewDashboardCollector(reg)
	c.Start()
	time.Sleep(1500 * time.Millisecond)
	c.Stop()

	c.mu.RLock()
	count := c.count
	c.mu.RUnlock()

	if count < 1 {
		t.Fatalf("expected at least 1 sample after 1.5s, got %d", count)
	}
}
