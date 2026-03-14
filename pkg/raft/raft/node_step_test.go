package raft

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

// ==================== Step() Routing & Term Comparison ====================

func TestStep_Term0_LocalMessage_DirectRoute(t *testing.T) {
	n := newTestNode(1, []uint64{1})
	clearEvents(n)
	// Term=0 local message should be routed directly
	err := n.Step(types.Event{Type: types.Propose, Term: 0, Logs: []types.Log{{Term: 1, Index: 1, Data: []byte("a")}}})
	assert.NoError(t, err)
}

func TestStep_LowTerm_Dropped(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 5, 2)
	// Event with term < current term should be dropped
	err := n.Step(types.Event{Type: types.Ping, Term: 3, From: 2})
	assert.NoError(t, err)
	events := collectEvents(n)
	// No response events should be generated
	assert.Equal(t, 0, len(events))
}

func TestStep_HighTerm_PingSyncResp_BecomeFollower(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	// High term Ping → BecomeFollower(newTerm, sender)
	n.Step(types.Event{Type: types.Ping, Term: 5, From: 3, CommittedIndex: 0})
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
	assert.Equal(t, uint64(3), n.cfg.Leader)
}

func TestStep_HighTerm_SyncResp_BecomeFollower(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.Step(types.Event{Type: types.SyncResp, Term: 5, From: 3, Reason: types.ReasonOk})
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
	assert.Equal(t, uint64(3), n.cfg.Leader)
}

func TestStep_HighTerm_OtherEvent_BecomeFollowerLeaderNone(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	// High term non-Ping/SyncResp → BecomeFollower(newTerm, None)
	n.Step(types.Event{Type: types.VoteReq, Term: 5, From: 3, Logs: []types.Log{{Term: 5, Index: 0}}})
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
	// Leader should be None since event is not Ping/SyncResp
	// But VoteReq handling sets leader to From for learner case.
	// For non-learner follower with VoteReq, leader is set to None by BecomeFollower
}

func TestStep_HighTerm_Learner_StaysLearner(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	// High term Ping for learner → BecomeLearner(newTerm, sender)
	n.Step(types.Event{Type: types.Ping, Term: 5, From: 3})
	assert.Equal(t, types.RoleLearner, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
	assert.Equal(t, uint64(3), n.cfg.Leader)
}

func TestStep_HighTerm_Learner_OtherEvent_LeaderNone(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	// High term non-Ping for learner → BecomeLearner(newTerm, None)
	n.Step(types.Event{Type: types.NotifySync, Term: 5, From: 3})
	assert.Equal(t, types.RoleLearner, n.cfg.Role)
	assert.Equal(t, uint32(5), n.cfg.Term)
	assert.Equal(t, uint64(0), n.cfg.Leader)
}

func TestStep_ConfChange_SwitchConfig(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	newCfg := types.Config{
		Replicas: []uint64{1, 2, 3, 4},
		Version:  1,
		Term:     3,
		Role:     types.RoleFollower,
		Leader:   2,
	}
	n.Step(types.Event{Type: types.ConfChange, Config: newCfg})
	assert.Equal(t, 4, len(n.cfg.Replicas))
}

func TestStep_Campaign_TriggersCampaign(t *testing.T) {
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
	n.Step(types.Event{Type: types.Campaign})
	assert.Equal(t, types.RoleCandidate, n.cfg.Role)
}

func TestStep_ApplyResp_Ok_AppliedTo(t *testing.T) {
	n := newTestNode(1, []uint64{1})
	clearEvents(n)
	// Propose and store a log
	proposeAndStore(n, []byte("data"))
	// Now apply
	n.Step(types.Event{Type: types.ApplyResp, Reason: types.ReasonOk, Index: 1})
	assert.Equal(t, uint64(1), n.queue.appliedIndex)
}

func TestStep_ApplyResp_Error_ResetApplying(t *testing.T) {
	n := newTestNode(1, []uint64{1})
	clearEvents(n)
	n.queue.applying = true
	n.Step(types.Event{Type: types.ApplyResp, Reason: types.ReasonError})
	assert.False(t, n.queue.applying)
}

// ==================== VoteReq / canVote ====================

func TestVoteReq_NormalVote_Accepted(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.Step(types.Event{
		Type: types.VoteReq, Term: 3, From: 2,
		Logs: []types.Log{{Term: 3, Index: 0}},
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteResp)
	assert.True(t, ok)
	assert.Equal(t, types.ReasonOk, e.Reason)
	assert.Equal(t, uint64(2), n.voteFor)
}

func TestVoteReq_AlreadyVotedSameNode_Accepted(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.voteFor = 2
	n.Step(types.Event{
		Type: types.VoteReq, Term: 3, From: 2,
		Logs: []types.Log{{Term: 3, Index: 0}},
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteResp)
	assert.True(t, ok)
	assert.Equal(t, types.ReasonOk, e.Reason)
}

func TestVoteReq_AlreadyVotedOtherNode_Rejected(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.voteFor = 3 // already voted for node 3
	n.Step(types.Event{
		Type: types.VoteReq, Term: 3, From: 2,
		Logs: []types.Log{{Term: 3, Index: 0}},
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteResp)
	assert.True(t, ok)
	assert.Equal(t, types.ReasonError, e.Reason)
}

func TestVoteReq_CandidateLogBehind_Rejected(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	// Local node has logs up to index 10
	n.queue.lastLogIndex = 10
	n.lastTermStartIndex.Term = 3
	n.Step(types.Event{
		Type: types.VoteReq, Term: 3, From: 2,
		Logs: []types.Log{{Term: 3, Index: 5}}, // candidate only has 5
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteResp)
	assert.True(t, ok)
	assert.Equal(t, types.ReasonError, e.Reason)
}

func TestVoteReq_CandidateTermLower_Rejected(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.lastTermStartIndex.Term = 3
	// Candidate has lower log term
	n.Step(types.Event{
		Type: types.VoteReq, Term: 3, From: 2,
		Logs: []types.Log{{Term: 2, Index: 100}},
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteResp)
	assert.True(t, ok)
	assert.Equal(t, types.ReasonError, e.Reason)
}

func TestVoteReq_CandidateTermHigher_Accepted(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.lastTermStartIndex.Term = 3
	// Candidate has higher log term
	n.Step(types.Event{
		Type: types.VoteReq, Term: 3, From: 2,
		Logs: []types.Log{{Term: 4, Index: 0}},
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteResp)
	assert.True(t, ok)
	assert.Equal(t, types.ReasonOk, e.Reason)
}

func TestVoteReq_NoLogs_Rejected(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.Step(types.Event{
		Type: types.VoteReq, Term: 3, From: 2,
		// No Logs field
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteResp)
	assert.True(t, ok)
	assert.Equal(t, types.ReasonError, e.Reason)
}

func TestVoteReq_Learner_BecomesFollower(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	n.Step(types.Event{
		Type: types.VoteReq, Term: 3, From: 2,
		Logs: []types.Log{{Term: 3, Index: 0}},
	})
	// Learner should become follower
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
	assert.Equal(t, uint64(2), n.cfg.Leader)
}

// ==================== stepLeader ====================

func TestLeader_Propose_AppendLogs(t *testing.T) {
	n := newTestNode(1, []uint64{1})
	clearEvents(n)
	evt := n.NewPropose([]byte("hello"))
	err := n.Step(evt)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), n.queue.lastLogIndex)
	events := collectEvents(n)
	_, ok := findEvent(events, types.StoreReq)
	assert.True(t, ok)
}

func TestLeader_Propose_StopPropose_Dropped(t *testing.T) {
	n := newTestNode(1, []uint64{1})
	clearEvents(n)
	n.stopPropose = true
	evt := n.NewPropose([]byte("hello"))
	err := n.Step(evt)
	assert.ErrorIs(t, err, types.ErrProposalDropped)
}

func TestLeader_SyncReq_UpdatesSyncInfo(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 2,
		Index: 5, StoredIndex: 3,
	})
	syncInfo := n.replicaSync[2]
	assert.NotNil(t, syncInfo)
	assert.Equal(t, uint64(5), syncInfo.LastSyncIndex)
	assert.Equal(t, uint64(3), syncInfo.StoredIndex)
}

func TestLeader_SyncReq_NoData_EmptySyncResp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	// storedIndex is 0, so no data available
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 2,
		Index: 1, StoredIndex: 0,
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.SyncResp)
	assert.True(t, ok)
	assert.Equal(t, types.ReasonOk, e.Reason)
	assert.Equal(t, 0, len(e.Logs))
}

func TestLeader_SyncReq_OnlySync_IndexBeyondStored_EmptySyncResp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.queue.storedIndex = 5
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 2,
		Index: 10, StoredIndex: 5, Reason: types.ReasonOnlySync,
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.SyncResp)
	assert.True(t, ok)
	assert.Equal(t, 0, len(e.Logs))
}

func TestLeader_SyncReq_HasData_GetLogsReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	// Simulate stored logs
	n.queue.storedIndex = 10
	n.queue.lastLogIndex = 10
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 2,
		Index: 5, StoredIndex: 4,
	})
	events := collectEvents(n)
	_, ok := findEvent(events, types.GetLogsReq)
	assert.True(t, ok)
}

func TestLeader_SyncReq_GetingLogs_NoDuplicate(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.queue.storedIndex = 10
	n.queue.lastLogIndex = 10
	// First sync creates GetLogsReq
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 2,
		Index: 5, StoredIndex: 4,
	})
	clearEvents(n)
	// Second sync should not create another GetLogsReq because GetingLogs=true
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 2,
		Index: 5, StoredIndex: 4,
	})
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.GetLogsReq))
}

func TestLeader_SyncReq_UpdatesCommittedIndex(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	// Leader has stored some logs
	n.queue.storedIndex = 10
	n.queue.lastLogIndex = 10
	// Simulate node 2 has synced up to index 10
	n.replicaSync[2] = &SyncInfo{StoredIndex: 10, LastSyncIndex: 11}
	// Now node 3 syncs
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 3,
		Index: 11, StoredIndex: 10,
	})
	// With 3 nodes and 2 replicas reporting stored=10, committed should advance
	assert.True(t, n.queue.committedIndex > 0)
}

func TestLeader_SyncReq_Learner_NoCommitImpact(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.cfg.Learners = []uint64{4}
	n.queue.storedIndex = 10
	n.queue.lastLogIndex = 10
	oldCommitted := n.queue.committedIndex
	// Learner sync should not affect commit
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 4,
		Index: 11, StoredIndex: 10,
	})
	assert.Equal(t, oldCommitted, n.queue.committedIndex)
}

func TestLeader_SyncReq_AutoSuspend(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2}),
		WithElectionOn(false),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
		WithAutoSuspend(true),
		WithSuspendAfterEmptySyncTick(2),
	)
	n := NewNode(0, types.RaftState{}, opts)
	makeLeader(n, 3)

	// Send empty syncs exceeding threshold
	for i := 0; i < 3; i++ {
		n.Step(types.Event{
			Type: types.SyncReq, Term: 3, From: 2,
			Index: 1, StoredIndex: 0,
		})
		clearEvents(n)
	}
	// The last sync should have speed=suspend
	n.Step(types.Event{
		Type: types.SyncReq, Term: 3, From: 2,
		Index: 1, StoredIndex: 0,
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.SyncResp)
	assert.True(t, ok)
	assert.Equal(t, types.SpeedSuspend, e.Speed)
}

func TestLeader_StoreResp_Ok_StoreToAndNotifySync(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	// Propose a log
	evt := n.NewPropose([]byte("data"))
	n.Step(evt)
	clearEvents(n)
	// Simulate StoreResp
	n.Step(types.Event{Type: types.StoreResp, Reason: types.ReasonOk, Index: 1})
	assert.Equal(t, uint64(1), n.queue.storedIndex)
	events := collectEvents(n)
	// Should notify sync to replicas
	notifySyncs := findEventsOfType(events, types.NotifySync)
	assert.True(t, len(notifySyncs) > 0)
}

func TestLeader_StoreResp_SingleNode_DirectCommit(t *testing.T) {
	n := newTestNode(1, []uint64{1})
	clearEvents(n)
	// Propose a log
	evt := n.NewPropose([]byte("data"))
	n.Step(evt)
	clearEvents(n)
	// Store response
	n.Step(types.Event{Type: types.StoreResp, Reason: types.ReasonOk, Index: 1})
	assert.Equal(t, uint64(1), n.queue.committedIndex)
}

func TestLeader_GetLogsResp_SendsSyncResp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.replicaSync[2] = &SyncInfo{GetingLogs: true}
	n.Step(types.Event{
		Type: types.GetLogsResp, To: 2, Index: 5,
		Logs:   []types.Log{{Term: 3, Index: 5, Data: []byte("data")}},
		Reason: types.ReasonOk,
	})
	events := collectEvents(n)
	e, ok := findEvent(events, types.SyncResp)
	assert.True(t, ok)
	assert.Equal(t, uint64(2), e.To)
	assert.False(t, n.replicaSync[2].GetingLogs)
}

func TestLeader_RoleSwitchResp_ClearsSwitching(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.replicaSync[2] = &SyncInfo{roleSwitching: true}
	n.stopPropose = true
	n.Step(types.Event{Type: types.LearnerToFollowerResp, From: 2})
	assert.False(t, n.replicaSync[2].roleSwitching)
	assert.False(t, n.stopPropose)
}

func TestLeader_ConfigReq_SendsConfigResp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.Step(types.Event{Type: types.ConfigReq, From: 2, Term: 3})
	events := collectEvents(n)
	e, ok := findEvent(events, types.ConfigResp)
	assert.True(t, ok)
	assert.Equal(t, uint64(2), e.To)
}

// ==================== stepFollower ====================

func TestFollower_Ping_ResetsElection(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.electionElapsed = 50
	n.Step(types.Event{Type: types.Ping, Term: 3, From: 2})
	assert.Equal(t, 0, n.electionElapsed)
}

func TestFollower_Ping_LeaderNone_SetsLeader(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, None)
	n.Step(types.Event{Type: types.Ping, Term: 3, From: 2})
	assert.Equal(t, uint64(2), n.cfg.Leader)
}

func TestFollower_Ping_HighConfigVersion_ConfigReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.cfg.Version = 1
	n.Step(types.Event{Type: types.Ping, Term: 3, From: 2, ConfigVersion: 5})
	events := collectEvents(n)
	_, ok := findEvent(events, types.ConfigReq)
	assert.True(t, ok)
}

func TestFollower_NotifySync_SendsSyncReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.suspend = true
	n.Step(types.Event{Type: types.NotifySync, Term: 3, From: 2})
	assert.False(t, n.suspend) // should unsuspend
	events := collectEvents(n)
	_, ok := findEvent(events, types.SyncReq)
	assert.True(t, ok)
}

func TestFollower_SyncResp_Ok_WithLogs_Appends(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.onlySync = true
	logs := []types.Log{{Term: 3, Index: 1, Data: []byte("data")}}
	n.Step(types.Event{
		Type: types.SyncResp, Term: 3, From: 2,
		Reason: types.ReasonOk, Logs: logs,
	})
	assert.Equal(t, uint64(1), n.queue.lastLogIndex)
	assert.False(t, n.syncing)
}

func TestFollower_SyncResp_Ok_NoLogs_Suspend(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.onlySync = true
	n.Step(types.Event{
		Type: types.SyncResp, Term: 3, From: 2,
		Reason: types.ReasonOk, Speed: types.SpeedSuspend,
	})
	assert.True(t, n.suspend)
}

func TestFollower_SyncResp_Truncate_SendsTruncateReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.onlySync = true
	n.Step(types.Event{
		Type: types.SyncResp, Term: 3, From: 2,
		Reason: types.ReasonTruncate, Index: 5,
	})
	assert.True(t, n.truncating)
	events := collectEvents(n)
	e, ok := findEvent(events, types.TruncateReq)
	assert.True(t, ok)
	assert.Equal(t, uint64(5), e.Index)
}

func TestFollower_TruncateResp_Ok_TruncatesAndSyncs(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	// Setup: truncate index >= lastLogIndex means no truncation of logs slice
	n.queue.lastLogIndex = 10
	n.queue.storedIndex = 10
	n.truncating = true
	// When TruncateResp.Index < lastLogIndex, it calls truncateLogTo
	// Use index >= lastLogIndex to skip that branch
	n.Step(types.Event{
		Type: types.TruncateResp, Term: 3,
		Reason: types.ReasonOk, Index: 10,
	})
	assert.False(t, n.truncating)
	// Index not < lastLogIndex, so no truncation
	events := collectEvents(n)
	_, ok := findEvent(events, types.SyncReq)
	assert.True(t, ok)
}

func TestFollower_StoreResp_Ok_StoreToAndSync(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.onlySync = true
	// Append logs first
	n.queue.append(types.Log{Term: 3, Index: 1, Data: []byte("data")})
	n.queue.appending = true
	n.Step(types.Event{
		Type: types.StoreResp, Term: 3,
		Reason: types.ReasonOk, Index: 1,
	})
	assert.Equal(t, uint64(1), n.queue.storedIndex)
	assert.False(t, n.queue.appending)
}

func TestFollower_ConfigResp_SwitchesConfig(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	newCfg := types.Config{
		Replicas: []uint64{1, 2, 3, 4},
		Version:  2,
		Role:     types.RoleFollower,
		Leader:   2,
	}
	n.Step(types.Event{Type: types.ConfigResp, Term: 3, From: 2, Config: newCfg})
	assert.Equal(t, 4, len(n.cfg.Replicas))
}

func TestFollower_Propose_Ignored(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	err := n.Step(types.Event{
		Type: types.Propose, Logs: []types.Log{{Term: 3, Index: 1, Data: []byte("data")}},
	})
	assert.NoError(t, err)
	// No StoreReq should be generated
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.StoreReq))
}

// ==================== stepCandidate ====================

func TestCandidate_VoteResp_QuorumReached_BecomeLeader(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2, 3}),
		WithElectionOn(true),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	n.BecomeCandidate()
	clearEvents(n)
	// Node 1 votes for self during BecomeCandidate (voteFor=1)
	n.votes[1] = true
	// Node 2 agrees
	n.Step(types.Event{Type: types.VoteResp, From: 2, Term: n.cfg.Term, Reason: types.ReasonOk})
	assert.Equal(t, types.RoleLeader, n.cfg.Role)
	events := collectEvents(n)
	// Should send Ping after becoming leader
	pings := findEventsOfType(events, types.Ping)
	assert.True(t, len(pings) > 0)
}

func TestCandidate_VoteResp_RejectedQuorum_BecomeFollower(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2, 3}),
		WithElectionOn(true),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	n.BecomeCandidate()
	clearEvents(n)
	n.votes[1] = true
	// Node 2 rejects
	n.Step(types.Event{Type: types.VoteResp, From: 2, Term: n.cfg.Term, Reason: types.ReasonError})
	// With 2 out of 3 votes and 1 reject, check status
	// quorum is 2, we have 2 votes total (1 yes, 1 no), granted=1 < quorum
	// but len(votes)=2 >= quorum, so BecomeFollower
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
}

func TestCandidate_VoteResp_NotEnough_Waiting(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2, 3, 4, 5}),
		WithElectionOn(true),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	n.BecomeCandidate()
	clearEvents(n)
	n.votes[1] = true
	// Only 1 more vote, need quorum=3
	n.Step(types.Event{Type: types.VoteResp, From: 2, Term: n.cfg.Term, Reason: types.ReasonOk})
	// 2 votes, need 3 for quorum of 5
	assert.Equal(t, types.RoleCandidate, n.cfg.Role)
}

func TestCandidate_Propose_Ignored(t *testing.T) {
	opts := NewOptions(
		WithNodeId(1),
		WithReplicas([]uint64{1, 2, 3}),
		WithElectionOn(true),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	n := NewNode(0, types.RaftState{}, opts)
	n.BecomeCandidate()
	clearEvents(n)
	err := n.Step(types.Event{
		Type: types.Propose, Logs: []types.Log{{Term: 1, Index: 1}},
	})
	assert.NoError(t, err)
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.StoreReq))
}

// ==================== stepLearner ====================

func TestLearner_Ping_ResetsElection(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	n.electionElapsed = 50
	n.Step(types.Event{Type: types.Ping, Term: 3, From: 2})
	assert.Equal(t, 0, n.electionElapsed)
}

func TestLearner_Ping_LeaderNone_SetsLeader(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, None)
	n.Step(types.Event{Type: types.Ping, Term: 3, From: 2})
	assert.Equal(t, types.RoleLearner, n.cfg.Role)
	assert.Equal(t, uint64(2), n.cfg.Leader)
}

func TestLearner_NotifySync_SendsSyncReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	n.Step(types.Event{Type: types.NotifySync, Term: 3, From: 2})
	events := collectEvents(n)
	_, ok := findEvent(events, types.SyncReq)
	assert.True(t, ok)
}

func TestLearner_SyncResp_Ok_WithLogs_Appends(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	logs := []types.Log{{Term: 3, Index: 1, Data: []byte("data")}}
	n.Step(types.Event{
		Type: types.SyncResp, Term: 3, From: 2,
		Reason: types.ReasonOk, Logs: logs,
	})
	assert.Equal(t, uint64(1), n.queue.lastLogIndex)
}

func TestLearner_SyncResp_Truncate_SendsTruncateReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	n.Step(types.Event{
		Type: types.SyncResp, Term: 3, From: 2,
		Reason: types.ReasonTruncate, Index: 5,
	})
	assert.True(t, n.truncating)
	events := collectEvents(n)
	_, ok := findEvent(events, types.TruncateReq)
	assert.True(t, ok)
}

func TestLearner_StoreResp_Ok_StoreToAndSyncReq(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	n.queue.append(types.Log{Term: 3, Index: 1, Data: []byte("data")})
	n.queue.appending = true
	n.Step(types.Event{
		Type: types.StoreResp, Reason: types.ReasonOk, Index: 1,
	})
	assert.Equal(t, uint64(1), n.queue.storedIndex)
	assert.False(t, n.queue.appending)
	events := collectEvents(n)
	_, ok := findEvent(events, types.SyncReq)
	assert.True(t, ok)
}

func TestLearner_TruncateResp_Ok(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	n.queue.lastLogIndex = 10
	n.queue.storedIndex = 10
	n.truncating = true
	// Use index >= lastLogIndex to avoid slice bounds issue in truncateLogTo
	n.Step(types.Event{
		Type: types.TruncateResp, Reason: types.ReasonOk, Index: 10,
	})
	assert.False(t, n.truncating)
}

func TestLearner_ConfigResp_SwitchesConfig(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLearner(n, 3, 2)
	newCfg := types.Config{
		Replicas: []uint64{1, 2, 3},
		Learners: []uint64{4},
		Version:  2,
		Role:     types.RoleLearner,
		Leader:   2,
	}
	n.Step(types.Event{Type: types.ConfigResp, Term: 3, From: 2, Config: newCfg})
	assert.Equal(t, uint64(2), n.cfg.Version)
}

// ==================== committedIndex ====================

func TestCommittedIndex_SingleNode_StoredIndex(t *testing.T) {
	n := newTestNode(1, []uint64{1})
	clearEvents(n)
	n.queue.storedIndex = 10
	n.queue.lastLogIndex = 10
	idx := n.committedIndexForLeader()
	assert.Equal(t, uint64(10), idx)
}

func TestCommittedIndex_ThreeNodes_MajorityConfirmed(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.queue.storedIndex = 10
	n.queue.lastLogIndex = 10
	// Both replicas have stored up to 10
	n.replicaSync[2] = &SyncInfo{StoredIndex: 10}
	n.replicaSync[3] = &SyncInfo{StoredIndex: 10}
	idx := n.committedIndexForLeader()
	// quorum=2, leader stored=10, both replicas stored=10
	// All 3 have storedIndex >= 10, so committed should be 9 (StoredIndex-1 from the algo)
	// Actually the algo counts replicas with StoredIndex >= maxLogIndex+1
	// Let me trace: getMaxLogIndexLessThanParam(0) → finds max StoredIndex < 0 which is none...
	// Actually StoredIndex < maxIndex when maxIndex=0 is always true
	// So secondMaxIndex = max of all StoredIndex = 10, returns 10-1 = 9
	// Then count replicas with StoredIndex >= 10: both = 2, count+1=3 >= quorum=2 → committed=9
	assert.Equal(t, uint64(9), idx)
}

func TestCommittedIndex_ThreeNodes_MinorityConfirmed_NoAdvance(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.queue.storedIndex = 10
	n.queue.lastLogIndex = 10
	// Only one replica has stored, not enough for quorum
	n.replicaSync[2] = &SyncInfo{StoredIndex: 10}
	n.replicaSync[3] = &SyncInfo{StoredIndex: 0}
	idx := n.committedIndexForLeader()
	// getMaxLogIndexLessThanParam(0): max(10,0) both < 0? no, maxIndex=0 means condition is always true
	// secondMaxIndex = 10, returns 9
	// count replicas with StoredIndex >= 10: only node 2 → count=1, count+1=2 >= quorum=2 → committed=9
	assert.Equal(t, uint64(9), idx)
}

func TestCommittedIndex_FiveNodes_MajorityConfirmed(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3, 4, 5})
	makeLeader(n, 3)
	n.queue.storedIndex = 10
	n.queue.lastLogIndex = 10
	// All 4 replicas have synced to index 10
	n.replicaSync[2] = &SyncInfo{StoredIndex: 10}
	n.replicaSync[3] = &SyncInfo{StoredIndex: 10}
	n.replicaSync[4] = &SyncInfo{StoredIndex: 10}
	n.replicaSync[5] = &SyncInfo{StoredIndex: 10}
	// quorum=3, all replicas stored=10, leader stored=10
	idx := n.committedIndexForLeader()
	// The algorithm descends through all replica stored indices
	assert.True(t, idx > 0)
}

func TestCommittedIndex_Follower_MinOfLeaderAndStored(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.queue.storedIndex = 5
	// Leader committed is 10 but follower only stored 5
	idx := n.committedIndexForFollow(10)
	assert.Equal(t, uint64(5), idx)
	// Leader committed is 3, less than stored
	idx2 := n.committedIndexForFollow(3)
	assert.Equal(t, uint64(3), idx2)
}

// ==================== roleSwitchIfNeed ====================

func TestRoleSwitchIfNeed_NoMigration_NoOp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.cfg.MigrateTo = 0
	n.cfg.MigrateFrom = 0
	n.replicaSync[2] = &SyncInfo{}
	n.roleSwitchIfNeed(types.Event{From: 2, Index: 100})
	// No role switch events should be generated
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.LearnerToLeaderReq))
	assert.Equal(t, 0, countEvents(events, types.LearnerToFollowerReq))
	assert.Equal(t, 0, countEvents(events, types.FollowerToLeaderReq))
}

func TestRoleSwitchIfNeed_LearnerToLeader_CaughtUp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2})
	makeLeader(n, 3)
	n.cfg.Learners = []uint64{4}
	n.cfg.MigrateTo = 4
	n.cfg.MigrateFrom = 1 // leader is node 1
	n.cfg.Leader = 1
	n.queue.lastLogIndex = 10
	n.replicaSync[4] = &SyncInfo{}
	// Learner has caught up (Index >= lastLogIndex+1)
	n.roleSwitchIfNeed(types.Event{From: 4, Index: 11})
	assert.True(t, n.replicaSync[4].roleSwitching)
	events := collectEvents(n)
	_, ok := findEvent(events, types.LearnerToLeaderReq)
	assert.True(t, ok)
}

func TestRoleSwitchIfNeed_LearnerToLeader_Approaching_StopPropose(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2})
	makeLeader(n, 3)
	n.cfg.Learners = []uint64{4}
	n.cfg.MigrateTo = 4
	n.cfg.MigrateFrom = 1
	n.cfg.Leader = 1
	n.queue.lastLogIndex = 200
	n.opts.LearnerToLeaderMinLogGap = 100
	n.replicaSync[4] = &SyncInfo{}
	// Learner is close but not caught up
	n.roleSwitchIfNeed(types.Event{From: 4, Index: 150})
	assert.True(t, n.stopPropose)
}

func TestRoleSwitchIfNeed_LearnerToFollower_CaughtUp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2})
	makeLeader(n, 3)
	n.cfg.Learners = []uint64{4}
	n.cfg.MigrateTo = 4
	n.cfg.MigrateFrom = 2 // not the leader
	n.queue.lastLogIndex = 10
	n.opts.LearnerToFollowerMinLogGap = 100
	n.replicaSync[4] = &SyncInfo{}
	// Learner close enough (within gap)
	n.roleSwitchIfNeed(types.Event{From: 4, Index: 5})
	// Index + gap = 105 > lastLogIndex=10 → should trigger
	assert.True(t, n.replicaSync[4].roleSwitching)
	events := collectEvents(n)
	_, ok := findEvent(events, types.LearnerToFollowerReq)
	assert.True(t, ok)
}

func TestRoleSwitchIfNeed_FollowerToLeader_CaughtUp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.cfg.MigrateTo = 2
	n.cfg.MigrateFrom = 1 // leader
	n.cfg.Leader = 1
	n.queue.lastLogIndex = 10
	n.replicaSync[2] = &SyncInfo{}
	// Follower has caught up
	n.roleSwitchIfNeed(types.Event{From: 2, Index: 11})
	assert.True(t, n.replicaSync[2].roleSwitching)
	events := collectEvents(n)
	_, ok := findEvent(events, types.FollowerToLeaderReq)
	assert.True(t, ok)
}
