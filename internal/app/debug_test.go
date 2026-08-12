package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestDebugConfigSnapshotIncludesFormalPreflightValues(t *testing.T) {
	cfg := Config{NodeID: 2}
	cfg.Cluster.Slots.InitialSlotCount = 12
	cfg.Cluster.Slots.HashSlotCount = 256
	cfg.Cluster.Slots.ReplicaCount = 3
	cfg.Cluster.Channel.ReplicaCount = 3
	cfg.Cluster.Channel.MaxChannels = 50_000

	snapshot := (&App{cfg: cfg}).debugConfigSnapshot().(map[string]any)
	for key, want := range map[string]any{
		"initial_slot_count": uint32(12), "hash_slot_count": uint16(256),
		"slot_replica_count": uint16(3), "channel_replica_count": uint16(3), "channel_max_loaded_count": 50_000,
	} {
		if got := snapshot[key]; got != want {
			t.Fatalf("debug config %s = %#v, want %#v", key, got, want)
		}
	}
}

func TestDebugClusterSnapshotUsesLiveSlotRaftFacts(t *testing.T) {
	runtime := &debugClusterRuntimeStub{
		control: control.Snapshot{Revision: 9, Slots: []control.SlotAssignment{{
			SlotID: 4, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 3,
		}}},
		statuses: map[uint32]clusterpkg.SlotRaftStatus{4: {
			NodeID: 1, SlotID: 4, LeaderID: 1, Role: "leader", Term: 7,
			CommitIndex: 100, AppliedIndex: 100, CurrentVoters: []uint64{1, 2, 3},
			ReplicaProgressComplete: true,
			ReplicaProgress: []clusterpkg.SlotRaftReplicaProgress{
				{NodeID: 1, MatchIndex: 100, NextIndex: 101, State: "StateReplicate"},
				{NodeID: 2, MatchIndex: 99, NextIndex: 100, State: "StateReplicate"},
				{NodeID: 3, MatchIndex: 98, NextIndex: 99, State: "StateProbe"},
			},
		}},
	}
	app := &App{cfg: Config{NodeID: 1}, cluster: runtime}

	got, err := app.debugClusterSnapshot(context.Background())
	if err != nil {
		t.Fatalf("debugClusterSnapshot() error = %v", err)
	}
	want := debugClusterResponse{NodeID: 1, StateRevision: 9, Slots: []debugClusterSlot{{
		SlotID: 4, LeaderID: 1, Replicas: []uint64{1, 2, 3}, Voters: []uint64{1, 2, 3},
		Term: 7, CommitIndex: 100, AppliedIndex: 100,
		ReplicaProgress: []debugReplicaProgress{
			{NodeID: 1, MatchIndex: 100, State: "StateReplicate"},
			{NodeID: 2, MatchIndex: 99, LagEntries: 1, State: "StateReplicate"},
			{NodeID: 3, MatchIndex: 98, LagEntries: 2, State: "StateProbe"},
		},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("debugClusterSnapshot() = %#v, want %#v", got, want)
	}
	if got.Slots[0].LeaderID == runtime.control.Slots[0].PreferredLeader {
		t.Fatal("debug cluster used configured preferred leader instead of live Raft leader")
	}
}

func TestDebugClusterSnapshotRejectsIncompleteOrInvalidProgress(t *testing.T) {
	tests := []clusterpkg.SlotRaftStatus{
		{NodeID: 1, SlotID: 1, LeaderID: 1, Role: "leader", CommitIndex: 10, ReplicaProgressComplete: false},
		{NodeID: 1, SlotID: 1, LeaderID: 1, Role: "leader", CommitIndex: 10, ReplicaProgressComplete: true, ReplicaProgress: []clusterpkg.SlotRaftReplicaProgress{{NodeID: 2, MatchIndex: 10, State: "future-state"}}},
	}
	for _, status := range tests {
		runtime := &debugClusterRuntimeStub{
			control:  control.Snapshot{Revision: 1, Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}}}},
			statuses: map[uint32]clusterpkg.SlotRaftStatus{1: status},
		}
		_, err := (&App{cfg: Config{NodeID: 1}, cluster: runtime}).debugClusterSnapshot(context.Background())
		if err == nil {
			t.Fatalf("debugClusterSnapshot() error = nil for status %#v", status)
		}
	}
}

func TestDebugClusterSnapshotAllowsReplicaProgressAheadOfCommit(t *testing.T) {
	runtime := &debugClusterRuntimeStub{
		control: control.Snapshot{Revision: 1, Slots: []control.SlotAssignment{{SlotID: 7, DesiredPeers: []uint64{1, 2, 3}}}},
		statuses: map[uint32]clusterpkg.SlotRaftStatus{7: {
			NodeID: 1, SlotID: 7, LeaderID: 1, Role: "leader", CommitIndex: 10,
			ReplicaProgressComplete: true,
			ReplicaProgress:         []clusterpkg.SlotRaftReplicaProgress{{NodeID: 2, MatchIndex: 11, State: "StateReplicate"}},
		}},
	}

	got, err := (&App{cfg: Config{NodeID: 1}, cluster: runtime}).debugClusterSnapshot(context.Background())
	if err != nil {
		t.Fatalf("debugClusterSnapshot() error = %v", err)
	}
	if progress := got.Slots[0].ReplicaProgress[0]; progress.MatchIndex != 11 || progress.LagEntries != 0 {
		t.Fatalf("replica progress = %+v, want replicated-ahead match with zero committed lag", progress)
	}
}

func TestDebugClusterSnapshotReplicaProgressErrorIncludesBoundedRaftFacts(t *testing.T) {
	runtime := &debugClusterRuntimeStub{
		control: control.Snapshot{Revision: 1, Slots: []control.SlotAssignment{{SlotID: 7, DesiredPeers: []uint64{1, 2, 3}}}},
		statuses: map[uint32]clusterpkg.SlotRaftStatus{7: {
			NodeID: 1, SlotID: 7, LeaderID: 1, Role: "leader", CommitIndex: 10,
			ReplicaProgressComplete: true,
			ReplicaProgress:         []clusterpkg.SlotRaftReplicaProgress{{NodeID: 2, MatchIndex: 10, State: "future-state"}},
		}},
	}

	_, err := (&App{cfg: Config{NodeID: 1}, cluster: runtime}).debugClusterSnapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), `slot=7 replica=2 previous_replica=0 match=10 commit=10 state="future-state"`) {
		t.Fatalf("debugClusterSnapshot() error = %v, want bounded Raft facts", err)
	}
}

type debugClusterRuntimeStub struct {
	control  control.Snapshot
	statuses map[uint32]clusterpkg.SlotRaftStatus
}

func (s *debugClusterRuntimeStub) Start(context.Context) error { return nil }
func (s *debugClusterRuntimeStub) Stop(context.Context) error  { return nil }
func (s *debugClusterRuntimeStub) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return s.control.Clone(), nil
}
func (s *debugClusterRuntimeStub) LocalSlotRaftStatus(_ context.Context, slotID uint32) (clusterpkg.SlotRaftStatus, error) {
	return s.statuses[slotID], nil
}
