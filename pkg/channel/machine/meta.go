package machine

import ch "github.com/WuKongIM/WuKongIM/pkg/channel"

// ApplyMeta applies the authoritative control-plane view to local state.
func (s *ChannelState) ApplyMeta(meta ch.Meta) Decision {
	if err := s.ValidateMeta(meta); err != nil {
		return Decision{Err: err}
	}
	if s.shouldClearAppendStateForMeta(meta) {
		s.clearAppendState()
	}
	s.ID = meta.ID
	s.Epoch = meta.Epoch
	s.LeaderEpoch = meta.LeaderEpoch
	s.Leader = meta.Leader
	s.Replicas = copyNodeIDs(meta.Replicas)
	s.ISR = copyNodeIDs(meta.ISR)
	s.MinISR = meta.MinISR
	s.LeaseUntil = meta.LeaseUntil
	if meta.RetentionThroughSeq > s.RetentionThroughSeq {
		s.RetentionThroughSeq = meta.RetentionThroughSeq
	}
	s.WriteFence = meta.WriteFence
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

func (s *ChannelState) shouldClearAppendStateForMeta(meta ch.Meta) bool {
	nextRole := ch.RoleFollower
	if meta.Leader == s.LocalNode {
		nextRole = ch.RoleLeader
	}
	return s.Epoch != meta.Epoch ||
		s.LeaderEpoch != meta.LeaderEpoch ||
		s.Leader != meta.Leader ||
		s.Role != nextRole ||
		s.Status != meta.Status
}

// clearAppendState aborts local append bookkeeping after an accepted metadata fence change.
func (s *ChannelState) clearAppendState() {
	s.InflightAppend = nil
	for opID := range s.PendingAppends {
		delete(s.PendingAppends, opID)
	}
	s.PendingAppendOrder = nil
}

// ValidateMeta rejects identity changes and metadata fence regression without mutating state.
func (s *ChannelState) ValidateMeta(meta ch.Meta) error {
	if meta.Key != "" && meta.Key != s.Key {
		return ch.ErrStaleMeta
	}
	if s.ID != (ch.ChannelID{}) && meta.ID != s.ID {
		return ch.ErrStaleMeta
	}
	if meta.Epoch < s.Epoch ||
		(meta.Epoch == s.Epoch && meta.LeaderEpoch < s.LeaderEpoch) {
		return ch.ErrStaleMeta
	}
	if meta.Epoch == s.Epoch && meta.LeaderEpoch == s.LeaderEpoch && meta.Leader != s.Leader {
		return ch.ErrStaleMeta
	}
	if meta.MinISR <= 0 || meta.MinISR > len(meta.ISR) {
		return ch.ErrInvalidConfig
	}
	return nil
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
