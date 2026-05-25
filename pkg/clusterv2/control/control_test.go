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
		Tasks:     []ReconcileTask{{TaskID: "bootstrap-1", SlotID: 1, Kind: TaskKindBootstrap, TargetNode: 1, TargetPeers: []uint64{1, 2, 3}, ConfigEpoch: 1}},
	}
}
