package fsm

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

func channelPromiseActivation(st state.ClusterState) command.Command {
	rev := st.Revision
	cmd := command.Command{Kind: command.KindActivateChannelAuthorityPromise, ExpectedRevision: &rev}
	for _, node := range st.Nodes {
		if node.JoinState != state.NodeJoinStateRemoved {
			cmd.ChannelPromiseCapabilities = append(cmd.ChannelPromiseCapabilities, state.ChannelReplicationCapability{
				NodeID: node.NodeID, ProtocolVersion: state.ChannelAuthorityPromiseProtocolVersion,
			})
		}
	}
	return cmd
}

// One-way activation of durable authority promises (the distributed log
// sequence is message_seq; docs/superpowers/specs/2026-11-09-log-sequence-is-message-seq-design.md).
// Failure cases: activating while any member (including Controller voters and
// mirrors, which decode Controller state strictly) is unproven or older;
// re-writing an active section; admitting an unproven joiner afterwards.
func TestChannelAuthorityPromiseActivationIsProvenAndOneWay(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	st := sm.Snapshot(ctx)

	missing := channelPromiseActivation(st)
	missing.ChannelPromiseCapabilities = missing.ChannelPromiseCapabilities[1:]
	if result, err := sm.Apply(ctx, 2, missing); err != nil || !result.Rejected || sm.Snapshot(ctx).ChannelAuthorityPromise != nil {
		t.Fatalf("activation without every member = %+v/%v", result, err)
	}
	older := channelPromiseActivation(st)
	older.ChannelPromiseCapabilities[0].ProtocolVersion = state.ChannelAuthorityPromiseProtocolVersion - 1
	if result, err := sm.Apply(ctx, 3, older); err != nil || !result.Rejected || sm.Snapshot(ctx).ChannelAuthorityPromise != nil {
		t.Fatalf("activation with an older member = %+v/%v", result, err)
	}
	duplicate := channelPromiseActivation(st)
	duplicate.ChannelPromiseCapabilities[1].NodeID = duplicate.ChannelPromiseCapabilities[0].NodeID
	if result, err := sm.Apply(ctx, 4, duplicate); err != nil || !result.Rejected {
		t.Fatalf("activation with a duplicate proof = %+v/%v", result, err)
	}

	applyOK(t, sm, 5, channelPromiseActivation(st))
	active := sm.Snapshot(ctx)
	if active.ChannelAuthorityPromise == nil || active.ChannelAuthorityPromise.ActivatedIndex != 5 {
		t.Fatalf("activation state = %+v, want activated at raft index 5", active.ChannelAuthorityPromise)
	}
	if err := active.Validate(); err != nil {
		t.Fatalf("activated state invalid: %v", err)
	}
	if result, err := sm.Apply(ctx, 6, channelPromiseActivation(active)); err != nil || result.Rejected || result.Changed {
		t.Fatalf("repeated activation = %+v/%v, want unchanged no-op", result, err)
	}
	if got := sm.Snapshot(ctx).ChannelAuthorityPromise.ActivatedIndex; got != 5 {
		t.Fatalf("repeated activation moved index to %d", got)
	}

	rev := sm.Snapshot(ctx).Revision
	joiner := state.Node{NodeID: 9, Addr: "n9", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateJoining, Status: state.NodeStatusAlive, CapacityWeight: 1}
	join := command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &rev, Node: &joiner}
	if result, err := sm.Apply(ctx, 7, join); err != nil || !result.Rejected {
		t.Fatalf("unproven joiner after activation = %+v/%v", result, err)
	}
	join.ChannelPromiseJoiningCapability = &state.ChannelReplicationCapability{NodeID: 9, ProtocolVersion: state.ChannelAuthorityPromiseProtocolVersion - 1}
	if result, err := sm.Apply(ctx, 8, join); err != nil || !result.Rejected {
		t.Fatalf("older joiner after activation = %+v/%v", result, err)
	}
	join.ChannelPromiseJoiningCapability = &state.ChannelReplicationCapability{NodeID: 9, ProtocolVersion: state.ChannelAuthorityPromiseProtocolVersion}
	applyOK(t, sm, 9, join)
}

func TestChannelAuthorityPromiseInactiveJoinNeedsNoProof(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	rev := sm.Snapshot(ctx).Revision
	joiner := state.Node{NodeID: 9, Addr: "n9", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateJoining, Status: state.NodeStatusAlive, CapacityWeight: 1}
	applyOK(t, sm, 2, command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &rev, Node: &joiner})
	if sm.Snapshot(ctx).ChannelAuthorityPromise != nil {
		t.Fatal("a join activated authority promises")
	}
}
