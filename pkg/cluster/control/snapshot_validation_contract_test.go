package control

import (
	"strings"
	"testing"
)

func TestSnapshotValidateRejectsMalformedControlTopology(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Snapshot)
		want string
	}{
		{name: "zero hash slot count", edit: func(s *Snapshot) { s.HashSlots.Count = 0 }, want: "hash slot count"},
		{name: "zero node id", edit: func(s *Snapshot) { s.Nodes[0].NodeID = 0 }, want: "node id"},
		{name: "unknown join state", edit: func(s *Snapshot) { s.Nodes[0].JoinState = "retired" }, want: "join_state"},
		{name: "zero physical slot id", edit: func(s *Snapshot) { s.Slots[0].SlotID = 0 }, want: "slot id"},
		{name: "duplicate physical slot", edit: func(s *Snapshot) { s.Slots = append(s.Slots, s.Slots[0]) }, want: "duplicate slot"},
		{name: "empty desired peers", edit: func(s *Snapshot) { s.Slots[0].DesiredPeers = nil }, want: "no desired peers"},
		{name: "duplicate desired peer", edit: func(s *Snapshot) { s.Slots[0].DesiredPeers = []uint64{1, 1} }, want: "duplicate peer"},
		{name: "preferred leader outside peers", edit: func(s *Snapshot) { s.Slots[0].PreferredLeader = 3; s.Slots[0].DesiredPeers = []uint64{1, 2} }, want: "preferred leader"},
		{name: "empty hash ranges", edit: func(s *Snapshot) { s.HashSlots.Ranges = nil }, want: "ranges must not be empty"},
		{name: "zero hash range target", edit: func(s *Snapshot) { s.HashSlots.Ranges[0].SlotID = 0 }, want: "zero slot"},
		{name: "unknown hash range target", edit: func(s *Snapshot) { s.HashSlots.Ranges[0].SlotID = 99 }, want: "unknown slot"},
		{name: "hash range gap", edit: func(s *Snapshot) { s.HashSlots.Ranges[0].From = 1 }, want: "not contiguous"},
		{name: "hash range reversed", edit: func(s *Snapshot) { s.HashSlots.Ranges[0].To = 0; s.HashSlots.Ranges[0].From = 1 }, want: "not contiguous"},
		{name: "hash range short coverage", edit: func(s *Snapshot) { s.HashSlots.Ranges[0].To = 2 }, want: "cover 3 slots"},
		{name: "invalid task identity", edit: func(s *Snapshot) { s.Tasks[0].TaskID = "" }, want: "invalid task"},
		{name: "task unknown slot", edit: func(s *Snapshot) { s.Tasks[0].SlotID = 99 }, want: "unknown slot"},
		{name: "task progress cardinality", edit: func(s *Snapshot) { s.Tasks[0].ParticipantProgress = s.Tasks[0].ParticipantProgress[:1] }, want: "progress does not match"},
		{name: "enabled ops missing owner", edit: func(s *Snapshot) { s.OpsMCP = &OpsMCPState{Enabled: true, Credentials: []OpsMCPCredential{{ID: "a"}}} }, want: "requires owner and credential"},
		{name: "enabled ops missing credential", edit: func(s *Snapshot) { s.OpsMCP = &OpsMCPState{Enabled: true, OwnerNodeID: 1} }, want: "requires owner and credential"},
		{name: "ops unknown owner", edit: func(s *Snapshot) { s.OpsMCP = &OpsMCPState{OwnerNodeID: 99} }, want: "owner 99 is unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := validSnapshot().Clone()
			tt.edit(&snapshot)
			err := snapshot.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want text %q", err, tt.want)
			}
		})
	}
}
