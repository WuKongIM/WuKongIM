package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestListSlotsBuildsReadOnlySlotInventory(t *testing.T) {
	generatedAt := time.Date(2026, 6, 16, 11, 0, 0, 0, time.UTC)
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			snapshot: control.Snapshot{
				Slots: []control.SlotAssignment{
					{SlotID: 2, DesiredPeers: []uint64{2}, ConfigEpoch: 5, PreferredLeader: 2},
					{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 7, PreferredLeader: 1},
				},
				HashSlots: control.HashSlotTable{
					Revision: 9,
					Count:    6,
					Ranges: []control.HashSlotRange{
						{From: 0, To: 2, SlotID: 1},
						{From: 3, To: 5, SlotID: 2},
					},
				},
			},
		},
		Now: func() time.Time { return generatedAt },
	})

	got, err := app.ListSlots(context.Background(), ListSlotsOptions{})
	if err != nil {
		t.Fatalf("ListSlots() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("slots len = %d, want 2: %#v", len(got), got)
	}

	first := got[0]
	if first.SlotID != 1 {
		t.Fatalf("first SlotID = %d, want 1", first.SlotID)
	}
	if first.HashSlots == nil || first.HashSlots.Count != 3 || !sameUint16Slice(first.HashSlots.Items, []uint16{0, 1, 2}) {
		t.Fatalf("first hash slots = %#v, want 0,1,2", first.HashSlots)
	}
	if first.State.Quorum != "unknown" || first.State.Sync != "unreported" || first.State.LeaderMatch || first.State.LeaderTransferPending {
		t.Fatalf("first state = %#v, want unknown runtime without fabricated health", first.State)
	}
	if !sameUint64Slice(first.Assignment.DesiredPeers, []uint64{1, 2}) || first.Assignment.PreferredLeader != 1 || first.Assignment.ConfigEpoch != 7 || first.Assignment.BalanceVersion != 0 {
		t.Fatalf("first assignment = %#v, want desired [1 2] preferred 1 epoch 7", first.Assignment)
	}
	if len(first.Runtime.CurrentPeers) != 0 ||
		len(first.Runtime.CurrentVoters) != 0 ||
		first.Runtime.LeaderID != 0 ||
		first.Runtime.PreferredLeaderID != 1 ||
		first.Runtime.HealthyVoters != 0 ||
		first.Runtime.HasQuorum ||
		first.Runtime.ObservedConfigEpoch != 0 ||
		!first.Runtime.LastReportAt.IsZero() {
		t.Fatalf("first runtime = %#v, want preferred intent with unknown live runtime", first.Runtime)
	}
	if first.NodeLog != nil {
		t.Fatalf("first NodeLog = %#v, want nil before log migration", first.NodeLog)
	}

	second := got[1]
	if second.SlotID != 2 || second.HashSlots == nil || second.HashSlots.Count != 3 || !sameUint16Slice(second.HashSlots.Items, []uint16{3, 4, 5}) {
		t.Fatalf("second slot = %#v, want slot 2 with hash slots 3,4,5", second)
	}
	if second.Runtime.PreferredLeaderID != 2 || second.Runtime.HealthyVoters != 0 || second.Runtime.HasQuorum {
		t.Fatalf("second runtime = %#v, want preferred intent with unknown live runtime", second.Runtime)
	}
}

func TestListSlotsIncludesSelectedNodeRaftStatus(t *testing.T) {
	generatedAt := time.Date(2026, 6, 16, 11, 0, 0, 0, time.UTC)
	slotRaft := &fakeSlotRaftStatusReader{
		statuses: map[slotRaftStatusKey]SlotNodeLogStatus{
			{nodeID: 1, slotID: 9}: {
				NodeID:        1,
				LeaderID:      1,
				Role:          "leader",
				CurrentVoters: []uint64{1, 2},
				CommitIndex:   93,
				AppliedIndex:  91,
			},
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			snapshot: control.Snapshot{
				Slots: []control.SlotAssignment{
					{SlotID: 9, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 7, PreferredLeader: 1},
				},
			},
		},
		SlotRaft: slotRaft,
		Now:      func() time.Time { return generatedAt },
	})

	got, err := app.ListSlots(context.Background(), ListSlotsOptions{NodeID: 1})
	if err != nil {
		t.Fatalf("ListSlots() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("slots len = %d, want 1: %#v", len(got), got)
	}

	nodeLog := got[0].NodeLog
	if nodeLog == nil {
		t.Fatalf("NodeLog = nil, want selected node Raft status")
	}
	if nodeLog.NodeID != 1 || nodeLog.LeaderID != 1 || nodeLog.Role != "leader" || nodeLog.CommitIndex != 93 || nodeLog.AppliedIndex != 91 {
		t.Fatalf("NodeLog = %#v, want node 1 leader role with commit/applied watermarks", nodeLog)
	}
	if got[0].Runtime.LeaderID != 1 || !sameUint64Slice(got[0].Runtime.CurrentVoters, []uint64{1, 2}) || !got[0].Runtime.HasQuorum {
		t.Fatalf("Runtime = %#v, want live leader and voter evidence", got[0].Runtime)
	}
	if slotRaft.requests != 1 {
		t.Fatalf("slot raft status requests = %d, want 1", slotRaft.requests)
	}
}

func TestListSlotsUsesLiveDefaultObservationForLeaderMatch(t *testing.T) {
	generatedAt := time.Date(2026, 6, 16, 11, 0, 0, 0, time.UTC)
	slotRaft := &fakeSlotRaftStatusReader{
		statuses: map[slotRaftStatusKey]SlotNodeLogStatus{
			{nodeID: 1, slotID: 9}: {
				NodeID:        1,
				LeaderID:      2,
				Role:          "follower",
				CurrentVoters: []uint64{1, 2, 3},
			},
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{
				Slots: []control.SlotAssignment{
					{SlotID: 9, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
				},
			},
		},
		SlotRaft: slotRaft,
		Now:      func() time.Time { return generatedAt },
	})

	got, err := app.ListSlots(context.Background(), ListSlotsOptions{})
	if err != nil {
		t.Fatalf("ListSlots() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("slots len = %d, want 1", len(got))
	}
	if got[0].State.Quorum != "ready" || got[0].State.Sync != "matched" || got[0].State.LeaderMatch {
		t.Fatalf("State = %#v, want ready matched with actual/preferred leader mismatch", got[0].State)
	}
	if got[0].Runtime.LeaderID != 2 || got[0].Runtime.PreferredLeaderID != 1 || !got[0].Runtime.LastReportAt.Equal(generatedAt) {
		t.Fatalf("Runtime = %#v, want actual leader 2 and preferred leader 1", got[0].Runtime)
	}
	if got[0].NodeLog != nil {
		t.Fatalf("NodeLog = %#v, want hidden default observation", got[0].NodeLog)
	}
}

func TestListSlotsIncludesActiveTaskProgress(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			snapshot: control.Snapshot{
				Revision:  1,
				Nodes:     []control.Node{{NodeID: 1, Addr: "n1", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive}},
				Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1}},
				HashSlots: control.HashSlotTable{Revision: 1, Count: 1, Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 1}}},
				Tasks: []control.ReconcileTask{{
					TaskID:           "slot-1-bootstrap-1",
					SlotID:           1,
					Kind:             control.TaskKindBootstrap,
					Step:             control.TaskStepCreateSlot,
					TargetNode:       1,
					TargetPeers:      []uint64{1},
					CompletionPolicy: control.TaskCompletionPolicyAllTargetPeers,
					ParticipantProgress: []control.TaskParticipantProgress{
						{NodeID: 1, Status: control.TaskParticipantStatusPending},
					},
					ConfigEpoch:         1,
					Status:              control.TaskStatusPending,
					PhaseIndex:          2,
					ObservedConfigIndex: 42,
					ObservedVoters:      []uint64{1, 2},
					ObservedLearners:    []uint64{3},
				}},
			},
		},
	})

	items, err := app.ListSlots(context.Background(), ListSlotsOptions{})

	if err != nil {
		t.Fatalf("ListSlots() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("slots len = %d, want 1", len(items))
	}
	if items[0].Task == nil {
		t.Fatalf("Task = nil, want active task summary")
	}
	if items[0].Task.TaskID != "slot-1-bootstrap-1" || items[0].Task.CompletionPolicy != "all_target_peers" {
		t.Fatalf("Task = %#v, want bootstrap task summary", items[0].Task)
	}
	if items[0].Task.PhaseIndex != 2 ||
		items[0].Task.ObservedConfigIndex != 42 ||
		!sameUint64Slice(items[0].Task.ObservedVoters, []uint64{1, 2}) ||
		!sameUint64Slice(items[0].Task.ObservedLearners, []uint64{3}) {
		t.Fatalf("Task proof = %#v, want phase 2 config 42 voters [1 2] learners [3]", items[0].Task)
	}
	if len(items[0].Task.Participants) != 1 || items[0].Task.Participants[0].NodeID != 1 {
		t.Fatalf("Participants = %#v, want node 1 participant", items[0].Task.Participants)
	}
}

func TestSlotTaskFromControlPreservesProofFieldsAndClonesSlices(t *testing.T) {
	task := control.ReconcileTask{
		TaskID:              "slot-9-replica-move-7",
		SlotID:              9,
		Kind:                control.TaskKindSlotReplicaMove,
		Step:                control.TaskStepPromoteLearner,
		TargetPeers:         []uint64{1, 2, 3},
		PhaseIndex:          3,
		ObservedConfigIndex: 77,
		ObservedVoters:      []uint64{1, 2, 3},
		ObservedLearners:    []uint64{4},
		Status:              control.TaskStatusRunning,
	}

	first := slotTaskFromControl(task)
	if first.PhaseIndex != 3 ||
		first.ObservedConfigIndex != 77 ||
		!sameUint64Slice(first.ObservedVoters, []uint64{1, 2, 3}) ||
		!sameUint64Slice(first.ObservedLearners, []uint64{4}) {
		t.Fatalf("first proof = %#v, want copied proof fields", first)
	}
	first.ObservedVoters[0] = 99
	first.ObservedLearners[0] = 99

	second := slotTaskFromControl(task)
	if !sameUint64Slice(second.ObservedVoters, []uint64{1, 2, 3}) {
		t.Fatalf("ObservedVoters = %#v, want cloned [1 2 3]", second.ObservedVoters)
	}
	if !sameUint64Slice(second.ObservedLearners, []uint64{4}) {
		t.Fatalf("ObservedLearners = %#v, want cloned [4]", second.ObservedLearners)
	}
}

func TestListSlotsFiltersByNode(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			snapshot: control.Snapshot{
				Slots: []control.SlotAssignment{
					{SlotID: 1, DesiredPeers: []uint64{1, 2}, PreferredLeader: 1},
					{SlotID: 2, DesiredPeers: []uint64{2}, PreferredLeader: 2},
				},
			},
		},
	})

	got, err := app.ListSlots(context.Background(), ListSlotsOptions{NodeID: 1})
	if err != nil {
		t.Fatalf("ListSlots() error = %v", err)
	}
	if len(got) != 1 || got[0].SlotID != 1 {
		t.Fatalf("filtered slots = %#v, want only slot 1", got)
	}

	empty, err := app.ListSlots(context.Background(), ListSlotsOptions{NodeID: 3})
	if err != nil {
		t.Fatalf("ListSlots() for node 3 error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("filtered node 3 slots = %#v, want empty", empty)
	}
}

type slotRaftStatusKey struct {
	nodeID uint64
	slotID uint32
}

type fakeSlotRaftStatusReader struct {
	statuses map[slotRaftStatusKey]SlotNodeLogStatus
	requests int
}

func (f *fakeSlotRaftStatusReader) SlotRaftStatus(_ context.Context, nodeID uint64, slotID uint32) (SlotNodeLogStatus, error) {
	f.requests++
	return f.statuses[slotRaftStatusKey{nodeID: nodeID, slotID: slotID}], nil
}

func (f *fakeSlotRaftStatusReader) CompactSlotRaftLog(_ context.Context, nodeID uint64, slotID uint32) (SlotRaftCompactionResult, error) {
	return SlotRaftCompactionResult{NodeID: nodeID, SlotID: slotID}, nil
}

func TestListSlotsReturnsClusterSnapshotError(t *testing.T) {
	wantErr := errors.New("control unavailable")
	app := New(Options{Cluster: fakeNodeSnapshotReader{err: wantErr}})

	_, err := app.ListSlots(context.Background(), ListSlotsOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ListSlots() error = %v, want %v", err, wantErr)
	}
}

func sameUint16Slice(left, right []uint16) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameUint64Slice(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
