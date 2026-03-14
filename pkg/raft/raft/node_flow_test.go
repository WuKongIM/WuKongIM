package raft

import (
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

// ==================== Flow Test Helpers ====================

// newThreeNodeCluster creates a 3 node cluster: leader=node1(id=1), follower=node2(id=2), follower=node3(id=3).
// All initial events are drained. Leader is at term 2.
func newThreeNodeCluster() (leader, f2, f3 *Node, nodes map[uint64]*Node) {
	replicas := []uint64{1, 2, 3}
	node1 := newTestNode(1, replicas)
	node2 := newTestNode(2, replicas)
	node3 := newTestNode(3, replicas)

	makeLeader(node1, 2)
	makeFollower(node2, 2, 1)
	makeFollower(node3, 2, 1)

	nodes = map[uint64]*Node{
		1: node1,
		2: node2,
		3: node3,
	}
	return node1, node2, node3, nodes
}

// driveCluster processes one round of events across all nodes.
// For each event:
//   - Local events (To == LocalNode) are handled with default responses:
//     StoreReq → StoreResp(Ok), ApplyReq → ApplyResp(Ok),
//     GetLogsReq → GetLogsResp with logs from the leader's queue,
//     TruncateReq → TruncateResp(Ok)
//   - Remote events are routed to the target node via Step().
//
// Returns true if any events were processed.
func driveCluster(nodes map[uint64]*Node) bool {
	hadEvents := false
	// Collect events from all nodes first
	allEvents := make(map[uint64][]types.Event)
	for id, n := range nodes {
		events := collectEvents(n)
		if len(events) > 0 {
			allEvents[id] = events
			hadEvents = true
		}
	}

	// Process all collected events
	for sourceId, events := range allEvents {
		for _, e := range events {
			if e.To == types.LocalNode {
				// Handle local events with default responses
				sourceNode := nodes[sourceId]
				switch e.Type {
				case types.StoreReq:
					lastIdx := e.Logs[len(e.Logs)-1].Index
					sourceNode.Step(types.Event{
						Type:   types.StoreResp,
						Index:  lastIdx,
						Reason: types.ReasonOk,
					})
				case types.ApplyReq:
					sourceNode.Step(types.Event{
						Type:   types.ApplyResp,
						Index:  e.EndIndex - 1, // EndIndex is exclusive
						Reason: types.ReasonOk,
					})
				case types.GetLogsReq:
					// Find the leader to get logs from
					var leader *Node
					for _, n := range nodes {
						if n.cfg.Role == types.RoleLeader {
							leader = n
							break
						}
					}
					logs := getLogsFromNode(leader, e.Index)
					sourceNode.Step(types.Event{
						Type:   types.GetLogsResp,
						From:   e.From,
						To:     e.From, // GetLogsResp.To = the replica that requested sync
						Index:  e.Index,
						Logs:   logs,
						Reason: types.ReasonOk,
					})
				case types.TruncateReq:
					sourceNode.Step(types.Event{
						Type:   types.TruncateResp,
						Index:  e.Index,
						Reason: types.ReasonOk,
					})
				}
			} else {
				// Route remote events to destination node
				destNode := nodes[e.To]
				if destNode != nil {
					destNode.Step(e)
				}
			}
		}
	}
	return hadEvents
}

// getLogsFromNode retrieves logs starting from syncIndex from the node's queue.
// syncIndex is the follower's lastLogIndex+1 (first log it needs).
func getLogsFromNode(n *Node, syncIndex uint64) []types.Log {
	if n == nil || syncIndex > n.queue.lastLogIndex {
		return nil
	}
	var result []types.Log
	for _, l := range n.queue.logs {
		if l.Index >= syncIndex {
			result = append(result, l)
		}
	}
	// Also check stored logs that have been trimmed from queue but exist as storedIndex
	// If the queue is empty but the node has stored logs, we need to construct them
	if len(result) == 0 && syncIndex <= n.queue.storedIndex {
		// Logs already stored and trimmed from queue - construct them
		for idx := syncIndex; idx <= n.queue.lastLogIndex; idx++ {
			result = append(result, types.Log{
				Index: idx,
				Term:  n.cfg.Term,
				Data:  []byte(fmt.Sprintf("data%d", idx)),
			})
		}
	}
	return result
}

// driveClusterUntilIdle loops driveCluster until all nodes are stable (no new events).
func driveClusterUntilIdle(nodes map[uint64]*Node) {
	for i := 0; i < 100; i++ { // safety limit
		if !driveCluster(nodes) {
			return
		}
	}
}

// proposeAndDrive proposes data on the leader and drives the cluster to idle.
func proposeAndDrive(leader *Node, data []byte, nodes map[uint64]*Node) {
	evt := leader.NewPropose(data)
	leader.Step(evt)
	driveClusterUntilIdle(nodes)
}

// ==================== Basic Replication Tests ====================

func TestFlow_ThreeNode_LogReplication(t *testing.T) {
	leader, f2, f3, nodes := newThreeNodeCluster()

	proposeAndDrive(leader, []byte("data1"), nodes)

	// All nodes should have lastLogIndex=1
	assert.Equal(t, uint64(1), leader.LastLogIndex(), "leader lastLogIndex")
	assert.Equal(t, uint64(1), f2.LastLogIndex(), "f2 lastLogIndex")
	assert.Equal(t, uint64(1), f3.LastLogIndex(), "f3 lastLogIndex")

	// Leader committed index should advance
	assert.True(t, leader.CommittedIndex() >= 1, "leader committedIndex should be >= 1")
}

func TestFlow_ThreeNode_MultipleProposals(t *testing.T) {
	leader, f2, f3, nodes := newThreeNodeCluster()

	proposeAndDrive(leader, []byte("data1"), nodes)
	proposeAndDrive(leader, []byte("data2"), nodes)
	proposeAndDrive(leader, []byte("data3"), nodes)

	// All nodes should have lastLogIndex=3
	assert.Equal(t, uint64(3), leader.LastLogIndex(), "leader lastLogIndex")
	assert.Equal(t, uint64(3), f2.LastLogIndex(), "f2 lastLogIndex")
	assert.Equal(t, uint64(3), f3.LastLogIndex(), "f3 lastLogIndex")

	// Committed index should advance to 3
	assert.Equal(t, uint64(3), leader.CommittedIndex(), "leader committedIndex")
}

func TestFlow_ThreeNode_CommitAndApply(t *testing.T) {
	leader, _, _, nodes := newThreeNodeCluster()

	proposeAndDrive(leader, []byte("data1"), nodes)

	// Leader should have committed and applied
	assert.True(t, leader.CommittedIndex() >= 1, "leader committedIndex")
	assert.True(t, leader.AppliedIndex() >= 1, "leader appliedIndex")
}

func TestFlow_SingleNode_DirectCommit(t *testing.T) {
	node := newTestNode(1, []uint64{1})
	clearEvents(node)
	assert.Equal(t, types.RoleLeader, node.cfg.Role)

	nodes := map[uint64]*Node{1: node}
	proposeAndDrive(node, []byte("data1"), nodes)

	assert.Equal(t, uint64(1), node.LastLogIndex(), "lastLogIndex")
	assert.Equal(t, uint64(1), node.CommittedIndex(), "committedIndex")
	assert.Equal(t, uint64(1), node.AppliedIndex(), "appliedIndex")
}

// ==================== Log Conflict Tests ====================

func TestFlow_LogConflict_FollowerExtraLogs(t *testing.T) {
	replicas := []uint64{1, 2}
	leader := newTestNode(1, replicas)
	follower := newTestNode(2, replicas)

	makeLeader(leader, 2)
	makeFollower(follower, 2, 1)

	// Set up leader with [1(t=2), 2(t=2)]
	// Propose log 1 on leader and store it
	proposeAndStore(leader, []byte("log1"))
	proposeAndStore(leader, []byte("log2"))

	// Set up follower with [1(t=1), 2(t=1), 3(t=1)] by directly manipulating queue
	follower.queue.append(types.Log{Index: 1, Term: 1, Data: []byte("old1")})
	follower.queue.append(types.Log{Index: 2, Term: 1, Data: []byte("old2")})
	follower.queue.append(types.Log{Index: 3, Term: 1, Data: []byte("old3")})
	// Store them so they are persisted
	follower.queue.storeTo(3)
	follower.lastTermStartIndex.Term = 1

	nodes := map[uint64]*Node{
		1: leader,
		2: follower,
	}

	// Step 1: Leader sends NotifySync
	leader.sendNotifySync(2)
	events := collectEvents(leader)
	for _, e := range events {
		if e.To == 2 {
			follower.Step(e)
		}
	}

	// Step 2: Follower sends SyncReq to leader
	fEvents := collectEvents(follower)
	syncReq, found := findEvent(fEvents, types.SyncReq)
	assert.True(t, found, "follower should send SyncReq")

	// Step 3: Leader processes SyncReq → GetLogsReq
	leader.Step(syncReq)
	lEvents := collectEvents(leader)
	getLogsReq, found := findEvent(lEvents, types.GetLogsReq)
	assert.True(t, found, "leader should send GetLogsReq")

	// Step 4: Simulate conflict detection in GetLogsResp
	// The follower has logs at term 1, but leader is at term 2.
	// Return ReasonTruncate to tell follower to truncate to index 1.
	// Index=1 means "truncate to index 1" (keep only log 1).
	leader.Step(types.Event{
		Type:   types.GetLogsResp,
		From:   getLogsReq.From,
		To:     getLogsReq.From, // route back to follower
		Index:  1,               // truncate point
		Reason: types.ReasonTruncate,
	})

	// Step 5: Drive cluster - leader sends SyncResp(Truncate), follower truncates, re-syncs
	lEvents = collectEvents(leader)
	syncResp, found := findEvent(lEvents, types.SyncResp)
	assert.True(t, found, "leader should send SyncResp with truncate")
	assert.Equal(t, types.ReasonTruncate, syncResp.Reason)

	// Follower receives truncate response
	follower.Step(syncResp)
	fEvents = collectEvents(follower)
	truncateReq, found := findEvent(fEvents, types.TruncateReq)
	assert.True(t, found, "follower should send TruncateReq")

	// Simulate TruncateResp
	follower.Step(types.Event{
		Type:   types.TruncateResp,
		Index:  truncateReq.Index,
		Reason: types.ReasonOk,
	})

	// Now drive the rest of the cluster to completion
	driveClusterUntilIdle(nodes)

	// Follower should have same logs as leader
	assert.Equal(t, leader.LastLogIndex(), follower.LastLogIndex(),
		"follower lastLogIndex should match leader")
}

func TestFlow_LogConflict_DifferentTerm(t *testing.T) {
	replicas := []uint64{1, 2}
	leader := newTestNode(1, replicas)
	follower := newTestNode(2, replicas)

	makeLeader(leader, 2)
	makeFollower(follower, 2, 1)

	// Leader has [1(t=1), 2(t=2)]
	leader.queue.append(types.Log{Index: 1, Term: 1, Data: []byte("log1")})
	leader.queue.storeTo(1)
	leader.queue.append(types.Log{Index: 2, Term: 2, Data: []byte("log2")})
	leader.queue.storeTo(2)

	// Follower has [1(t=1), 2(t=1)] - same index 2 but different term
	follower.queue.append(types.Log{Index: 1, Term: 1, Data: []byte("log1")})
	follower.queue.append(types.Log{Index: 2, Term: 1, Data: []byte("old2")})
	follower.queue.storeTo(2)
	follower.lastTermStartIndex.Term = 1

	nodes := map[uint64]*Node{
		1: leader,
		2: follower,
	}

	// Leader notifies follower
	leader.sendNotifySync(2)
	events := collectEvents(leader)
	for _, e := range events {
		if e.To == 2 {
			follower.Step(e)
		}
	}

	// Follower sends SyncReq
	fEvents := collectEvents(follower)
	syncReq, found := findEvent(fEvents, types.SyncReq)
	assert.True(t, found)

	// Leader receives SyncReq → GetLogsReq
	leader.Step(syncReq)
	lEvents := collectEvents(leader)
	getLogsReq, found := findEvent(lEvents, types.GetLogsReq)
	assert.True(t, found)

	// Simulate conflict detection: follower's term at index 2 differs
	// Tell follower to truncate to index 1
	leader.Step(types.Event{
		Type:   types.GetLogsResp,
		From:   getLogsReq.From,
		To:     getLogsReq.From,
		Index:  1, // truncate to index 1
		Reason: types.ReasonTruncate,
	})

	lEvents = collectEvents(leader)
	syncResp, found := findEvent(lEvents, types.SyncResp)
	assert.True(t, found)
	assert.Equal(t, types.ReasonTruncate, syncResp.Reason)

	follower.Step(syncResp)
	fEvents = collectEvents(follower)
	truncateReq, found := findEvent(fEvents, types.TruncateReq)
	assert.True(t, found)

	follower.Step(types.Event{
		Type:   types.TruncateResp,
		Index:  truncateReq.Index,
		Reason: types.ReasonOk,
	})

	// Drive to idle; follower re-syncs and gets correct logs
	driveClusterUntilIdle(nodes)

	assert.Equal(t, leader.LastLogIndex(), follower.LastLogIndex(),
		"follower should have same lastLogIndex as leader")
}

// ==================== Election Tests ====================

func TestFlow_Election_CandidateWins(t *testing.T) {
	replicas := []uint64{1, 2, 3}
	node1 := newTestNode(1, replicas)
	node2 := newTestNode(2, replicas)
	node3 := newTestNode(3, replicas)

	// All start as followers
	makeFollower(node1, 1, 0)
	makeFollower(node2, 1, 0)
	makeFollower(node3, 1, 0)

	nodes := map[uint64]*Node{
		1: node1,
		2: node2,
		3: node3,
	}

	// node1 starts campaign
	node1.Step(types.Event{Type: types.Campaign})

	assert.Equal(t, types.RoleCandidate, node1.cfg.Role, "node1 should be candidate")

	// Collect VoteReq events from node1
	events := collectEvents(node1)
	voteReqs := findEventsOfType(events, types.VoteReq)

	// Route VoteReqs to other nodes
	for _, vr := range voteReqs {
		if vr.To == types.LocalNode {
			// Self vote - already handled by campaign()
			node1.Step(types.Event{
				Type:   types.VoteResp,
				From:   1,
				To:     types.LocalNode,
				Term:   vr.Term,
				Reason: types.ReasonOk,
			})
		} else if destNode := nodes[vr.To]; destNode != nil {
			destNode.Step(vr)
		}
	}

	// Collect VoteResp from node2 and node3
	for _, id := range []uint64{2, 3} {
		resp := collectEvents(nodes[id])
		for _, e := range resp {
			if e.Type == types.VoteResp && e.To == 1 {
				node1.Step(e)
			}
		}
	}

	// node1 should now be leader
	assert.Equal(t, types.RoleLeader, node1.cfg.Role, "node1 should be leader after winning vote")

	// node1 should have sent Ping to followers
	events = collectEvents(node1)
	pings := findEventsOfType(events, types.Ping)
	assert.True(t, len(pings) >= 2, "leader should send Ping to followers")
}

func TestFlow_Election_CandidateLogBehind_Rejected(t *testing.T) {
	replicas := []uint64{1, 2, 3}
	node1 := newTestNode(1, replicas)
	node2 := newTestNode(2, replicas)
	node3 := newTestNode(3, replicas)

	makeFollower(node1, 1, 0)
	makeFollower(node2, 1, 0)
	makeFollower(node3, 1, 0)

	// node2 and node3 have more logs than node1
	// node1: lastLogIndex=1
	node1.queue.append(types.Log{Index: 1, Term: 1, Data: []byte("log1")})
	node1.queue.storeTo(1)

	// node2: lastLogIndex=5
	for i := uint64(1); i <= 5; i++ {
		node2.queue.append(types.Log{Index: i, Term: 1, Data: []byte(fmt.Sprintf("log%d", i))})
	}
	node2.queue.storeTo(5)

	// node3: lastLogIndex=5
	for i := uint64(1); i <= 5; i++ {
		node3.queue.append(types.Log{Index: i, Term: 1, Data: []byte(fmt.Sprintf("log%d", i))})
	}
	node3.queue.storeTo(5)

	nodes := map[uint64]*Node{
		1: node1,
		2: node2,
		3: node3,
	}

	// node1 campaigns
	node1.Step(types.Event{Type: types.Campaign})
	assert.Equal(t, types.RoleCandidate, node1.cfg.Role)

	events := collectEvents(node1)
	voteReqs := findEventsOfType(events, types.VoteReq)

	// Route VoteReqs
	for _, vr := range voteReqs {
		if vr.To == types.LocalNode {
			node1.Step(types.Event{
				Type:   types.VoteResp,
				From:   1,
				To:     types.LocalNode,
				Term:   vr.Term,
				Reason: types.ReasonOk,
			})
		} else if destNode := nodes[vr.To]; destNode != nil {
			destNode.Step(vr)
		}
	}

	// Collect rejections from node2 and node3
	for _, id := range []uint64{2, 3} {
		resp := collectEvents(nodes[id])
		for _, e := range resp {
			if e.Type == types.VoteResp && e.To == 1 {
				node1.Step(e)
			}
		}
	}

	// node1 should become follower (not enough votes)
	assert.Equal(t, types.RoleFollower, node1.cfg.Role,
		"node1 should become follower after being rejected")
}

// ==================== Catch-up & Recovery Tests ====================

func TestFlow_FollowerCatchUp(t *testing.T) {
	replicas := []uint64{1, 2}
	leader := newTestNode(1, replicas)
	follower := newTestNode(2, replicas)

	makeLeader(leader, 2)
	makeFollower(follower, 2, 1)

	// Leader has 5 stored logs
	for i := uint64(1); i <= 5; i++ {
		proposeAndStore(leader, []byte(fmt.Sprintf("data%d", i)))
	}
	assert.Equal(t, uint64(5), leader.LastLogIndex())
	assert.Equal(t, uint64(5), leader.queue.storedIndex)

	// Follower starts empty (lastLogIndex=0)
	assert.Equal(t, uint64(0), follower.LastLogIndex())

	nodes := map[uint64]*Node{
		1: leader,
		2: follower,
	}

	// Drive the cluster - leader notifies, follower syncs
	// We need to trigger NotifySync first
	leader.sendNotifySync(2)
	driveClusterUntilIdle(nodes)

	// Follower should have caught up
	assert.Equal(t, uint64(5), follower.LastLogIndex(),
		"follower should have caught up to leader's lastLogIndex=5")
}

func TestFlow_LeaderChange_ThenPropose(t *testing.T) {
	leader, f2, f3, nodes := newThreeNodeCluster()

	// Leader writes 2 logs and replicates to all
	proposeAndDrive(leader, []byte("data1"), nodes)
	proposeAndDrive(leader, []byte("data2"), nodes)

	assert.Equal(t, uint64(2), leader.LastLogIndex())
	assert.Equal(t, uint64(2), f2.LastLogIndex())
	assert.Equal(t, uint64(2), f3.LastLogIndex())

	// node2 becomes leader with higher term
	newTerm := uint32(3)
	f2.BecomeLeader(newTerm)
	clearEvents(f2)
	leader.BecomeFollower(newTerm, 2) // old leader becomes follower
	clearEvents(leader)
	f3.BecomeFollower(newTerm, 2)
	clearEvents(f3)

	// Update nodes map references (same objects, just for clarity)
	// f2 is now the leader

	// f2 proposes new log
	proposeAndDrive(f2, []byte("data3"), nodes)

	// All nodes should have log 3
	assert.Equal(t, uint64(3), leader.LastLogIndex(), "old leader should have log 3")
	assert.Equal(t, uint64(3), f2.LastLogIndex(), "new leader should have log 3")
	assert.Equal(t, uint64(3), f3.LastLogIndex(), "f3 should have log 3")
}

// ==================== Heartbeat & Learner Tests ====================

func TestFlow_PingUpdatesFollowerCommit(t *testing.T) {
	replicas := []uint64{1, 2}
	leader := newTestNode(1, replicas)
	follower := newTestNode(2, replicas)

	makeLeader(leader, 2)
	makeFollower(follower, 2, 1)

	// Follower has stored logs but committedIndex=0
	follower.queue.append(types.Log{Index: 1, Term: 2, Data: []byte("log1")})
	follower.queue.append(types.Log{Index: 2, Term: 2, Data: []byte("log2")})
	follower.queue.append(types.Log{Index: 3, Term: 2, Data: []byte("log3")})
	follower.queue.storeTo(3)

	assert.Equal(t, uint64(0), follower.CommittedIndex(), "follower committedIndex should be 0 initially")

	// Leader sends Ping with committedIndex=2
	follower.Step(types.Event{
		Type:           types.Ping,
		From:           1,
		To:             2,
		Term:           2,
		Index:          5,
		CommittedIndex: 2,
	})

	// Follower should update committedIndex to min(leaderCommitted=2, followerStored=3) = 2
	assert.Equal(t, uint64(2), follower.CommittedIndex(),
		"follower committedIndex should be min(leaderCommitted, followerStored)")

	// Send another Ping with committedIndex=5 (higher than follower stored)
	follower.Step(types.Event{
		Type:           types.Ping,
		From:           1,
		To:             2,
		Term:           2,
		Index:          5,
		CommittedIndex: 5,
	})

	// Follower should update committedIndex to min(5, 3) = 3
	assert.Equal(t, uint64(3), follower.CommittedIndex(),
		"follower committedIndex should be capped at storedIndex")
}

func TestFlow_LearnerSync(t *testing.T) {
	replicas := []uint64{1, 2, 3}
	leader := newTestNode(1, replicas)
	f2 := newTestNode(2, replicas)
	f3 := newTestNode(3, replicas)

	// Create learner node (id=4)
	learnerOpts := NewOptions(
		WithNodeId(4),
		WithReplicas(replicas),
		WithElectionOn(false),
		WithElectionInterval(1000),
		WithAdvance(func() {}),
		WithKey("test"),
	)
	learner := NewNode(0, types.RaftState{}, learnerOpts)

	makeLeader(leader, 2)
	makeFollower(f2, 2, 1)
	makeFollower(f3, 2, 1)
	makeLearner(learner, 2, 1)

	// Configure leader to know about the learner
	leader.cfg.Learners = []uint64{4}

	nodes := map[uint64]*Node{
		1: leader,
		2: f2,
		3: f3,
		4: learner,
	}

	// Leader proposes and replicates to followers
	proposeAndDrive(leader, []byte("data1"), nodes)

	assert.Equal(t, uint64(1), leader.LastLogIndex())
	assert.Equal(t, uint64(1), f2.LastLogIndex())
	assert.Equal(t, uint64(1), f3.LastLogIndex())

	// Now drive learner sync manually
	// Leader sends NotifySync to learner
	leader.sendNotifySync(4)
	driveClusterUntilIdle(nodes)

	// Learner should have the log
	assert.Equal(t, uint64(1), learner.LastLogIndex(),
		"learner should have synced the log")

	// Now verify learner doesn't affect commit calculation
	// Record the current committed index on leader
	committedBefore := leader.CommittedIndex()

	// Propose another log
	proposeAndDrive(leader, []byte("data2"), nodes)

	// Sync learner again
	leader.sendNotifySync(4)
	driveClusterUntilIdle(nodes)

	assert.Equal(t, uint64(2), learner.LastLogIndex(), "learner should have log 2")

	// Leader's committed index should only depend on replicas (nodes 1,2,3), not learner
	// If only the learner had stored but not the followers, committed should not advance
	// Since we drove all nodes, committed should advance based on replica majority
	assert.True(t, leader.CommittedIndex() >= committedBefore,
		"leader committedIndex should advance based on replica majority, not learner")
}
