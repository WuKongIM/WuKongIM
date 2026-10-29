package fsm

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

func TestPresenceMembershipRequiresCapabilitiesAndRetainsOwnerObligations(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	st := sm.Snapshot(ctx)
	// Controller-only nodes remain required ingress owners; health is irrelevant.
	st.Nodes[0].Roles = []state.NodeRole{state.NodeRoleControllerVoter}
	st.Nodes[3].Status = state.NodeStatusDown
	if err := sm.Restore(ctx, st); err != nil {
		t.Fatal(err)
	}
	proofs := make([]state.PresenceCapabilities, 0, len(st.Nodes))
	for _, node := range st.Nodes {
		proofs = append(proofs, state.PresenceCapabilities{NodeID: node.NodeID, BootID: node.NodeID + 100, ControllerStateVersion: 2, ControllerCommandVersion: 2, PresenceProtocolVersion: 1})
	}
	revision := st.Revision
	incomplete := command.Command{Kind: command.KindEnsurePresenceOwnerSet, ExpectedRevision: &revision, PresenceBasis: st.PresenceAdmissionBasis(), PresenceCapabilities: proofs[:len(proofs)-1]}
	result, err := sm.Apply(ctx, 2, incomplete)
	if err != nil || !result.Rejected || sm.Snapshot(ctx).Presence != nil {
		t.Fatalf("incomplete proof=%+v/%v", result, err)
	}
	complete := incomplete
	complete.PresenceCapabilities = proofs
	result = applyOK(t, sm, 3, complete)
	if result.PresenceOwnerSet == nil || len(result.PresenceOwnerSet.Owners) != 4 {
		t.Fatal("non-data/down owner omitted")
	}
	st = sm.Snapshot(ctx)
	revision = st.Revision
	newNode := state.Node{NodeID: 5, Addr: "n5", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateJoining, Status: state.NodeStatusAlive, CapacityWeight: 1}
	join := command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &revision, Node: &newNode}
	result, err = sm.Apply(ctx, 4, join)
	if err != nil || !result.Rejected {
		t.Fatalf("unproved post-activation join=%+v/%v", result, err)
	}
	join.PresenceJoiningCapability = &state.PresenceCapabilities{NodeID: 5, BootID: 105, ControllerStateVersion: 2, ControllerCommandVersion: 2, PresenceProtocolVersion: 1}
	applyOK(t, sm, 5, join)
	st = sm.Snapshot(ctx)
	for i := range st.Nodes {
		if st.Nodes[i].NodeID == 4 {
			st.Nodes[i].JoinState = state.NodeJoinStateRemoved
		}
	}
	st.AppliedRaftIndex = 6
	st.Presence.RosterRevision = 6
	if err := sm.Restore(ctx, st); err != nil {
		t.Fatal(err)
	}
	revision = st.Revision
	// Removing a member cannot silently declare its old sessions absent.
	remaining := append([]state.PresenceCapabilities(nil), proofs[:3]...)
	remaining = append(remaining, *join.PresenceJoiningCapability)
	result, err = sm.Apply(ctx, 7, command.Command{Kind: command.KindEnsurePresenceOwnerSet, ExpectedRevision: &revision, PresenceBasis: st.PresenceAdmissionBasis(), PresenceCapabilities: remaining})
	if err != nil || !result.Rejected || result.Reason != ReasonPresenceOwnerObligation {
		t.Fatalf("owner removal=%+v/%v", result, err)
	}
	if len(sm.Snapshot(ctx).Presence.OwnerSet.Owners) != 4 {
		t.Fatal("old owner obligation erased")
	}
}
