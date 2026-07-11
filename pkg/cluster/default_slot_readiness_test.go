package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/slots"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type slotStatusReaderFunc func(multiraft.SlotID) (multiraft.Status, error)

func (f slotStatusReaderFunc) Status(slotID multiraft.SlotID) (multiraft.Status, error) {
	return f(slotID)
}

func TestNodeDefaultSlotReadinessDropsWhenAssignedRuntimeCloses(t *testing.T) {
	snapshot := nodeControlSnapshot()
	snapshot.Nodes = snapshot.Nodes[:1]
	snapshot.Slots[0].DesiredPeers = []uint64{1}

	node, err := New(validNodeConfig(t), withController(control.NewStaticController(snapshot)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	waitUntil(t, func() bool { return node.Snapshot().SlotsReady })
	waitUntil(t, func() bool {
		route, err := node.RouteHashSlot(0)
		return err == nil && route.Leader == 1
	})
	if err := node.defaultSlotRuntime.CloseSlot(context.Background(), multiraft.SlotID(1)); err != nil {
		t.Fatalf("CloseSlot() error = %v", err)
	}
	waitUntil(t, func() bool { return !node.Snapshot().SlotsReady })
	route, err := node.RouteHashSlot(0)
	if err != nil || route.Leader != 1 {
		t.Fatalf("RouteHashSlot() route=%#v error=%v, want last known leader 1 retained", route, err)
	}
}

func TestDefaultSlotStatusesPreserveSuccessfulUnknownLeader(t *testing.T) {
	reader := slotStatusReaderFunc(func(slotID multiraft.SlotID) (multiraft.Status, error) {
		if slotID == 2 {
			return multiraft.Status{}, errors.New("status unavailable")
		}
		if slotID == 3 {
			return multiraft.Status{SlotID: 99, LeaderID: 1}, nil
		}
		return multiraft.Status{SlotID: slotID, LeaderID: 0}, nil
	})

	statuses := defaultSlotStatuses(reader, []uint32{1, 2, 3})
	if len(statuses) != 1 || statuses[0].SlotID != 1 || statuses[0].Leader != 0 {
		t.Fatalf("defaultSlotStatuses() = %#v, want successful Slot 1 with unknown leader", statuses)
	}

	_, localAssignedSlotIDs := defaultSlotReadinessInputs([]control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}}, 1)
	if !localAssignedSlotsReady(localAssignedSlotIDs, statuses) {
		t.Fatal("localAssignedSlotsReady() = false for a successful status with unknown leader")
	}
}

func TestLocalAssignedSlotsReadyIgnoresUnassignedRuntime(t *testing.T) {
	assignments := []control.SlotAssignment{
		{SlotID: 1, DesiredPeers: []uint64{1}},
		{SlotID: 2, DesiredPeers: []uint64{2}},
	}
	_, localAssignedSlotIDs := defaultSlotReadinessInputs(assignments, 1)
	tests := []struct {
		name     string
		statuses []slots.Status
		want     bool
	}{
		{name: "assigned missing", want: false},
		{name: "only unassigned present", statuses: []slots.Status{{SlotID: 2}}, want: false},
		{name: "assigned present", statuses: []slots.Status{{SlotID: 1}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := localAssignedSlotsReady(localAssignedSlotIDs, tt.statuses); got != tt.want {
				t.Fatalf("localAssignedSlotsReady() = %t, want %t", got, tt.want)
			}
		})
	}

	_, noLocalAssignments := defaultSlotReadinessInputs(assignments, 3)
	if !localAssignedSlotsReady(noLocalAssignments, nil) {
		t.Fatal("localAssignedSlotsReady() = false with zero local assignments")
	}
}

func TestUpdateDefaultSlotsReadyRejectsStaleRevision(t *testing.T) {
	node := &Node{
		controlSnapshot: control.Snapshot{Revision: 2},
		snapshot:        Snapshot{StateRevision: 2, SlotsReady: true},
	}

	node.updateDefaultSlotsReady(1, false)
	if !node.Snapshot().SlotsReady {
		t.Fatal("stale revision changed SlotsReady")
	}
	node.updateDefaultSlotsReady(2, false)
	if node.Snapshot().SlotsReady {
		t.Fatal("current revision did not change SlotsReady")
	}
}
