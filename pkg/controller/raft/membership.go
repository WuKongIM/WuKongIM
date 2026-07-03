package raft

import (
	"context"

	"go.etcd.io/raft/v3/raftpb"
)

// MembershipChangeResult describes one committed Controller Raft membership change.
type MembershipChangeResult struct {
	// Index is the committed Raft log index of the membership change.
	Index uint64
	// ConfState is the Raft configuration after applying the committed change.
	ConfState raftpb.ConfState
}

// AddLearner adds nodeID as a non-voting Controller Raft learner.
func (s *Service) AddLearner(ctx context.Context, nodeID uint64) (MembershipChangeResult, error) {
	return s.submitMembershipChange(ctx, raftpb.ConfChange{
		Type:   raftpb.ConfChangeAddLearnerNode,
		NodeID: nodeID,
	})
}

// PromoteLearner promotes nodeID from learner to Controller Raft voter.
func (s *Service) PromoteLearner(ctx context.Context, nodeID uint64) (MembershipChangeResult, error) {
	return s.submitMembershipChange(ctx, raftpb.ConfChange{
		Type:   raftpb.ConfChangeAddNode,
		NodeID: nodeID,
	})
}
