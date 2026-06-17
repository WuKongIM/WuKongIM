package clusterv2

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestLocalControlSnapshotReturnsClone(t *testing.T) {
	node := &Node{
		controlSnapshot: control.Snapshot{
			ControllerID: 1,
			Nodes: []control.Node{{
				NodeID: 1,
				Addr:   "127.0.0.1:7011",
				Roles:  []control.Role{control.RoleController, control.RoleData},
				Status: control.NodeAlive,
			}},
		},
	}
	node.started.Store(true)

	got, err := node.LocalControlSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalControlSnapshot() error = %v", err)
	}
	got.Nodes[0].Addr = "mutated"
	got.Nodes[0].Roles[0] = control.RoleData

	again, err := node.LocalControlSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalControlSnapshot() second error = %v", err)
	}
	if again.Nodes[0].Addr != "127.0.0.1:7011" {
		t.Fatalf("snapshot node addr = %q, want original", again.Nodes[0].Addr)
	}
	if again.Nodes[0].Roles[0] != control.RoleController {
		t.Fatalf("snapshot role = %q, want original", again.Nodes[0].Roles[0])
	}
}

func TestLocalControlSnapshotRequiresForegroundNode(t *testing.T) {
	node := &Node{}

	_, err := node.LocalControlSnapshot(context.Background())
	if err != ErrNotStarted {
		t.Fatalf("LocalControlSnapshot() error = %v, want %v", err, ErrNotStarted)
	}
}
