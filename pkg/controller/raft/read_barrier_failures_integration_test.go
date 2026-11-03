//go:build integration

package raft

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

// Failure inventory is recorded before implementation in the retained
// EXTRA-FAILURE-INVENTORY-v1.json. These tests drive real Raft messages and
// persistent FSM ports. They never manufacture ReadState or mutate applied.

func TestReadBarrierRejectsForwardedReadsAtPublicStep(t *testing.T) {
	c := newRaftTestCluster(t, []uint64{1, 2, 3})
	c.start(t)
	c.propose(t, testInitCommand("wk-read-entry-policy", c.peers))
	c.waitForRevision(t, 1)
	leader := c.waitForLeader(t)
	peer := readBarrierOtherNode(t, c, leader.id)
	for _, kind := range []raftpb.MessageType{raftpb.MsgReadIndex, raftpb.MsgReadIndexResp} {
		t.Run(kind.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := leader.service.Step(ctx, raftpb.Message{From: peer.id, To: leader.id, Term: leader.service.Status().Term, Type: kind, Entries: []raftpb.Entry{{Data: []byte("unowned-network-read")}}})
			require.ErrorIs(t, err, ErrReadBarrierForwardingUnsupported,
				"network read forwarding must not bypass the only bounded read owner")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, leader.service.Step(ctx, raftpb.Message{From: peer.id, To: leader.id, Term: leader.service.Status().Term, Type: raftpb.MsgHeartbeatResp, Context: []byte("old-heartbeat")}),
		"normal context-bearing quorum protocol remains supported")
}

func TestReadBarrierIsolatedLeaderCannotProveFreshness(t *testing.T) {
	c := newRaftTestCluster(t, []uint64{1, 2, 3})
	c.start(t)
	c.propose(t, testInitCommand("wk-read-isolated", c.peers))
	c.waitForRevision(t, 1)
	leader := c.waitForLeader(t)
	require.Equal(t, RoleLeader, leader.service.Status().Role)
	c.transport.setInterceptor(func(m raftpb.Message) bool {
		return m.From == leader.id || m.To == leader.id
	})
	t.Cleanup(func() { c.transport.setInterceptor(nil) })
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := leader.service.ReadBarrier(ctx)
	require.Error(t, err, "a locally remembered leader cannot replace a current quorum")
	require.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrNotLeader), "unexpected isolated-read failure: %v", err)
}

func TestReadBarrierWaitsForActualPersistentApply(t *testing.T) {
	c, gate := newReadBarrierGatedCluster(t)
	node := c.nodes[0]
	block := gate.holdNext(nil)
	t.Cleanup(block.release)
	proposal := startReadBarrierProposal(t, node, testUpsertNodeCommand(1, 1, "read-visible-after-save"))
	waitReadBarrierSignal(t, block.entered, "committed command never reached actual FSM apply")
	read := startReadBarrierCall(t, node.service, context.Background())
	assertReadBarrierPending(t, read, "volatile RawNode applied progress bypassed a blocked persistent FSM")
	block.release()
	require.NoError(t, proposal.wait(t))
	result := read.wait(t)
	require.NoError(t, result.err)
	snapshot := node.stateMachine.Snapshot(context.Background())
	require.Equal(t, "read-visible-after-save", findTestNode(t, snapshot, 1).Name)
	require.GreaterOrEqual(t, result.index, snapshot.AppliedRaftIndex)
}

func TestReadBarrierKnownLeaderLossDoesNotWaitForBlockedConfigurationApply(t *testing.T) {
	c := newRaftTestCluster(t, []uint64{1, 2, 3})
	gates := make(map[uint64]*readBarrierGatedFSM, len(c.nodes))
	for _, node := range c.nodes {
		gate := &readBarrierGatedFSM{stateMachine: node.stateMachine}
		node.stateMachine = gate
		gates[node.id] = gate
		c.rebuildService(t, node)
	}
	c.start(t)
	c.propose(t, testInitCommand("wk-read-known-leader-loss", c.peers))
	c.waitForRevision(t, 1)
	leader := c.waitForLeader(t)
	peer := readBarrierOtherNode(t, c, leader.id)
	block := gates[leader.id].holdNext(nil)
	// Release the actual FSM before cleanup joins callers or stops the cluster,
	// including the expected RED path where the assertion below aborts the test.
	defer block.release()
	proposal := startReadBarrierProposal(t, leader, testUpsertNodeCommand(1, leader.id, "held-across-known-leader-loss"))
	waitReadBarrierSignal(t, block.entered, "ordinary committed apply never reached the FSM gate")

	wire := newReadBarrierWireGate(leader.id, c.transport.deliver)
	c.transport.setInterceptor(wire.intercept)
	t.Cleanup(func() { c.transport.setInterceptor(nil) })
	read := startReadBarrierCall(t, leader.service, context.Background())
	token := wire.nextToken(t)
	replies := wire.repliesFor(t, token)
	wire.open()
	for _, reply := range replies {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := leader.service.Step(ctx, reply)
		cancel()
		require.NoError(t, err)
	}
	assertReadBarrierPending(t, read, "a quorum-confirmed read bypassed the held durable apply")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	page, err := leader.service.LogEntries(ctx, LogEntriesOptions{Limit: 1})
	cancel()
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	previous := page.Items[0]
	require.Equal(t, previous.Index, page.CommitIndex, "fixture append must match the actual committed prefix")
	oldTerm := leader.service.Status().Term
	require.Equal(t, RoleLeader, leader.service.Status().Role)

	// The configured peer's higher-term append is an ordinary public protocol
	// fault seam. Its prefix comes from the real persisted log; the committed
	// configuration entry queues behind the already-held ordinary FSM apply.
	// No ReadState or applied watermark is injected by this fixture.
	c.transport.setInterceptor(func(m raftpb.Message) bool {
		return m.From == leader.id || m.To == leader.id
	})
	change, err := (&raftpb.ConfChange{Type: raftpb.ConfChangeAddLearnerNode, NodeID: 4}).Marshal()
	require.NoError(t, err)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	err = leader.service.Step(ctx, raftpb.Message{
		Type: raftpb.MsgApp, From: peer.id, To: leader.id, Term: oldTerm + 1,
		Index: previous.Index, LogTerm: previous.Term, Commit: previous.Index + 1,
		Entries: []raftpb.Entry{{Index: previous.Index + 1, Term: oldTerm + 1, Type: raftpb.EntryConfChange, Data: change}},
	})
	cancel()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		status := leader.service.Status()
		return status.Role == RoleFollower && status.Term == oldTerm+1
	}, time.Second, time.Millisecond, "public Step never made leadership loss known")
	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		page, err := leader.service.LogEntries(ctx, LogEntriesOptions{Limit: 1})
		return err == nil && len(page.Items) == 1 && page.Items[0].Index == previous.Index+1 && page.Items[0].Type == "conf_change"
	}, time.Second, time.Millisecond, "higher-term configuration Ready never reached durable WAL")
	select {
	case <-read.done:
		require.ErrorIs(t, read.result.err, ErrNotLeader,
			"a known role loss must fail the pending read before an unrelated apply can resume")
	case <-time.After(time.Second):
		t.Fatal("known leadership loss left ReadBarrier waiting behind blocked configuration apply")
	}
	block.release()
	require.ErrorIs(t, proposal.wait(t), ErrNotLeader)
}

func TestReadBarrierApplyFailureNeverReturnsOldSnapshot(t *testing.T) {
	c, gate := newReadBarrierGatedCluster(t)
	node := c.nodes[0]
	injected := errors.New("read barrier fixture: FSM persistence failed")
	block := gate.holdNext(injected)
	t.Cleanup(block.release)
	proposal := startReadBarrierProposal(t, node, testUpsertNodeCommand(1, 1, "must-not-be-visible"))
	waitReadBarrierSignal(t, block.entered, "failing command never reached actual FSM port")
	read := startReadBarrierCall(t, node.service, context.Background())
	assertReadBarrierPending(t, read, "read escaped before the blocked failing apply resolved")
	block.release()
	require.ErrorIs(t, proposal.wait(t), injected)
	require.Error(t, read.wait(t).err, "failed apply cannot grant a successful older cut")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := node.service.ReadBarrier(ctx)
	require.Error(t, err, "a degraded/stopped service must keep subsequent reads failed closed")
	require.NotEqual(t, "must-not-be-visible", findTestNode(t, node.stateMachine.Snapshot(context.Background()), 1).Name)
}

func TestReadBarrierDurabilityWriteFailuresDoNotGrantProgress(t *testing.T) {
	for _, target := range []string{"materialized_state", "applied_marker"} {
		t.Run(target, func(t *testing.T) {
			c, gate := newReadBarrierGatedCluster(t)
			node := c.nodes[0]
			block := gate.holdNext(nil)
			t.Cleanup(block.release)
			proposal := startReadBarrierProposal(t, node, testUpsertNodeCommand(1, 1, "new-state-before-marker"))
			waitReadBarrierSignal(t, block.entered, "command never reached the persistent FSM port")
			read := startReadBarrierCall(t, node.service, context.Background())
			assertReadBarrierPending(t, read, "read escaped the actual pending durable write")
			path := node.statePath
			if target == "applied_marker" {
				path = filepath.Join(node.raftDir, "meta.json")
			}
			// This is an actual filesystem rename failure, not a forged applied
			// value or injected success. WAL/commit already precede the held FSM.
			require.NoError(t, os.Rename(path, path+".fixture-before-failure"))
			require.NoError(t, os.Mkdir(path, 0o700))
			block.release()
			require.Error(t, proposal.wait(t))
			require.Error(t, read.wait(t).err)
			snapshot := node.stateMachine.Snapshot(context.Background())
			if target == "applied_marker" {
				require.Equal(t, "new-state-before-marker", findTestNode(t, snapshot, 1).Name,
					"fixture must reach the real marker after FSM publication")
			} else {
				require.NotEqual(t, "new-state-before-marker", findTestNode(t, snapshot, 1).Name)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := node.service.ReadBarrier(ctx)
			require.Error(t, err, "partially updated in-memory metadata is not a durable read watermark")
		})
	}
}

func TestReadBarrierCanceledIssuedTokensKeepThe64Bound(t *testing.T) {
	c := newRaftTestCluster(t, []uint64{1, 2, 3})
	c.start(t)
	c.propose(t, testInitCommand("wk-read-token-bound", c.peers))
	c.waitForRevision(t, 1)
	leader := c.waitForLeader(t)
	wire := newReadBarrierWireGate(leader.id, c.transport.deliver)
	c.transport.setInterceptor(wire.intercept)
	t.Cleanup(func() { c.transport.setInterceptor(nil) })
	for i := 0; i < 64; i++ {
		call := startReadBarrierCall(t, leader.service, context.Background())
		wire.nextToken(t)
		call.cancel()
		require.ErrorIs(t, call.wait(t).err, context.Canceled)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := leader.service.ReadBarrier(ctx)
	require.ErrorIs(t, err, ErrReadBarrierBusy, "canceled RawNode-owned tokens must not be refunded early")
	require.Equal(t, 64, wire.tokenCount(), "overload must not submit an extra ReadIndex token")
	wire.open()
	// A real later quorum heartbeat retires the canceled ReadIndex contexts.
	// Retry only the admission probe, never a product command or workload.
	require.Eventually(t, func() bool {
		attempt, stop := context.WithTimeout(context.Background(), time.Second)
		_, err := leader.service.ReadBarrier(attempt)
		stop()
		return err == nil
	}, 4*time.Second, 10*time.Millisecond)
}

func TestReadBarrierLeaderABARequiresItsNewQuorum(t *testing.T) {
	c := newRaftTestCluster(t, []uint64{1, 2, 3})
	c.start(t)
	c.propose(t, testInitCommand("wk-read-leader-aba", c.peers))
	c.waitForRevision(t, 1)
	original := c.waitForLeader(t)
	wire := newReadBarrierWireGate(original.id, c.transport.deliver)
	c.transport.setInterceptor(wire.intercept)
	t.Cleanup(func() { c.transport.setInterceptor(nil) })
	old := startReadBarrierCall(t, original.service, context.Background())
	oldToken := wire.nextToken(t)
	oldReplies := wire.repliesFor(t, oldToken)
	peer := readBarrierOtherNode(t, c, original.id)
	oldTerm := original.service.Status().Term
	transferReadBarrierLeadership(t, original, peer)
	require.Error(t, old.wait(t).err, "old-term read survived leadership loss")
	transferReadBarrierLeadership(t, peer, original)
	require.Greater(t, original.service.Status().Term, oldTerm)
	current := startReadBarrierCall(t, original.service, context.Background())
	newToken := wire.nextToken(t)
	require.False(t, bytes.Equal(oldToken, newToken), "request identity was reused across a leader term")
	for _, reply := range oldReplies {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		require.NoError(t, original.service.Step(ctx, reply))
		cancel()
	}
	assertReadBarrierPending(t, current, "old heartbeat contexts satisfied a different current-term read")
	wire.open()
	require.NoError(t, current.wait(t).err)
}

func TestReadBarrierStopRestartAfterSnapshotUsesNewIdentity(t *testing.T) {
	c := newRaftTestCluster(t, []uint64{1, 2, 3})
	c.start(t)
	c.propose(t, testInitCommand("wk-read-start-aba", c.peers))
	c.waitForRevision(t, 1)
	original := c.waitForLeader(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	compact, err := original.service.CompactLog(ctx)
	cancel()
	require.NoError(t, err)
	require.True(t, compact.Compacted)
	wire := newReadBarrierWireGate(original.id, c.transport.deliver)
	c.transport.setInterceptor(wire.intercept)
	t.Cleanup(func() { c.transport.setInterceptor(nil) })
	old := startReadBarrierCall(t, original.service, context.Background())
	oldToken := wire.nextToken(t)
	oldReplies := wire.repliesFor(t, oldToken)
	stopDone := make(chan error, 1)
	go func() { stopDone <- original.service.Stop() }()
	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Stop waited for a new ReadState instead of failing pending reads")
	}
	require.Error(t, old.wait(t).err)
	var replacement *testRaftNode
	require.Eventually(t, func() bool {
		for _, node := range c.nodes {
			if node.id != original.id && node.service.Status().Role == RoleLeader {
				replacement = node
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, original.service.Start(context.Background()))
	transferReadBarrierLeadership(t, replacement, original)
	require.GreaterOrEqual(t, original.service.Status().SnapshotIndex, compact.AfterSnapshotIndex)
	current := startReadBarrierCall(t, original.service, context.Background())
	newToken := wire.nextToken(t)
	require.False(t, bytes.Equal(oldToken, newToken), "request identity was reused after Service restart")
	for _, reply := range oldReplies {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		require.NoError(t, original.service.Step(ctx, reply))
		cancel()
	}
	assertReadBarrierPending(t, current, "an old Start's quorum reply completed the restarted reader")
	wire.open()
	result := current.wait(t)
	require.NoError(t, result.err)
	require.GreaterOrEqual(t, result.index, compact.AppliedIndex)
	require.Equal(t, uint64(1), original.stateMachine.Snapshot(context.Background()).Revision)
}

func TestReadBarrierBeforeCurrentTermCommitKeepsTokensOutsideRawNode(t *testing.T) {
	c := newRaftTestCluster(t, []uint64{1, 2, 3})
	c.start(t)
	c.propose(t, testInitCommand("wk-read-term-guard", c.peers))
	c.waitForRevision(t, 1)
	original := c.waitForLeader(t)
	target := readBarrierOtherNode(t, c, original.id)
	previousTerm := original.service.Status().Term
	var hold atomic.Bool
	hold.Store(true)
	var readContexts atomic.Int32
	c.transport.setInterceptor(func(m raftpb.Message) bool {
		if m.From == target.id && m.Type == raftpb.MsgHeartbeat && len(m.Context) != 0 {
			readContexts.Add(1)
		}
		if m.From != target.id || m.Type != raftpb.MsgApp || !hold.Load() {
			return false
		}
		for _, entry := range m.Entries {
			if entry.Term > previousTerm {
				return true
			}
		}
		return false
	})
	t.Cleanup(func() { hold.Store(false); c.transport.setInterceptor(nil) })
	transferReadBarrierLeadership(t, original, target)
	require.Greater(t, target.service.Status().Term, previousTerm)
	// Filling the public admission cap proves all 64 requests were accepted.
	// They cannot enter etcd's uncancelable pre-current-term pending queue.
	readBarrierPendingBatch(t, target.service, false)
	require.Zero(t, readContexts.Load(), "ReadIndex entered RawNode before a current-term entry committed")
	// Canceled, not-yet-issued work must leave capacity reusable before commit.
	readBarrierPendingBatch(t, target.service, true)
	require.Zero(t, readContexts.Load(), "cancellation/re-admission leaked a pre-commit ReadIndex token")
	hold.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := target.service.ReadBarrier(ctx)
	require.NoError(t, err)
	require.Positive(t, readContexts.Load(), "healthy completion must obtain an actual current-term quorum")
}

type readBarrierOutcome struct {
	index uint64
	err   error
}

type readBarrierCall struct {
	cancel context.CancelFunc
	done   chan struct{}
	result readBarrierOutcome
}

func startReadBarrierCall(t *testing.T, service *Service, parent context.Context) *readBarrierCall {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	call := &readBarrierCall{cancel: cancel, done: make(chan struct{})}
	go func() {
		call.result.index, call.result.err = service.ReadBarrier(ctx)
		close(call.done)
	}()
	t.Cleanup(func() { cancel(); call.wait(t) })
	return call
}

func (c *readBarrierCall) wait(t *testing.T) readBarrierOutcome {
	t.Helper()
	select {
	case <-c.done:
		return c.result
	case <-time.After(5 * time.Second):
		t.Fatal("read caller did not join within its bounded fixture allowance")
		return readBarrierOutcome{}
	}
}

func assertReadBarrierPending(t *testing.T, call *readBarrierCall, reason string) {
	t.Helper()
	select {
	case <-call.done:
		t.Fatalf("%s: index=%d err=%v", reason, call.result.index, call.result.err)
	case <-time.After(100 * time.Millisecond):
	}
}

type readBarrierProposal struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func startReadBarrierProposal(t *testing.T, node *testRaftNode, cmd command.Command) *readBarrierProposal {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &readBarrierProposal{cancel: cancel, done: make(chan struct{})}
	go func() { p.err = node.service.Propose(ctx, cmd); close(p.done) }()
	t.Cleanup(func() { cancel(); p.wait(t) })
	return p
}

func (p *readBarrierProposal) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-p.done:
		return p.err
	case <-time.After(5 * time.Second):
		t.Fatal("proposal caller did not join")
		return nil
	}
}

type readBarrierBatchGate struct {
	entered chan struct{}
	open    chan struct{}
	once    sync.Once
	err     error
}

func (g *readBarrierBatchGate) release() { g.once.Do(func() { close(g.open) }) }

type readBarrierGatedFSM struct {
	stateMachine
	mu   sync.Mutex
	next *readBarrierBatchGate
}

func (g *readBarrierGatedFSM) holdNext(err error) *readBarrierBatchGate {
	g.mu.Lock()
	defer g.mu.Unlock()
	gate := &readBarrierBatchGate{entered: make(chan struct{}), open: make(chan struct{}), err: err}
	g.next = gate
	return gate
}

func (g *readBarrierGatedFSM) ApplyBatch(ctx context.Context, entries []fsm.AppliedCommand) (fsm.BatchApplyResult, error) {
	g.mu.Lock()
	gate := g.next
	g.next = nil
	g.mu.Unlock()
	if gate != nil {
		close(gate.entered)
		select {
		case <-gate.open:
		case <-ctx.Done():
			return fsm.BatchApplyResult{}, ctx.Err()
		}
		if gate.err != nil {
			return fsm.BatchApplyResult{}, gate.err
		}
	}
	return g.stateMachine.ApplyBatch(ctx, entries)
}

func newReadBarrierGatedCluster(t *testing.T) (*testRaftCluster, *readBarrierGatedFSM) {
	t.Helper()
	c := newRaftTestCluster(t, []uint64{1})
	gate := &readBarrierGatedFSM{stateMachine: c.nodes[0].stateMachine}
	c.nodes[0].stateMachine = gate
	c.rebuildService(t, c.nodes[0])
	c.start(t)
	c.propose(t, testInitCommand("wk-read-durable-gate", c.peers))
	c.waitForRevision(t, 1)
	return c, gate
}

// readBarrierWireGate holds actual context-bearing quorum responses. Because
// etcd's periodic heartbeat also reuses the newest pending read context, the
// controlled link forwards a context-free liveness response while retaining the
// actual quorum context. This models ordinary liveness ACKs independently from
// missing read ACKs; it never fabricates ReadState or application progress.
type readBarrierWireGate struct {
	mu       sync.Mutex
	leader   uint64
	hold     bool
	seen     map[string]bool
	replies  map[string][]raftpb.Message
	tokens   chan []byte
	ordinary func(raftpb.Message)
}

func newReadBarrierWireGate(leader uint64, ordinary func(raftpb.Message)) *readBarrierWireGate {
	return &readBarrierWireGate{leader: leader, hold: true, seen: make(map[string]bool), replies: make(map[string][]raftpb.Message), tokens: make(chan []byte, 128), ordinary: ordinary}
}

func (g *readBarrierWireGate) intercept(m raftpb.Message) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.hold {
		return false
	}
	if m.From == g.leader && m.Type == raftpb.MsgHeartbeat && len(m.Context) != 0 {
		key := string(m.Context)
		if !g.seen[key] {
			g.seen[key] = true
			select {
			case g.tokens <- bytes.Clone(m.Context):
			default:
			}
		}
	}
	if m.To == g.leader && m.Type == raftpb.MsgHeartbeatResp && len(m.Context) != 0 {
		liveness := m
		liveness.Context = nil
		g.ordinary(liveness)
		key := string(m.Context)
		m.Context = bytes.Clone(m.Context)
		// Keep one reply per peer, not every retransmitted heartbeat.
		for _, previous := range g.replies[key] {
			if previous.From == m.From {
				return true
			}
		}
		g.replies[key] = append(g.replies[key], m)
		return true
	}
	return false
}

func (g *readBarrierWireGate) nextToken(t *testing.T) []byte {
	t.Helper()
	select {
	case token := <-g.tokens:
		return token
	case <-time.After(3 * time.Second):
		t.Fatal("read never submitted an observable quorum token")
		return nil
	}
}

func (g *readBarrierWireGate) repliesFor(t *testing.T, token []byte) []raftpb.Message {
	t.Helper()
	var replies []raftpb.Message
	require.Eventually(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		replies = append([]raftpb.Message(nil), g.replies[string(token)]...)
		return len(replies) == 2
	}, 3*time.Second, time.Millisecond)
	return replies
}

func (g *readBarrierWireGate) tokenCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.seen)
}

func (g *readBarrierWireGate) open() {
	g.mu.Lock()
	g.hold = false
	g.mu.Unlock()
}

func transferReadBarrierLeadership(t *testing.T, current, target *testRaftNode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	// MsgTransferLeader is the ordinary configured follower's transfer request;
	// etcd sends TimeoutNow and runs a real election for the target.
	require.NoError(t, current.service.Step(ctx, raftpb.Message{Type: raftpb.MsgTransferLeader, From: target.id, To: current.id}))
	cancel()
	require.Eventually(t, func() bool { return target.service.Status().Role == RoleLeader }, 5*time.Second, 10*time.Millisecond)
}

func readBarrierOtherNode(t *testing.T, c *testRaftCluster, id uint64) *testRaftNode {
	t.Helper()
	for _, node := range c.nodes {
		if node.id != id {
			return node
		}
	}
	t.Fatal("missing second configured voter")
	return nil
}

func waitReadBarrierSignal(t *testing.T, signal <-chan struct{}, reason string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal(reason)
	}
}

func readBarrierPendingBatch(t *testing.T, service *Service, allowCancelNotificationDrain bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ctx, cancel := context.WithCancel(context.Background())
		results := make(chan error, 65)
		joined := make(chan struct{})
		var remaining atomic.Int32
		remaining.Store(65)
		for range 65 {
			go func() {
				defer func() {
					if remaining.Add(-1) == 0 {
						close(joined)
					}
				}()
				_, err := service.ReadBarrier(ctx)
				results <- err
			}()
		}
		t.Cleanup(func() {
			cancel()
			select {
			case <-joined:
			case <-time.After(3 * time.Second):
				t.Error("pending read callers did not join")
			}
		})
		busy, canceled := 0, 0
		select {
		case err := <-results:
			require.ErrorIs(t, err, ErrReadBarrierBusy, "the 65th pre-current-term request must fail admission")
			busy++
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("bounded read admission did not reject its 65th pending request")
		}
		cancel()
		for range 64 {
			select {
			case err := <-results:
				switch {
				case errors.Is(err, ErrReadBarrierBusy):
					busy++
				case errors.Is(err, context.Canceled):
					canceled++
				default:
					t.Fatalf("not-yet-issued read lost caller cancellation: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled pre-current-term request did not return")
			}
		}
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Fatal("pending read callers did not join")
		}
		if busy == 1 && canceled == 64 {
			return
		}
		if !allowCancelNotificationDrain || time.Now().After(deadline) {
			t.Fatalf("full 64-read capacity was not reusable: busy=%d canceled=%d", busy, canceled)
		}
		// Caller return can precede the owner's coalesced cancellation wakeup.
		// Wait for physical retirement rather than requiring synchronous refund.
		time.Sleep(time.Millisecond)
	}
}
