package raft

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

func TestBecomeFollower_SetsState(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	n.BecomeFollower(5, 2)
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
	assert.Equal(t, uint64(2), n.cfg.Leader)
	assert.Equal(t, uint64(0), n.voteFor) // None
	assert.NotNil(t, n.stepFunc)
	assert.NotNil(t, n.tickFnc)
}

func TestBecomeCandidate_SetsState(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.BecomeCandidate()
	assert.Equal(t, types.RoleCandidate, n.cfg.Role)
	assert.Equal(t, uint32(4), n.cfg.Term) // term incremented
	assert.Equal(t, uint64(1), n.voteFor)  // voted for self
	assert.Equal(t, uint64(0), n.cfg.Leader)
	assert.NotNil(t, n.stepFunc)
	assert.NotNil(t, n.tickFnc)
}

func TestBecomeLeader_SetsState(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	n.BecomeLeader(5)
	assert.Equal(t, types.RoleLeader, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
	assert.Equal(t, uint64(1), n.cfg.Leader) // self
	assert.NotNil(t, n.stepFunc)
	assert.NotNil(t, n.tickFnc)
}

func TestBecomeLearner_SetsState(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	n.BecomeLearner(5, 2)
	assert.Equal(t, types.RoleLearner, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
	assert.Equal(t, uint64(2), n.cfg.Leader)
	assert.Equal(t, uint64(0), n.voteFor)
	assert.NotNil(t, n.stepFunc)
	assert.NotNil(t, n.tickFnc)
}

func TestReset_ClearsState(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	n.voteFor = 2
	n.votes[2] = true
	n.stopPropose = true
	n.onlySync = true
	n.suspend = true
	n.replicaSync[2] = &SyncInfo{StoredIndex: 10}
	n.reset()
	assert.Equal(t, uint64(0), n.voteFor)
	assert.Equal(t, 0, len(n.votes))
	assert.False(t, n.stopPropose)
	assert.False(t, n.onlySync)
	assert.False(t, n.suspend)
	assert.Equal(t, 0, len(n.replicaSync))
}

func TestBecomeFollower_FromLeader(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.BecomeFollower(4, 2)
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
	assert.Equal(t, uint32(4), n.cfg.Term)
	assert.Equal(t, uint64(2), n.cfg.Leader)
}

func TestBecomeFollower_FromCandidate(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.BecomeCandidate()
	clearEvents(n)
	n.BecomeFollower(5, 2)
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
}

func TestBecomeLeader_FromFollower(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.BecomeLeader(4)
	assert.Equal(t, types.RoleLeader, n.cfg.Role)
	assert.Equal(t, uint64(1), n.cfg.Leader)
}

func TestBecomeLearner_FromFollower(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.BecomeLearner(4, 3)
	assert.Equal(t, types.RoleLearner, n.cfg.Role)
	assert.Equal(t, uint64(3), n.cfg.Leader)
}
