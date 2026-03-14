package raft

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

func TestSendPing_ToAll(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.sendPing(All)
	events := collectEvents(n)
	pings := findEventsOfType(events, types.Ping)
	assert.Equal(t, 2, len(pings)) // to 2 and 3
	for _, p := range pings {
		assert.Equal(t, uint64(1), p.From)
		assert.Equal(t, uint32(3), p.Term)
	}
}

func TestSendPing_ToSingleNode(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.sendPing(2)
	events := collectEvents(n)
	pings := findEventsOfType(events, types.Ping)
	assert.Equal(t, 1, len(pings))
	assert.Equal(t, uint64(2), pings[0].To)
	assert.Equal(t, n.queue.committedIndex, pings[0].CommittedIndex)
}

func TestSendPing_NotLeader_NoOp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.sendPing(All)
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.Ping))
}

func TestSendVoteReq_Fields(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.lastTermStartIndex.Term = 3
	n.queue.lastLogIndex = 10
	n.sendVoteReq(2)
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteReq)
	assert.True(t, ok)
	assert.Equal(t, uint64(1), e.From)
	assert.Equal(t, uint64(2), e.To)
	assert.Equal(t, uint32(3), e.Term)
	assert.Equal(t, 1, len(e.Logs))
	assert.Equal(t, uint32(3), e.Logs[0].Term)
	assert.Equal(t, uint64(10), e.Logs[0].Index)
}

func TestSendVoteResp_Fields(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 0)
	n.sendVoteResp(2, types.ReasonOk)
	events := collectEvents(n)
	e, ok := findEvent(events, types.VoteResp)
	assert.True(t, ok)
	assert.Equal(t, uint64(1), e.From)
	assert.Equal(t, uint64(2), e.To)
	assert.Equal(t, types.ReasonOk, e.Reason)
}

func TestSendSyncReq_Fields(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.queue.storedIndex = 5
	n.queue.lastLogIndex = 8
	n.onlySync = true
	n.sendSyncReq()
	events := collectEvents(n)
	e, ok := findEvent(events, types.SyncReq)
	assert.True(t, ok)
	assert.Equal(t, uint64(1), e.From)
	assert.Equal(t, uint64(2), e.To)
	assert.Equal(t, uint32(3), e.Term)
	assert.Equal(t, uint64(6), e.StoredIndex) // storedIndex + 1
	assert.Equal(t, uint64(9), e.Index)       // lastLogIndex + 1
	assert.Equal(t, types.ReasonOnlySync, e.Reason)
	assert.True(t, n.syncing)
}

func TestSendSyncReq_Truncating_NoOp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.truncating = true
	n.sendSyncReq()
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.SyncReq))
}

func TestSendSyncReq_AlreadySyncing_NoOp(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.syncing = true
	n.sendSyncReq()
	events := collectEvents(n)
	assert.Equal(t, 0, countEvents(events, types.SyncReq))
}

func TestSendNotifySync_ToAll(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	n.sendNotifySync(All)
	events := collectEvents(n)
	notifys := findEventsOfType(events, types.NotifySync)
	assert.Equal(t, 2, len(notifys)) // to nodes 2 and 3
}

func TestSendDestory_Fields(t *testing.T) {
	n := newTestNode(1, []uint64{1})
	clearEvents(n)
	n.sendDestory()
	events := collectEvents(n)
	e, ok := findEvent(events, types.Destory)
	assert.True(t, ok)
	assert.Equal(t, uint64(types.LocalNode), e.To)
}
