package fsm

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

func semanticBasis(st state.ClusterState) *state.PresenceBasis {
	b := &state.PresenceBasis{Controllers: append([]state.ControllerVoter(nil), st.Controllers...)}
	for _, n := range st.Nodes {
		b.Members = append(b.Members, state.PresenceMember{NodeID: n.NodeID, Addr: n.Addr, JoinState: n.JoinState, Roles: append([]state.NodeRole(nil), n.Roles...)})
	}
	if st.Presence != nil {
		b.RosterRevision = st.Presence.RosterRevision
		b.OwnerSetEpoch = st.Presence.OwnerSet.Epoch
	}
	return b
}
func semanticEnsure(st state.ClusterState) command.Command {
	rev := st.Revision
	c := command.Command{Kind: command.KindEnsurePresenceOwnerSet, ExpectedRevision: &rev, PresenceBasis: semanticBasis(st)}
	for _, n := range st.Nodes {
		if n.JoinState != state.NodeJoinStateRemoved {
			c.PresenceCapabilities = append(c.PresenceCapabilities, state.PresenceCapabilities{NodeID: n.NodeID, BootID: 100 + n.NodeID, ControllerStateVersion: 2, ControllerCommandVersion: 2, PresenceProtocolVersion: 1})
		}
	}
	return c
}
func TestPresenceSemanticBootstrapBindsFullRoster(t *testing.T) {
	for _, mode := range []string{"unrelated_revision", "changed_endpoint", "changed_join", "changed_roles", "changed_voters", "added_removed_tombstone", "unsupported_capability"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			sm, _ := initializedStateMachine(t, 1)
			before := sm.Snapshot(ctx)
			cmd := semanticEnsure(before)
			next := before.Clone()
			next.Revision++
			next.AppliedRaftIndex = 2
			switch mode {
			case "unrelated_revision":
				next.Nodes[2].Name = "operator-label"
			case "changed_endpoint":
				next.Nodes[2].Addr = "replacement-address"
			case "changed_join":
				next.Nodes[2].JoinState = state.NodeJoinStateLeaving
			case "changed_roles":
				next.Nodes[2].Roles = []state.NodeRole{state.NodeRoleData, state.NodeRoleControllerVoter}
			case "changed_voters":
				next.Nodes[2].Roles = append(next.Nodes[2].Roles, state.NodeRoleControllerVoter)
				next.Controllers = append(next.Controllers, state.ControllerVoter{NodeID: 3, Addr: next.Nodes[2].Addr, Role: state.ControllerRoleVoter})
			case "added_removed_tombstone":
				next.Nodes = append(next.Nodes, state.Node{NodeID: 5, Addr: "removed-node-5", JoinState: state.NodeJoinStateRemoved, Roles: []state.NodeRole{state.NodeRoleData}, Status: state.NodeStatusDown, CapacityWeight: 1})
			case "unsupported_capability":
				cmd.PresenceCapabilities[0].PresenceProtocolVersion = 0
			}
			// Restore selects a deterministic pre-activation cut; it is not a lifecycle shortcut.
			if err := sm.Restore(ctx, next); err != nil {
				t.Fatal(err)
			}
			result, err := sm.Apply(ctx, 3, cmd)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unrelated_revision" {
				if result.Rejected || result.PresenceOwnerSet == nil {
					t.Fatalf("same full semantic roster rejected: %+v", result)
				}
				if sm.Snapshot(ctx).Presence.RosterRevision != 3 {
					t.Fatal("first capability-proven activation did not initialize durable roster high water")
				}
			} else if !result.Rejected || sm.Snapshot(ctx).Presence != nil {
				t.Fatalf("changed/unproved membership accepted: %+v", result)
			}
		})
	}
}
func TestPresenceSemanticRosterABAClosesGateAndRequiresNewM(t *testing.T) {
	ctx := context.Background()
	sm, store := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, semanticEnsure(sm.Snapshot(ctx)))
	active := sm.Snapshot(ctx)
	old := semanticEnsure(active)
	original := active.Nodes[2]
	changed := original
	changed.JoinState = state.NodeJoinStateLeaving
	applyOK(t, sm, 3, command.Command{Kind: command.KindUpsertNode, Node: &changed})
	applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertNode, Node: &original})
	aba := sm.Snapshot(ctx)
	if !reflect.DeepEqual(aba.Nodes, active.Nodes) {
		t.Fatal("fixture did not restore exact roster")
	}
	if aba.Presence.RosterRevision != 4 {
		t.Fatalf("semantic ABA high water=%d want4", aba.Presence.RosterRevision)
	}
	if aba.PresenceRosterCurrent(aba.Presence.OwnerSet.Owners) {
		t.Fatal("old M reopened after semantic ABA")
	}
	result, err := sm.Apply(ctx, 5, old)
	if err != nil || !result.Rejected {
		t.Fatalf("old proof accepted after ABA: %+v/%v", result, err)
	}
	result = applyOK(t, sm, 6, semanticEnsure(sm.Snapshot(ctx)))
	if result.PresenceOwnerSet == nil || result.PresenceOwnerSet.Epoch != 6 {
		t.Fatalf("same final vector did not require fresh M: %+v", result)
	}
	restored, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.Load(ctx); err != nil {
		t.Fatal(err)
	}
	current := restored.Snapshot(ctx)
	if current.Presence.RosterRevision != 4 || current.Presence.OwnerSet.Epoch != 6 {
		t.Fatal("restart lost semantic high water")
	}
	rollback := current.Clone()
	rollback.Presence.RosterRevision = 2
	rollback.AppliedRaftIndex = 7
	if err = restored.Restore(ctx, rollback); err == nil {
		t.Fatal("restore accepted roster high-water rollback")
	}
}

func TestPresenceSemanticEnsureRejectsLateOldBootAndMissingStamp(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, semanticEnsure(sm.Snapshot(ctx)))
	observed := sm.Snapshot(ctx)
	old := semanticEnsure(observed)
	newBoot := semanticEnsure(observed)
	newBoot.PresenceCapabilities[0].BootID = 999
	applyOK(t, sm, 3, newBoot)
	// The semantic roster did not change, but another exact boot already won M.
	result, err := sm.Apply(ctx, 4, old)
	if err != nil || !result.Rejected {
		t.Fatalf("late old boot replaced newer M: %+v/%v", result, err)
	}
	current := sm.Snapshot(ctx)
	if current.Presence.OwnerSet.Owners[0].CapabilityGeneration != 999 {
		t.Fatal("late proof rolled owner boot back")
	}
	current.Presence.RosterRevision = 0
	if current.PresenceRosterCurrent(current.Presence.OwnerSet.Owners) {
		t.Fatal("missing old-format stamp silently treated as fresh")
	}
}

func TestPresenceSemanticRestoreCannotLowerRosterHighWater(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, semanticEnsure(sm.Snapshot(ctx)))
	current := sm.Snapshot(ctx)
	current.Presence.RosterRevision = 2
	if err := sm.Restore(ctx, current); err != nil {
		t.Fatal(err)
	}
	older := current.Clone()
	older.Presence.RosterRevision = 1
	older.AppliedRaftIndex = 3
	if err := sm.Restore(ctx, older); err == nil {
		t.Fatal("lower semantic roster high water accepted")
	}
}
