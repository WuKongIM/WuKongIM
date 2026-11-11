package fsm

import (
	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

// ReasonChannelPromiseNotReady rejects activation or a join without proven
// Channel authority promise support for every required member.
const ReasonChannelPromiseNotReady = "channel_promise_not_ready"

// applyActivateChannelAuthorityPromise commits one-way activation only when
// every non-removed member, Controller voters included, proved support.
func (sm *StateMachine) applyActivateChannelAuthorityPromise(next *state.ClusterState, index uint64, cmd command.Command) ApplyResult {
	if next.Revision == 0 || index == 0 {
		return reject(ReasonChannelPromiseNotReady)
	}
	if next.ChannelAuthorityPromise != nil {
		return noop(ReasonNoChange)
	}
	if !next.ChannelPromiseProofsCover(cmd.ChannelPromiseCapabilities) {
		return reject(ReasonChannelPromiseNotReady)
	}
	before := next.Clone()
	next.ChannelAuthorityPromise = &state.ChannelAuthorityPromiseState{ActivatedIndex: index}
	return validateChanged(next, before, cmd)
}

// channelPromiseNodeAdmission requires a new member to prove promise support
// once activation is committed. Existing members and removals are unaffected.
func channelPromiseNodeAdmission(st state.ClusterState, node state.Node, proof *state.ChannelReplicationCapability) bool {
	if st.ChannelAuthorityPromise == nil || node.JoinState == state.NodeJoinStateRemoved {
		return true
	}
	for _, old := range st.Nodes {
		if old.NodeID == node.NodeID && old.Addr == node.Addr && old.JoinState != state.NodeJoinStateRemoved {
			return true
		}
	}
	return proof != nil && proof.NodeID == node.NodeID && proof.SupportsChannelAuthorityPromise()
}
