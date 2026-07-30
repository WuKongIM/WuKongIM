package multiraft

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

type testCluster struct {
	mu         sync.RWMutex
	network    *asyncTestNetwork
	runtimes   map[NodeID]*Runtime
	transports map[NodeID]*clusterTransport
	stores     map[NodeID]map[SlotID]*internalFakeStorage
	fsms       map[NodeID]map[SlotID]*internalFakeStateMachine
}

type asyncNetworkConfig struct {
	MaxDelay time.Duration
	Seed     int64
}

type asyncTestNetwork struct {
	maxDelay time.Duration
	stopCh   chan struct{}

	mu     sync.Mutex
	closed bool
	rng    *rand.Rand
	err    error
	wg     sync.WaitGroup

	blocked map[networkLink]struct{}

	cluster *testCluster
}

type networkLink struct {
	from NodeID
	to   NodeID
}

func newAsyncTestCluster(t testing.TB, nodeIDs []NodeID, cfg asyncNetworkConfig) *testCluster {
	return newAsyncTestClusterWithObserver(t, nodeIDs, cfg, nil)
}

func newAsyncTestClusterWithObserver(t testing.TB, nodeIDs []NodeID, cfg asyncNetworkConfig, observer SchedulerObserver) *testCluster {
	t.Helper()

	network := newAsyncTestNetwork(cfg)
	cluster := &testCluster{
		network:    network,
		runtimes:   make(map[NodeID]*Runtime),
		transports: make(map[NodeID]*clusterTransport),
		stores:     make(map[NodeID]map[SlotID]*internalFakeStorage),
		fsms:       make(map[NodeID]map[SlotID]*internalFakeStateMachine),
	}
	network.cluster = cluster

	for _, nodeID := range nodeIDs {
		transport := &clusterTransport{network: network, from: nodeID}
		rt, err := New(Options{
			NodeID:       nodeID,
			TickInterval: 10 * time.Millisecond,
			Workers:      1,
			Transport:    transport,
			Observer:     observer,
			Raft: RaftOptions{
				ElectionTick:  20,
				HeartbeatTick: 1,
			},
		})
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", nodeID, err)
		}

		cluster.runtimes[nodeID] = rt
		cluster.transports[nodeID] = transport
		cluster.stores[nodeID] = make(map[SlotID]*internalFakeStorage)
		cluster.fsms[nodeID] = make(map[SlotID]*internalFakeStateMachine)
	}

	t.Cleanup(func() {
		network.close()
		for _, rt := range cluster.runtimes {
			if err := rt.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		}
	})

	return cluster
}

type slotLeaderChangeObserver struct {
	mu      sync.Mutex
	changes []slotLeaderChangeObservation
}

type slotLeaderChangeObservation struct {
	slotID SlotID
	from   NodeID
	to     NodeID
	cause  LeaderChangeCause
}

func (o *slotLeaderChangeObserver) SetSchedulerWorkers(int) {}

func (o *slotLeaderChangeObserver) SetSchedulerInflight(int) {}

func (o *slotLeaderChangeObserver) SetSchedulerState(SchedulerStateEvent) {}

func (o *slotLeaderChangeObserver) ObserveSchedulerAdmission(string) {}

func (o *slotLeaderChangeObserver) ObserveSchedulerTask(string, time.Duration) {}

func (o *slotLeaderChangeObserver) ObserveSlotLeaderChange(slotID SlotID, from, to NodeID) {
	o.ObserveSlotLeaderChangeWithCause(slotID, from, to, LeaderChangeCauseElection)
}

func (o *slotLeaderChangeObserver) ObserveSlotLeaderChangeWithCause(slotID SlotID, from, to NodeID, cause LeaderChangeCause) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.changes = append(o.changes, slotLeaderChangeObservation{slotID: slotID, from: from, to: to, cause: cause})
}

func (o *slotLeaderChangeObserver) clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.changes = nil
}

func (o *slotLeaderChangeObserver) waitForTarget(t testing.TB, slotID SlotID, to NodeID) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		for _, change := range o.changes {
			if change.slotID == slotID && change.to == to {
				o.mu.Unlock()
				return
			}
		}
		o.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	o.mu.Lock()
	observed := append([]slotLeaderChangeObservation(nil), o.changes...)
	o.mu.Unlock()
	t.Fatalf("leader changes = %#v, want slot=%d to=%d", observed, slotID, to)
}

func (o *slotLeaderChangeObserver) waitForTargetCause(t testing.TB, slotID SlotID, to NodeID, cause LeaderChangeCause) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		for _, change := range o.changes {
			if change.slotID == slotID && change.to == to && change.cause == cause {
				o.mu.Unlock()
				return
			}
		}
		o.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	o.mu.Lock()
	observed := append([]slotLeaderChangeObservation(nil), o.changes...)
	o.mu.Unlock()
	t.Fatalf("leader changes = %#v, want slot=%d to=%d cause=%q", observed, slotID, to, cause)
}

func newAsyncTestNetwork(cfg asyncNetworkConfig) *asyncTestNetwork {
	if cfg.Seed == 0 {
		cfg.Seed = 1
	}
	return &asyncTestNetwork{
		maxDelay: cfg.MaxDelay,
		stopCh:   make(chan struct{}),
		rng:      rand.New(rand.NewSource(cfg.Seed)),
		blocked:  make(map[networkLink]struct{}),
	}
}

func (c *testCluster) runtime(nodeID NodeID) *Runtime {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimes[nodeID]
}

func (c *testCluster) otherNodes(nodeID NodeID) []NodeID {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]NodeID, 0, len(c.runtimes)-1)
	for id := range c.runtimes {
		if id != nodeID {
			out = append(out, id)
		}
	}
	return out
}

func (c *testCluster) bootstrapSlot(t testing.TB, slotID SlotID, voters []NodeID) {
	t.Helper()

	for _, nodeID := range voters {
		store := &internalFakeStorage{}
		fsm := &internalFakeStateMachine{}
		c.stores[nodeID][slotID] = store
		c.fsms[nodeID][slotID] = fsm

		err := c.runtime(nodeID).BootstrapSlot(context.Background(), BootstrapSlotRequest{
			Slot: SlotOptions{
				ID:           slotID,
				Storage:      store,
				StateMachine: fsm,
			},
			Voters: voters,
		})
		if err != nil {
			t.Fatalf("BootstrapSlot(node=%d) error = %v", nodeID, err)
		}
	}
}

func waitForFutureResult(t testing.TB, fut Future) Result {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := fut.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	return res
}

func (c *testCluster) waitForLeader(t testing.TB, slotID SlotID) NodeID {
	t.Helper()

	return c.waitForStableLeader(t, slotID, 0)
}

func (c *testCluster) waitForSpecificLeader(t testing.TB, slotID SlotID, leaderID NodeID) {
	t.Helper()

	if got := c.waitForStableLeader(t, slotID, leaderID); got != leaderID {
		t.Fatalf("waitForStableLeader() = %d, want %d", got, leaderID)
	}
}

func (c *testCluster) waitForBootstrapApplied(t testing.TB, slotID SlotID, appliedIndex uint64) {
	t.Helper()

	c.waitForCondition(t, func() bool {
		for _, rt := range c.runtimes {
			st, err := rt.Status(slotID)
			if err != nil {
				return false
			}
			if st.AppliedIndex < appliedIndex || st.CommitIndex < appliedIndex {
				return false
			}
		}
		return true
	})
}

func (c *testCluster) pickFollower(leaderID NodeID) NodeID {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for nodeID := range c.runtimes {
		if nodeID != leaderID {
			return nodeID
		}
	}
	return 0
}

func (c *testCluster) partitionNode(nodeID NodeID) {
	for _, peer := range c.otherNodes(nodeID) {
		c.network.block(nodeID, peer)
		c.network.block(peer, nodeID)
	}
}

func (c *testCluster) healNode(nodeID NodeID) {
	for _, peer := range c.otherNodes(nodeID) {
		c.network.unblock(nodeID, peer)
		c.network.unblock(peer, nodeID)
	}
}

func (c *testCluster) waitForAllApplied(t testing.TB, slotID SlotID, data []byte) {
	t.Helper()

	c.waitForCondition(t, func() bool {
		for nodeID := range c.runtimes {
			fsm := c.fsms[nodeID][slotID]
			if fsm == nil {
				return false
			}
			fsm.mu.Lock()
			if len(fsm.applied) == 0 || string(fsm.applied[len(fsm.applied)-1]) != string(data) {
				fsm.mu.Unlock()
				return false
			}
			fsm.mu.Unlock()
		}
		return true
	})
}

func (c *testCluster) waitForAllAppliedSequence(t testing.TB, slotID SlotID, commands [][]byte) {
	t.Helper()

	c.waitForCondition(t, func() bool {
		for nodeID := range c.runtimes {
			fsm := c.fsms[nodeID][slotID]
			if fsm == nil {
				return false
			}
			fsm.mu.Lock()
			if len(fsm.applied) < len(commands) {
				fsm.mu.Unlock()
				return false
			}
			for i, command := range commands {
				if string(fsm.applied[i]) != string(command) {
					fsm.mu.Unlock()
					return false
				}
			}
			fsm.mu.Unlock()
		}
		return true
	})
}

func (c *testCluster) waitForLeaderAmong(t testing.TB, slotID SlotID, candidates []NodeID) NodeID {
	t.Helper()

	const stableWindow = 100 * time.Millisecond

	var (
		lastLeader NodeID
		stableFrom time.Time
	)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.requireHealthyNetwork(t)

		leaderID, ok := c.currentLeaderAmong(slotID, candidates)
		if ok {
			if leaderID != lastLeader {
				lastLeader = leaderID
				stableFrom = time.Now()
			}
			if time.Since(stableFrom) >= stableWindow {
				return leaderID
			}
		} else {
			lastLeader = 0
			stableFrom = time.Time{}
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.requireHealthyNetwork(t)
	t.Fatal("cluster condition not satisfied before timeout")
	return 0
}

func (c *testCluster) waitForNodeCommitIndex(t testing.TB, nodeID NodeID, slotID SlotID, index uint64) {
	t.Helper()

	c.waitForCondition(t, func() bool {
		st, err := c.runtime(nodeID).Status(slotID)
		return err == nil && st.CommitIndex >= index
	})
}

func (c *testCluster) waitForAllNodesAppliedIndex(t testing.TB, slotID SlotID, index uint64) {
	t.Helper()

	c.waitForCondition(t, func() bool {
		for nodeID := range c.runtimes {
			st, err := c.runtime(nodeID).Status(slotID)
			if err != nil {
				return false
			}
			if st.AppliedIndex < index || st.CommitIndex < index {
				return false
			}
		}
		return true
	})
}

func (c *testCluster) waitForStableLeader(t testing.TB, slotID SlotID, want NodeID) NodeID {
	t.Helper()

	const stableWindow = 100 * time.Millisecond

	var (
		lastLeader NodeID
		stableFrom time.Time
	)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.requireHealthyNetwork(t)

		leaderID, ok := c.currentLeader(slotID)
		if ok && (want == 0 || leaderID == want) {
			if leaderID != lastLeader {
				lastLeader = leaderID
				stableFrom = time.Now()
			}
			if time.Since(stableFrom) >= stableWindow {
				return leaderID
			}
		} else {
			lastLeader = 0
			stableFrom = time.Time{}
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.requireHealthyNetwork(t)
	t.Fatal("cluster condition not satisfied before timeout")
	return 0
}

func (c *testCluster) currentLeader(slotID SlotID) (NodeID, bool) {
	var (
		leaderID    NodeID
		leaderCount int
	)
	for _, rt := range c.runtimes {
		st, err := rt.Status(slotID)
		if err != nil {
			return 0, false
		}
		if st.Role == RoleCandidate {
			return 0, false
		}
		if st.Role == RoleLeader {
			leaderCount++
			if leaderID == 0 {
				leaderID = st.NodeID
			}
			if st.NodeID != leaderID {
				return 0, false
			}
		}
		if st.LeaderID != 0 {
			if leaderID == 0 {
				leaderID = st.LeaderID
			}
			if st.LeaderID != leaderID {
				return 0, false
			}
		}
	}
	return leaderID, leaderCount == 1 && leaderID != 0
}

func (c *testCluster) currentLeaderAmong(slotID SlotID, candidates []NodeID) (NodeID, bool) {
	var (
		leaderID    NodeID
		leaderCount int
	)
	for _, nodeID := range candidates {
		rt := c.runtime(nodeID)
		if rt == nil {
			return 0, false
		}
		st, err := rt.Status(slotID)
		if err != nil {
			return 0, false
		}
		if st.Role == RoleCandidate {
			return 0, false
		}
		if st.Role == RoleLeader {
			leaderCount++
			if leaderID == 0 {
				leaderID = st.NodeID
			}
			if st.NodeID != leaderID {
				return 0, false
			}
		}
	}
	return leaderID, leaderCount == 1 && leaderID != 0
}

func (c *testCluster) waitForCondition(t testing.TB, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.requireHealthyNetwork(t)
		if fn() {
			c.requireHealthyNetwork(t)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.requireHealthyNetwork(t)
	t.Fatal("cluster condition not satisfied before timeout")
}

func (c *testCluster) markAppliedCounts(slotID SlotID) map[NodeID]int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	counts := make(map[NodeID]int, len(c.stores))
	for nodeID, slots := range c.stores {
		store := slots[slotID]
		if store == nil {
			continue
		}
		store.mu.Lock()
		counts[nodeID] = store.markAppliedCount
		store.mu.Unlock()
	}
	return counts
}

func (c *testCluster) requireHealthyNetwork(t testing.TB) {
	t.Helper()

	if err := c.network.firstError(); err != nil {
		t.Fatalf("network delivery error: %v", err)
	}
}

func (n *asyncTestNetwork) firstError() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.err
}

func (n *asyncTestNetwork) recordError(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.err == nil {
		n.err = err
	}
}

func (n *asyncTestNetwork) randomDelay() time.Duration {
	if n.maxDelay <= 0 {
		return 0
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	return time.Duration(n.rng.Int63n(int64(n.maxDelay) + 1))
}

func (n *asyncTestNetwork) block(from, to NodeID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked[networkLink{from: from, to: to}] = struct{}{}
}

func (n *asyncTestNetwork) unblock(from, to NodeID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.blocked, networkLink{from: from, to: to})
}

func (n *asyncTestNetwork) isBlocked(from, to NodeID) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.blocked[networkLink{from: from, to: to}]
	return ok
}

func (n *asyncTestNetwork) close() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.closed = true
	close(n.stopCh)
	n.mu.Unlock()
	n.wg.Wait()
}

func (n *asyncTestNetwork) beginDelivery() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return false
	}
	n.wg.Add(1)
	return true
}

type clusterTransport struct {
	network *asyncTestNetwork
	from    NodeID
}

func (t *clusterTransport) Send(ctx context.Context, batch []Envelope) error {
	for _, env := range batch {
		if !t.network.beginDelivery() {
			return nil
		}
		env := Envelope{
			SlotID:  env.SlotID,
			Message: cloneMessage(env.Message, true),
		}
		go func(env Envelope) {
			defer t.network.wg.Done()

			delay := t.network.randomDelay()
			if delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-t.network.stopCh:
					return
				case <-timer.C:
				}
			} else {
				select {
				case <-t.network.stopCh:
					return
				default:
				}
			}

			target := NodeID(env.Message.To)
			if t.network.isBlocked(t.from, target) {
				return
			}
			rt := t.network.cluster.runtime(target)
			if rt == nil {
				return
			}
			if err := rt.Step(context.Background(), env); err != nil &&
				!errors.Is(err, ErrRuntimeClosed) &&
				!errors.Is(err, ErrSlotClosed) &&
				!errors.Is(err, ErrSlotNotFound) {
				t.network.recordError(fmt.Errorf("deliver from=%d to=%d slot=%d: %w", t.from, target, env.SlotID, err))
			}
		}(env)
	}
	return nil
}
