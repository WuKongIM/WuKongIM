package raft

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"go.etcd.io/raft/v3/raftpb"
)

type configStateMachineStub struct{}

func (configStateMachineStub) Load(context.Context) error { return nil }
func (configStateMachineStub) Reset()                     {}
func (configStateMachineStub) Restore(context.Context, state.ClusterState) error {
	return nil
}
func (configStateMachineStub) Snapshot(context.Context) state.ClusterState {
	return state.ClusterState{}
}
func (configStateMachineStub) IsDegraded() bool { return false }
func (configStateMachineStub) ApplyBatch(context.Context, []fsm.AppliedCommand) (fsm.BatchApplyResult, error) {
	return fsm.BatchApplyResult{}, nil
}

type configTransportStub struct{}

func (configTransportStub) Send([]raftpb.Message) {}

func TestConfigNormalizationAppliesOperationalDefaultsAndOwnsPeerOrder(t *testing.T) {
	peers := []Peer{{NodeID: 3, Addr: "n3"}, {NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}}
	cfg := Config{Peers: peers}.normalized()
	if cfg.TickInterval != defaultTickInterval || cfg.MaxApplyBatchEntries != defaultMaxApplyBatchEntries ||
		cfg.MaxApplyBatchBytes != defaultMaxApplyBatchBytes || cfg.MaxApplyDelay != defaultMaxApplyDelay ||
		cfg.WALSegmentSize != defaultWALSegmentSize || cfg.SnapshotCount != defaultSnapshotCount ||
		cfg.SnapshotCatchUpEntries != defaultSnapshotCatchUp || cfg.SnapshotMinInterval != defaultSnapshotMinInterval {
		t.Fatalf("normalized defaults = %+v", cfg)
	}
	if cfg.Peers[0].NodeID != 1 || cfg.Peers[1].NodeID != 2 || cfg.Peers[2].NodeID != 3 {
		t.Fatalf("normalized peers = %+v", cfg.Peers)
	}
	if peers[0].NodeID != 3 || peers[1].NodeID != 1 || peers[2].NodeID != 2 {
		t.Fatalf("normalization mutated caller peers = %+v", peers)
	}
}

func TestConfigValidationRejectsEveryUnsafeRuntimeBoundary(t *testing.T) {
	valid := func() Config {
		return Config{
			NodeID: 1, Peers: []Peer{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}},
			RaftDir: t.TempDir(), StateMachine: configStateMachineStub{}, Transport: configTransportStub{},
			TickInterval: time.Millisecond, MaxApplyBatchEntries: 8, MaxApplyBatchBytes: 1024,
			MaxApplyDelay: time.Millisecond, WALSegmentSize: 4096, SnapshotCount: 10,
			SnapshotCatchUpEntries: 5, SnapshotMinInterval: time.Millisecond,
		}
	}
	if err := valid().validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "zero local node", mutate: func(c *Config) { c.NodeID = 0 }},
		{name: "empty peers", mutate: func(c *Config) { c.Peers = nil }},
		{name: "zero peer", mutate: func(c *Config) { c.Peers[1].NodeID = 0 }},
		{name: "empty peer address", mutate: func(c *Config) { c.Peers[1].Addr = "" }},
		{name: "duplicate peer", mutate: func(c *Config) { c.Peers[1].NodeID = 1 }},
		{name: "missing self", mutate: func(c *Config) { c.NodeID = 3 }},
		{name: "empty raft directory", mutate: func(c *Config) { c.RaftDir = "" }},
		{name: "nil state machine", mutate: func(c *Config) { c.StateMachine = nil }},
		{name: "nil transport", mutate: func(c *Config) { c.Transport = nil }},
		{name: "non-positive tick", mutate: func(c *Config) { c.TickInterval = 0 }},
		{name: "non-positive batch entries", mutate: func(c *Config) { c.MaxApplyBatchEntries = 0 }},
		{name: "zero batch bytes", mutate: func(c *Config) { c.MaxApplyBatchBytes = 0 }},
		{name: "non-positive apply delay", mutate: func(c *Config) { c.MaxApplyDelay = 0 }},
		{name: "zero WAL segment", mutate: func(c *Config) { c.WALSegmentSize = 0 }},
		{name: "catch-up exceeds snapshot", mutate: func(c *Config) { c.SnapshotCatchUpEntries = 11 }},
		{name: "non-positive snapshot interval", mutate: func(c *Config) { c.SnapshotMinInterval = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid()
			cfg.Peers = append([]Peer(nil), cfg.Peers...)
			test.mutate(&cfg)
			if err := cfg.validate(); err == nil {
				t.Fatal("validate() error = nil")
			}
		})
	}
}
