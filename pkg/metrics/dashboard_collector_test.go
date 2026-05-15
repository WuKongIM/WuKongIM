package metrics

import (
	"testing"
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
