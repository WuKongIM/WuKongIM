package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
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
	if first.State.Quorum != "ready" || first.State.Sync != "matched" || !first.State.LeaderMatch || first.State.LeaderTransferPending {
		t.Fatalf("first state = %#v, want ready matched leader match without transfer", first.State)
	}
	if !sameUint64Slice(first.Assignment.DesiredPeers, []uint64{1, 2}) || first.Assignment.PreferredLeader != 1 || first.Assignment.ConfigEpoch != 7 || first.Assignment.BalanceVersion != 0 {
		t.Fatalf("first assignment = %#v, want desired [1 2] preferred 1 epoch 7", first.Assignment)
	}
	if !sameUint64Slice(first.Runtime.CurrentPeers, []uint64{1, 2}) ||
		!sameUint64Slice(first.Runtime.CurrentVoters, []uint64{1, 2}) ||
		first.Runtime.LeaderID != 1 ||
		first.Runtime.HealthyVoters != 2 ||
		!first.Runtime.HasQuorum ||
		first.Runtime.ObservedConfigEpoch != 7 ||
		!first.Runtime.LastReportAt.Equal(generatedAt) {
		t.Fatalf("first runtime = %#v, want derived desired runtime", first.Runtime)
	}
	if first.NodeLog != nil {
		t.Fatalf("first NodeLog = %#v, want nil before log migration", first.NodeLog)
	}

	second := got[1]
	if second.SlotID != 2 || second.HashSlots == nil || second.HashSlots.Count != 3 || !sameUint16Slice(second.HashSlots.Items, []uint16{3, 4, 5}) {
		t.Fatalf("second slot = %#v, want slot 2 with hash slots 3,4,5", second)
	}
	if second.Runtime.LeaderID != 2 || second.Runtime.HealthyVoters != 1 || !second.Runtime.HasQuorum {
		t.Fatalf("second runtime = %#v, want single-voter ready runtime", second.Runtime)
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
