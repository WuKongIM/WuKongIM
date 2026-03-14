package raft

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

func TestTick_AutoDestroy_Timeout_DestoryEvent(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1}),
		WithElectionOn(false),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
		WithAutoDestory(true),
		WithDestoryAfterIdleTick(5),
	)
	n := NewNode(0, types.RaftState{}, opts)
	clearEvents(n)
	tickN(n, 6)
	events := collectEvents(n)
	_, ok := findEvent(events, types.Destory)
	assert.True(t, ok)
}

func TestTick_Follower_ElectionTimeout_Campaign(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2, 3}),
		WithElectionOn(true),
		WithElectionInterval(10),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	makeFollower(n, 1, 2)
	// Tick enough times to trigger election
	tickN(n, n.randomizedElectionTimeout+1)
	// Should have become candidate
	assert.Equal(t, types.RoleCandidate, n.cfg.Role)
}

func TestTick_Follower_SyncInterval_SyncReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	// Tick enough times to trigger sync
	tickN(n, n.opts.SyncIntervalTick+1)
	events := collectEvents(n)
	_, ok := findEvent(events, types.SyncReq)
	assert.True(t, ok)
}

func TestTick_Follower_SyncRespTimeout_ResetSyncing(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.syncing = true
	tickN(n, n.opts.SyncRespTimeoutTick+1)
	assert.False(t, n.syncing)
}

func TestTick_Follower_Suspended_NoSync(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.suspend = true
	tickN(n, 100)
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.SyncReq))
}

func TestTick_Leader_Heartbeat_ElectionOn_PingsAll(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2, 3}),
		WithElectionOn(true),
		WithElectionInterval(1000),
		WithHeartbeatInterval(1),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	makeLeader(n, 3)
	n.Tick()
	events := collectEvents(n)
	pings := findEventsOfType(events, types.Ping)
	// Should send ping to node 2 and node 3
	assert.Equal(t, 2, len(pings))
}

func TestTick_Leader_Heartbeat_NoElection_PerReplica(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	// Without election, leader sends ping per replica based on SyncTick
	tickN(n, n.opts.SyncIntervalTick+2)
	events := collectEvents(n)
	pings := findEventsOfType(events, types.Ping)
	assert.True(t, len(pings) > 0)
}

func TestTick_Candidate_ElectionTimeout_Recampaign(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2, 3}),
		WithElectionOn(true),
		WithElectionInterval(10),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	n.BecomeCandidate()
	clearEvents(n)
	oldTerm := n.cfg.Term
	tickN(n, n.randomizedElectionTimeout+1)
	// Should have re-campaigned (term increases)
	assert.True(t, n.cfg.Term > oldTerm)
}

func TestTick_Learner_SyncInterval_SyncReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	tickN(n, n.opts.SyncIntervalTick+1)
	events := collectEvents(n)
	_, ok := findEvent(events, types.SyncReq)
	assert.True(t, ok)
}

func TestTick_Learner_Suspended_NoSync(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	n.suspend = true
	tickN(n, 100)
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.SyncReq))
}

func TestCampaign_FromFollower_BecomeCandidate(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2, 3}),
		WithElectionOn(true),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	makeFollower(n, 1, 0)
	n.campaign()
	assert.Equal(t, types.RoleCandidate, n.cfg.Role)
	events := collectEvents(n)
	voteReqs := findEventsOfType(events, types.VoteReq)
	// Should send VoteReq to nodes 2 and 3, plus self (LocalNode)
	assert.Equal(t, 3, len(voteReqs))
}

func TestCampaign_FromLeader_BecomeFollowerFirst(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.opts.ElectionOn = true
	n.campaign()
	// When leader campaigns, it first becomes follower
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
}

func TestCampaign_SingleNode_BecomeLeaderDirectly(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1}),
		WithElectionOn(true),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	// Single node should already be leader from NewNode
	assert.Equal(t, types.RoleLeader, n.cfg.Role)
}

func TestHasSyncReq_Empty(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	assert.False(t, n.hasSyncReq())
}

func TestHasSyncReq_WithSyncReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.events = append(n.events, types.Event{Type: types.SyncReq})
	assert.True(t, n.hasSyncReq())
}

func TestHasSyncReq_WithOtherEvents(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.events = append(n.events, types.Event{Type: types.Ping})
	assert.False(t, n.hasSyncReq())
}
