package management

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNodeConfigSnapshotReadsSelectedExistingNode(t *testing.T) {
	cluster := &nodeConfigControlReader{
		nodeID: 1,
		snapshot: control.Snapshot{
			Nodes: []control.Node{
				{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			},
		},
	}
	reader := &nodeConfigReaderStub{
		snapshot: NodeConfigSnapshot{
			GeneratedAt:     time.Unix(10, 0).UTC(),
			NodeID:          2,
			Source:          NodeConfigSnapshotSourceEffectiveStartup,
			RequiresRestart: true,
			Groups: []NodeConfigGroup{{
				ID:    "cluster",
				Title: "Cluster",
				Items: []NodeConfigItem{{
					Key:   "WK_CLUSTER_HASH_SLOT_COUNT",
					Label: "Hash slot count",
					Value: "256",
				}},
			}},
		},
	}
	app := New(Options{Cluster: cluster, NodeConfig: reader})

	got, err := app.NodeConfigSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot() error = %v", err)
	}
	if reader.nodeID != 2 {
		t.Fatalf("reader nodeID = %d, want 2", reader.nodeID)
	}
	if got.NodeID != 2 || got.Source != NodeConfigSnapshotSourceEffectiveStartup || len(got.Groups) != 1 {
		t.Fatalf("snapshot = %#v, want node 2 effective snapshot", got)
	}
}

func TestNodeConfigSnapshotRejectsMissingNodeBeforeReader(t *testing.T) {
	reader := &nodeConfigReaderStub{}
	app := New(Options{
		Cluster: &nodeConfigControlReader{
			nodeID:   1,
			snapshot: control.Snapshot{Nodes: []control.Node{{NodeID: 1}}},
		},
		NodeConfig: reader,
	})

	_, err := app.NodeConfigSnapshot(context.Background(), 9)
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("NodeConfigSnapshot() error = %v, want metadb.ErrNotFound", err)
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d, want 0", reader.calls)
	}
}

func TestNodeConfigSnapshotRejectsInvalidNodeID(t *testing.T) {
	app := New(Options{Cluster: &nodeConfigControlReader{nodeID: 1}, NodeConfig: &nodeConfigReaderStub{}})

	_, err := app.NodeConfigSnapshot(context.Background(), 0)
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("NodeConfigSnapshot(0) error = %v, want metadb.ErrInvalidArgument", err)
	}
}

func TestNodeConfigSnapshotUnavailableWithoutReader(t *testing.T) {
	app := New(Options{
		Cluster: &nodeConfigControlReader{
			nodeID:   1,
			snapshot: control.Snapshot{Nodes: []control.Node{{NodeID: 1}}},
		},
	})

	_, err := app.NodeConfigSnapshot(context.Background(), 1)
	if !errors.Is(err, ErrNodeConfigUnavailable) {
		t.Fatalf("NodeConfigSnapshot() error = %v, want ErrNodeConfigUnavailable", err)
	}
}

func TestNodeConfigSnapshotUnavailableWithoutClusterBeforeReader(t *testing.T) {
	reader := &nodeConfigReaderStub{}
	app := New(Options{NodeConfig: reader})

	_, err := app.NodeConfigSnapshot(context.Background(), 1)
	if !errors.Is(err, ErrNodeConfigUnavailable) {
		t.Fatalf("NodeConfigSnapshot() error = %v, want ErrNodeConfigUnavailable", err)
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d, want 0", reader.calls)
	}
}

func TestNodeConfigDTOsHaveNoEntryTags(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(NodeConfigSnapshot{}),
		reflect.TypeOf(NodeConfigGroup{}),
		reflect.TypeOf(NodeConfigItem{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if tag := field.Tag.Get("json"); tag != "" {
				t.Fatalf("%s.%s json tag = %q, want empty entry-independent usecase field", typ.Name(), field.Name, tag)
			}
		}
	}
}

type nodeConfigControlReader struct {
	nodeID   uint64
	snapshot control.Snapshot
}

func (r *nodeConfigControlReader) NodeID() uint64 { return r.nodeID }

func (r *nodeConfigControlReader) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return r.snapshot, nil
}

type nodeConfigReaderStub struct {
	calls    int
	nodeID   uint64
	snapshot NodeConfigSnapshot
	err      error
}

func (r *nodeConfigReaderStub) NodeConfigSnapshot(_ context.Context, nodeID uint64) (NodeConfigSnapshot, error) {
	r.calls++
	r.nodeID = nodeID
	if r.err != nil {
		return NodeConfigSnapshot{}, r.err
	}
	return r.snapshot, nil
}
