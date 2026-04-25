package cluster

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestStaticDiscovery_Resolve(t *testing.T) {
	d := NewStaticDiscovery([]NodeConfig{
		{NodeID: 1, Addr: "10.0.0.1:9001"},
		{NodeID: 2, Addr: "10.0.0.2:9001"},
	})

	addr, err := d.Resolve(1)
	if err != nil {
		t.Fatalf("Resolve(1): %v", err)
	}
	if addr != "10.0.0.1:9001" {
		t.Fatalf("expected 10.0.0.1:9001, got %s", addr)
	}

	_, err = d.Resolve(99)
	if !errors.Is(err, transport.ErrNodeNotFound) {
		t.Fatalf("expected transport.ErrNodeNotFound, got: %v", err)
	}
}

func TestStaticDiscovery_GetNodes(t *testing.T) {
	d := NewStaticDiscovery([]NodeConfig{
		{NodeID: 1, Addr: "a"},
		{NodeID: 2, Addr: "b"},
	})
	nodes := d.GetNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}
