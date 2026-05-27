package raft

import (
	etcdraft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func (s *Service) sendReadyMessages(messages []raftpb.Message) {
	if len(messages) == 0 {
		return
	}
	s.cfg.Transport.Send(messages)
}

func countConfChanges(entries []raftpb.Entry) int {
	count := 0
	for _, entry := range entries {
		if entry.Type == raftpb.EntryConfChange || entry.Type == raftpb.EntryConfChangeV2 {
			count++
		}
	}
	return count
}

func applyConfChange(rawNode *etcdraft.RawNode, entry raftpb.Entry) (raftpb.ConfState, error) {
	switch entry.Type {
	case raftpb.EntryConfChange:
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(entry.Data); err != nil {
			return raftpb.ConfState{}, err
		}
		return *rawNode.ApplyConfChange(cc), nil
	case raftpb.EntryConfChangeV2:
		var cc raftpb.ConfChangeV2
		if err := cc.Unmarshal(entry.Data); err != nil {
			return raftpb.ConfState{}, err
		}
		return *rawNode.ApplyConfChange(cc), nil
	default:
		return raftpb.ConfState{}, nil
	}
}

func (s *Service) setRunError(err error) {
	s.mu.Lock()
	s.err = err
	s.started = false
	s.mu.Unlock()
	s.recordDegraded(err)
}

func (s *Service) currentError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	return ErrStopped
}

func (s *Service) recordDegraded(err error) {
	if err == nil {
		return
	}
	s.statusMu.Lock()
	st := s.status
	if st.NodeID == 0 {
		st.NodeID = s.cfg.NodeID
	}
	if st.Role == "" {
		st.Role = RoleUnknown
	}
	st.Degraded = true
	st.ErrorReason = err.Error()
	s.status = st
	s.statusMu.Unlock()
}

func (s *Service) updateStatus(rawNode *etcdraft.RawNode, err error) {
	status := rawNode.Status()
	s.leaderID.Store(status.Lead)
	s.statusMu.Lock()
	st := s.status
	st.NodeID = s.cfg.NodeID
	st.Role = raftRoleName(status.RaftState)
	st.LeaderID = status.Lead
	st.Term = status.Term
	st.CommitIndex = status.Commit
	st.AppliedIndex = status.Applied
	if err != nil {
		st.Degraded = true
		st.ErrorReason = err.Error()
	}
	s.status = st
	s.statusMu.Unlock()
}

func shouldBootstrap(startup runStartupState) bool {
	return startup.LastIndex == 0 && startup.AppliedIndex == 0 && etcdraft.IsEmptyHardState(startup.HardState) && isZeroConfState(startup.ConfState) && etcdraft.IsEmptySnap(startup.Snapshot)
}

func isSmallestPeer(nodeID uint64, peers []Peer) bool {
	if len(peers) == 0 {
		return false
	}
	min := peers[0].NodeID
	for _, peer := range peers[1:] {
		if peer.NodeID < min {
			min = peer.NodeID
		}
	}
	return nodeID == min
}

func raftPeers(peers []Peer) []etcdraft.Peer {
	out := make([]etcdraft.Peer, 0, len(peers))
	for _, peer := range peers {
		out = append(out, etcdraft.Peer{ID: peer.NodeID})
	}
	return out
}

func isZeroConfState(state raftpb.ConfState) bool {
	return len(state.Voters) == 0 && len(state.Learners) == 0 && len(state.VotersOutgoing) == 0 && len(state.LearnersNext) == 0 && !state.AutoLeave
}
