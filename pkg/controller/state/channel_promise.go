package state

// ChannelAuthorityPromiseProtocolVersion is the Channel replication protocol
// that fences a new leader authority with durable per-replica promises instead
// of an in-log current-term barrier. It keeps the distributed log sequence
// equal to message_seq (docs/superpowers/specs/2026-11-09-log-sequence-is-message-seq-design.md).
const ChannelAuthorityPromiseProtocolVersion uint32 = 2

// ChannelReplicationCapability is one running node's answer to the Channel
// replication capability probe, never a build label. Answering at all proves
// the node decodes the activation command and state section.
type ChannelReplicationCapability struct {
	// NodeID is the node that answered.
	NodeID uint64 `json:"node_id"`
	// ProtocolVersion is the highest Channel replication protocol it runs.
	ProtocolVersion uint32 `json:"protocol_version"`
}

// ChannelAuthorityPromiseState records one-way cluster activation. Once set,
// leaders fence authorities with promises and joiners must prove support.
// Older binaries cannot decode it, so rollback after activation is unsupported.
type ChannelAuthorityPromiseState struct {
	// ActivatedIndex is the Raft index that committed activation.
	ActivatedIndex uint64 `json:"activated_index"`
}

func validateChannelAuthorityPromise(s ClusterState) error {
	if s.ChannelAuthorityPromise != nil && s.ChannelAuthorityPromise.ActivatedIndex == 0 {
		return invalid("channel authority promise activation index is missing")
	}
	return nil
}

// ChannelPromiseProofsCover reports whether proofs prove promise support for
// exactly every non-removed member, each once. Controller voters are members
// and mirrors decode state strictly, so all of them must be covered.
func (s ClusterState) ChannelPromiseProofsCover(proofs []ChannelReplicationCapability) bool {
	members := make(map[uint64]bool, len(s.Nodes))
	for _, node := range s.Nodes {
		if node.JoinState != NodeJoinStateRemoved {
			members[node.NodeID] = false
		}
	}
	for _, voter := range s.Controllers {
		if _, ok := members[voter.NodeID]; !ok {
			return false
		}
	}
	if len(members) == 0 || len(proofs) != len(members) {
		return false
	}
	for _, proof := range proofs {
		seen, ok := members[proof.NodeID]
		if !ok || seen || !proof.SupportsChannelAuthorityPromise() {
			return false
		}
		members[proof.NodeID] = true
	}
	return true
}

// SupportsChannelAuthorityPromise reports whether the answering node runs a
// protocol that understands authority promises.
func (c ChannelReplicationCapability) SupportsChannelAuthorityPromise() bool {
	return c.NodeID != 0 && c.ProtocolVersion >= ChannelAuthorityPromiseProtocolVersion
}
