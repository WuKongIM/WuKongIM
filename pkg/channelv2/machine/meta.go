package machine

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// ApplyMeta applies the authoritative control-plane view to local state.
func (s *ChannelState) ApplyMeta(meta ch.Meta) Decision {
	if meta.Key != "" && meta.Key != s.Key {
		return Decision{Err: ch.ErrStaleMeta}
	}
	if meta.MinISR <= 0 || meta.MinISR > len(meta.ISR) {
		return Decision{Err: ch.ErrInvalidConfig}
	}
	s.ID = meta.ID
	s.Epoch = meta.Epoch
	s.LeaderEpoch = meta.LeaderEpoch
	s.Leader = meta.Leader
	s.Replicas = copyNodeIDs(meta.Replicas)
	s.ISR = copyNodeIDs(meta.ISR)
	s.MinISR = meta.MinISR
	s.Status = meta.Status
	if meta.Status == ch.StatusDeleted {
		s.CommitReady = false
		return Decision{}
	}
	if meta.Leader == s.LocalNode {
		s.Role = ch.RoleLeader
		s.Progress[s.LocalNode] = ReplicaProgress{Match: s.LEO}
	} else {
		s.Role = ch.RoleFollower
	}
	s.CommitReady = meta.Status == ch.StatusActive || meta.Status == ch.StatusCreating
	return Decision{}
}

// IsReplica reports whether node is part of the authoritative replica set.
func (s *ChannelState) IsReplica(node ch.NodeID) bool {
	for _, replica := range s.Replicas {
		if replica == node {
			return true
		}
	}
	return false
}

// IsISR reports whether node participates in commit quorum.
func (s *ChannelState) IsISR(node ch.NodeID) bool {
	for _, replica := range s.ISR {
		if replica == node {
			return true
		}
	}
	return false
}
