package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel/replica"
)

type taskMask uint8

const (
	taskReplication taskMask = 1 << iota
	taskSnapshot
)

type ChannelDelegate interface {
	OnReplication(key core.ChannelKey)
	OnSnapshot(key core.ChannelKey)
}

type channel struct {
	key      core.ChannelKey
	gen      uint64
	replica  replica.Replica
	now      func() time.Time
	delegate ChannelDelegate
	onAppend func(core.ChannelKey)
	changes  *replicaChangeNotifier
	meta     atomic.Pointer[core.Meta]
	// applyMu serializes metadata application so older fence generations cannot publish after newer ones.
	applyMu sync.Mutex
	mu      sync.Mutex
	pending taskMask

	lastActiveAt time.Time
	inUse        int
	evicting     bool

	replicationPeers   nodeIDQueue
	replicationTargets []PeerLaneKey
	snapshotBytes      int64
}

type ownedAppendReplica interface {
	AppendOwned(ctx context.Context, batch []core.Record) (core.CommitResult, error)
}

func newChannel(
	key core.ChannelKey,
	generation uint64,
	rep replica.Replica,
	meta core.Meta,
	now func() time.Time,
	delegate ChannelDelegate,
	onAppend func(core.ChannelKey),
	changes *replicaChangeNotifier,
) *channel {
	if now == nil {
		now = time.Now
	}
	if changes == nil {
		changes = newReplicaChangeNotifier()
	}
	c := &channel{
		key:      key,
		gen:      generation,
		replica:  rep,
		now:      now,
		delegate: delegate,
		onAppend: onAppend,
		changes:  changes,
	}
	c.lastActiveAt = c.now()
	c.setMeta(meta)
	return c
}

func (c *channel) ID() core.ChannelKey {
	return c.key
}

func (c *channel) Meta() core.Meta {
	return c.metaSnapshot()
}

func (c *channel) Status() core.ReplicaState {
	return c.replica.Status()
}

func (c *channel) waitReplicaChange(ctx context.Context, version uint64) bool {
	if c == nil || c.changes == nil {
		return false
	}
	return c.changes.wait(ctx, version)
}

func (c *channel) Append(ctx context.Context, records []core.Record) (core.CommitResult, error) {
	if !c.beginUse() {
		return core.CommitResult{}, ErrChannelNotFound
	}
	defer c.endUse()

	meta := c.metaSnapshot()
	state := c.replica.Status()
	if state.Role == core.ReplicaRoleTombstoned {
		return core.CommitResult{}, core.ErrTombstoned
	}
	if state.Role == core.ReplicaRoleFencedLeader {
		return core.CommitResult{}, core.ErrLeaseExpired
	}
	if state.Role == core.ReplicaRoleLeader && !state.CommitReady {
		return core.CommitResult{}, core.ErrNotReady
	}
	if meta.WriteFence.BlocksAppend() {
		return core.CommitResult{}, core.ErrWriteFenced
	}
	if !meta.LeaseUntil.IsZero() && !c.now().Before(meta.LeaseUntil) {
		return core.CommitResult{}, core.ErrLeaseExpired
	}
	var (
		result core.CommitResult
		err    error
	)
	if owned, ok := c.replica.(ownedAppendReplica); ok {
		result, err = owned.AppendOwned(ctx, records)
	} else {
		result, err = c.replica.Append(ctx, records)
	}
	if err != nil {
		return core.CommitResult{}, err
	}
	if c.onAppend != nil {
		c.onAppend(c.key)
	}
	return result, nil
}

func (c *channel) beginUse() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.evicting {
		return false
	}
	c.inUse++
	c.lastActiveAt = c.now()
	return true
}

func (c *channel) endUse() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inUse > 0 {
		c.inUse--
	}
}

func (c *channel) touch() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.evicting {
		return false
	}
	c.lastActiveAt = c.now()
	return true
}

func (c *channel) tryMarkIdleEvicting(cutoff time.Time) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.evicting || c.inUse > 0 {
		return false
	}
	if c.lastActiveAt.After(cutoff) {
		return false
	}
	if c.pending != 0 || c.snapshotBytes > 0 || c.replicationPeers.pending() > 0 {
		return false
	}
	c.evicting = true
	return true
}

func (c *channel) cancelIdleEviction() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evicting = false
}

func (c *channel) setMeta(meta core.Meta) {
	next := meta
	c.meta.Store(&next)
}

func (c *channel) metaSnapshot() core.Meta {
	ptr := c.meta.Load()
	if ptr == nil {
		return core.Meta{}
	}
	return *ptr
}

func (c *channel) replicaChangeVersion() uint64 {
	if c == nil || c.changes == nil {
		return 0
	}
	return c.changes.snapshot()
}

func (c *channel) markReplication() {
	c.markTask(taskReplication)
}

func (c *channel) markSnapshot() {
	c.markTask(taskSnapshot)
}

func (c *channel) markTask(mask taskMask) {
	c.mu.Lock()
	c.pending |= mask
	c.mu.Unlock()
}

func (c *channel) runPendingTasks() {
	if c.delegate == nil {
		return
	}
	c.runTask(taskReplication, c.delegate.OnReplication)
	c.runTask(taskSnapshot, c.delegate.OnSnapshot)
}

func (c *channel) runTask(mask taskMask, fn func(core.ChannelKey)) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	if c.pending&mask == 0 {
		c.mu.Unlock()
		return
	}
	c.pending &^= mask
	c.mu.Unlock()
	fn(c.key)
}

func (c *channel) enqueueReplication(peer core.NodeID) {
	c.mu.Lock()
	c.replicationPeers.enqueue(peer)
	c.mu.Unlock()
}

func (c *channel) popReplicationPeer() (core.NodeID, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.replicationPeers.pop()
}

func (c *channel) enqueueSnapshot(bytes int64) {
	c.mu.Lock()
	c.snapshotBytes += bytes
	c.mu.Unlock()
}

func (c *channel) setReplicationTargets(targets []PeerLaneKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.replicationTargets = append(c.replicationTargets[:0], targets...)
}

func (c *channel) replicationTargetsSnapshot() []PeerLaneKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]PeerLaneKey(nil), c.replicationTargets...)
}

func (c *channel) drainSnapshotBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	bytes := c.snapshotBytes
	c.snapshotBytes = 0
	return bytes
}

func (c *channel) clearInvalidReplicationPeers(allow func(core.NodeID) bool) {
	c.mu.Lock()
	c.replicationPeers.filter(allow)
	c.mu.Unlock()
}

func (c *channel) clearSnapshotWork() {
	c.mu.Lock()
	c.snapshotBytes = 0
	c.pending &^= taskSnapshot
	c.mu.Unlock()
}

type nodeIDQueue struct {
	items []core.NodeID
	head  int
	set   map[core.NodeID]struct{}
	dirty map[core.NodeID]struct{}
}

type replicaChangeNotifier struct {
	mu      sync.Mutex
	version uint64
	ready   chan struct{}
	// waiters tracks goroutines currently blocked on ready.
	waiters int
}

func newReplicaChangeNotifier() *replicaChangeNotifier {
	return &replicaChangeNotifier{ready: make(chan struct{})}
}

func (q *nodeIDQueue) enqueue(nodeID core.NodeID) {
	if q.set == nil {
		q.set = make(map[core.NodeID]struct{})
	}
	if _, ok := q.set[nodeID]; ok {
		if q.dirty == nil {
			q.dirty = make(map[core.NodeID]struct{})
		}
		q.dirty[nodeID] = struct{}{}
		return
	}
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	} else if q.head > 0 && len(q.items) == cap(q.items) {
		q.compact()
	}
	q.items = append(q.items, nodeID)
	q.set[nodeID] = struct{}{}
}

func (q *nodeIDQueue) pop() (core.NodeID, bool) {
	if q.head >= len(q.items) {
		return 0, false
	}

	nodeID := q.items[q.head]
	q.items[q.head] = 0
	q.head++
	if _, ok := q.dirty[nodeID]; ok {
		delete(q.dirty, nodeID)
		if q.head == len(q.items) {
			q.items = q.items[:0]
			q.head = 0
		} else if q.head > 0 && len(q.items) == cap(q.items) {
			q.compact()
		}
		q.items = append(q.items, nodeID)
		return nodeID, true
	}
	delete(q.set, nodeID)
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	}
	return nodeID, true
}

func (q *nodeIDQueue) pending() int {
	if q == nil {
		return 0
	}
	return len(q.items) - q.head
}

func (q *nodeIDQueue) compact() {
	n := copy(q.items, q.items[q.head:])
	for i := n; i < len(q.items); i++ {
		q.items[i] = 0
	}
	q.items = q.items[:n]
	q.head = 0
}

func (q *nodeIDQueue) filter(allow func(core.NodeID) bool) {
	if allow == nil {
		return
	}
	filtered := nodeIDQueue{}
	for i := q.head; i < len(q.items); i++ {
		nodeID := q.items[i]
		if !allow(nodeID) {
			continue
		}
		filtered.enqueue(nodeID)
	}
	*q = filtered
}

func (n *replicaChangeNotifier) notify() {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.version++
	if n.waiters == 0 {
		return
	}
	close(n.ready)
	n.ready = make(chan struct{})
}

func (n *replicaChangeNotifier) snapshot() uint64 {
	if n == nil {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.version
}

func (n *replicaChangeNotifier) wait(ctx context.Context, version uint64) bool {
	if n == nil {
		return false
	}
	n.mu.Lock()
	if n.version != version {
		n.mu.Unlock()
		return true
	}
	ready := n.ready
	n.waiters++
	n.mu.Unlock()

	signaled := false
	select {
	case <-ready:
		signaled = true
	case <-ctx.Done():
	}
	n.mu.Lock()
	n.waiters--
	n.mu.Unlock()
	return signaled
}
