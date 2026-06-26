package control

import (
	"context"
	"testing"
)

func TestSnapshotValidateRejectsInvalidHashSlotCoverage(t *testing.T) {
	snap := Snapshot{Revision: 1, HashSlots: HashSlotTable{Count: 2, Ranges: []HashSlotRange{{From: 0, To: 0, SlotID: 1}}}}
	if err := snap.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want invalid coverage")
	}
}

func TestSnapshotValidateRejectsDuplicateNodes(t *testing.T) {
	snap := validSnapshot()
	snap.Nodes = append(snap.Nodes, snap.Nodes[0])
	if err := snap.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate node rejection")
	}
}

func TestSnapshotValidateRejectsSlotPeerWithoutDataNode(t *testing.T) {
	snap := validSnapshot()
	snap.Slots[0].DesiredPeers = []uint64{1, 4}
	if err := snap.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want unknown data peer rejection")
	}
}

func TestSnapshotValidateRejectsSlotPeerThatIsNotActive(t *testing.T) {
	snap := validSnapshot()
	snap.Nodes[1].JoinState = NodeJoinStateJoining
	if err := snap.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want joining desired peer rejection")
	}
}

func TestSnapshotValidateAllowsLeavingDataSlotPeer(t *testing.T) {
	snap := validSnapshot()
	snap.Nodes[1].JoinState = NodeJoinStateLeaving

	if err := snap.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want leaving desired peer allowed", err)
	}
}

func TestStaticControllerPublishesSnapshot(t *testing.T) {
	initial := validSnapshot()
	c := NewStaticController(initial)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	got, err := c.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot() error = %v", err)
	}
	if got.Revision != initial.Revision {
		t.Fatalf("revision = %d, want %d", got.Revision, initial.Revision)
	}

	next := initial
	next.Revision = 2
	if err := c.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case ev := <-c.Watch():
		if ev.Snapshot.Revision != 2 {
			t.Fatalf("event revision = %d, want 2", ev.Snapshot.Revision)
		}
	default:
		t.Fatal("missing snapshot event")
	}
}

func TestStaticControllerRecordsReports(t *testing.T) {
	c := NewStaticController(validSnapshot())
	if err := c.ReportNode(context.Background(), NodeReport{NodeID: 2, Addr: "127.0.0.1:1002"}); err != nil {
		t.Fatalf("ReportNode() error = %v", err)
	}
	if err := c.ReportSlots(context.Background(), SlotRuntimeReport{NodeID: 2, Slots: []SlotRuntimeView{{SlotID: 1, Leader: 2}}}); err != nil {
		t.Fatalf("ReportSlots() error = %v", err)
	}
	if got := c.LastNodeReport(); got.NodeID != 2 {
		t.Fatalf("LastNodeReport().NodeID = %d, want 2", got.NodeID)
	}
	if got := c.LastSlotReport(); got.NodeID != 2 || len(got.Slots) != 1 {
		t.Fatalf("LastSlotReport() = %#v, want node 2 with one slot", got)
	}
}

func TestStaticControllerRecordsTaskWrites(t *testing.T) {
	c := NewStaticController(validSnapshot())
	progress := TaskProgress{
		TaskID:             "bootstrap-1",
		SlotID:             1,
		TaskKind:           TaskKindBootstrap,
		ConfigEpoch:        1,
		TaskAttempt:        0,
		ParticipantNodeID:  2,
		ParticipantAttempt: 0,
		Status:             TaskParticipantStatusDone,
	}
	if err := c.ReportTaskProgress(context.Background(), progress); err != nil {
		t.Fatalf("ReportTaskProgress() error = %v", err)
	}
	completed := TaskResult{TaskID: "bootstrap-1", SlotID: 1, TaskKind: TaskKindBootstrap, ConfigEpoch: 1}
	if err := c.CompleteTask(context.Background(), completed); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	failed := TaskResult{TaskID: "bootstrap-2", SlotID: 2, TaskKind: TaskKindBootstrap, ConfigEpoch: 2, Err: "boom"}
	if err := c.FailTask(context.Background(), failed); err != nil {
		t.Fatalf("FailTask() error = %v", err)
	}
	transfer := SlotLeaderTransferRequest{SlotID: 1, SourceNode: 1, TargetNode: 2, TargetPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, StateRevision: 9}
	transferResult, err := c.RequestSlotLeaderTransfer(context.Background(), transfer)
	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v", err)
	}
	if len(c.ProgressReports) != 1 || c.ProgressReports[0].ParticipantNodeID != 2 {
		t.Fatalf("ProgressReports = %#v, want one node 2 report", c.ProgressReports)
	}
	if len(c.CompletedTasks) != 1 || c.CompletedTasks[0].TaskID != "bootstrap-1" {
		t.Fatalf("CompletedTasks = %#v, want bootstrap-1", c.CompletedTasks)
	}
	if len(c.FailedTasks) != 1 || c.FailedTasks[0].Err != "boom" {
		t.Fatalf("FailedTasks = %#v, want boom failure", c.FailedTasks)
	}
	if len(c.LeaderTransfers) != 1 || c.LeaderTransfers[0].TargetNode != 2 {
		t.Fatalf("LeaderTransfers = %#v, want target node 2", c.LeaderTransfers)
	}
	if !transferResult.Created || transferResult.Task == nil || transferResult.Task.TaskID != "slot-1-leader-transfer-7-r9" {
		t.Fatalf("RequestSlotLeaderTransfer() = %#v, want deterministic task", transferResult)
	}
}

func validSnapshot() Snapshot {
	return Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []Role{RoleController, RoleData}, Status: NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []Role{RoleData}, Status: NodeAlive},
			{NodeID: 3, Addr: "127.0.0.1:1003", Roles: []Role{RoleData}, Status: NodeAlive},
		},
		Slots:     []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: HashSlotTable{Revision: 1, Count: 4, Ranges: []HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		Tasks: []ReconcileTask{{
			TaskID:           "bootstrap-1",
			SlotID:           1,
			Kind:             TaskKindBootstrap,
			Step:             TaskStepCreateSlot,
			TargetNode:       1,
			TargetPeers:      []uint64{1, 2, 3},
			CompletionPolicy: TaskCompletionPolicyAllTargetPeers,
			ParticipantProgress: []TaskParticipantProgress{
				{NodeID: 1, Status: TaskParticipantStatusPending},
				{NodeID: 2, Status: TaskParticipantStatusPending},
				{NodeID: 3, Status: TaskParticipantStatusPending},
			},
			ConfigEpoch: 1,
			Status:      TaskStatusPending,
		}},
	}
}
