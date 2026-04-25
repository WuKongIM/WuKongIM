package runtime

import (
	"sync"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// laneDispatchWorkKey identifies a single peer/lane work item.
type laneDispatchWorkKey struct {
	peer core.NodeID
	lane uint16
}

// laneDispatchQueue serializes peer/lane work and tracks dirty requeues.
type laneDispatchQueue struct {
	mu sync.Mutex

	queue      []laneDispatchWorkKey
	head       int
	queued     map[laneDispatchWorkKey]struct{}
	processing map[laneDispatchWorkKey]struct{}
	dirty      map[laneDispatchWorkKey]struct{}
}

type laneRetryState struct {
	timer        *time.Timer
	timerVersion uint64
}

// newLaneDispatchQueue constructs an empty lane dispatch queue.
func newLaneDispatchQueue() *laneDispatchQueue {
	return &laneDispatchQueue{
		queued:     make(map[laneDispatchWorkKey]struct{}),
		processing: make(map[laneDispatchWorkKey]struct{}),
		dirty:      make(map[laneDispatchWorkKey]struct{}),
	}
}

// schedule adds work unless it is already queued or currently running.
func (q *laneDispatchQueue) schedule(key laneDispatchWorkKey) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, running := q.processing[key]; running {
		q.dirty[key] = struct{}{}
		return
	}
	if _, ok := q.queued[key]; ok {
		return
	}
	q.queued[key] = struct{}{}
	q.queue = append(q.queue, key)
}

// pop returns the next queued work item in FIFO order.
func (q *laneDispatchQueue) pop() (laneDispatchWorkKey, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for {
		if q.head >= len(q.queue) {
			return laneDispatchWorkKey{}, false
		}
		key := q.queue[q.head]
		q.queue[q.head] = laneDispatchWorkKey{}
		q.head++
		if _, ok := q.queued[key]; !ok {
			continue
		}
		delete(q.queued, key)
		q.processing[key] = struct{}{}
		if q.head == len(q.queue) {
			q.queue = q.queue[:0]
			q.head = 0
		}
		return key, true
	}
}

// hasWork reports whether there is pending lane dispatch work.
func (q *laneDispatchQueue) hasWork() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.head < len(q.queue)
}

// finish marks work complete and requeues dirty items once.
func (q *laneDispatchQueue) finish(key laneDispatchWorkKey) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, running := q.processing[key]; !running {
		return false
	}
	delete(q.processing, key)
	if _, dirty := q.dirty[key]; !dirty {
		return false
	}
	delete(q.dirty, key)
	if _, ok := q.queued[key]; ok {
		return false
	}
	q.queued[key] = struct{}{}
	q.queue = append(q.queue, key)
	return true
}

// scheduleLaneDispatch queues a peer/lane pair for asynchronous long-poll reissue.
func (r *runtime) scheduleLaneDispatch(peer core.NodeID, laneID uint16) {
	if r.isClosed() {
		return
	}
	r.clearLaneRetry(peer, laneID)
	r.laneDispatcher.schedule(laneDispatchWorkKey{peer: peer, lane: laneID})
	r.startLaneDispatcherWorker()
}

// startLaneDispatcherWorker starts a single background worker for queued lane work.
func (r *runtime) startLaneDispatcherWorker() {
	if r.isClosed() {
		return
	}
	if !r.laneDispatcherWorker.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for {
			if r.isClosed() {
				r.laneDispatcherWorker.Store(false)
				return
			}
			r.runLaneDispatcher()
			r.laneDispatcherWorker.Store(false)
			if r.isClosed() || !r.laneDispatcher.hasWork() || !r.laneDispatcherWorker.CompareAndSwap(false, true) {
				return
			}
		}
	}()
}

// runLaneDispatcher launches queued lane work until the queue is empty.
func (r *runtime) runLaneDispatcher() {
	if r.isClosed() {
		return
	}
	for {
		key, ok := r.laneDispatcher.pop()
		if !ok {
			return
		}
		r.dispatchLanePollAsync(key)
		if r.isClosed() {
			return
		}
	}
}

func (r *runtime) dispatchLanePollAsync(key laneDispatchWorkKey) {
	go func() {
		r.dispatchLanePoll(key)
		if r.laneDispatcher.finish(key) {
			r.startLaneDispatcherWorker()
		}
	}()
}

// dispatchLanePoll sends one lane-poll request for a scheduled peer/lane pair.
func (r *runtime) dispatchLanePoll(key laneDispatchWorkKey) {
	manager, ok := r.laneManager(key.peer)
	if !ok {
		return
	}
	req, ok := manager.NextRequest(key.lane)
	if !ok {
		return
	}
	retryKey, _ := manager.AnyChannel(key.lane)
	err := r.sendEnvelope(Envelope{
		Peer:            key.peer,
		ChannelKey:      retryKey,
		RequestID:       r.requestID.Add(1),
		Kind:            MessageKindLanePollRequest,
		LanePollRequest: &req,
	})
	if err == nil {
		return
	}
	manager.SendFailed(key.lane)
	r.scheduleLaneRetry(key.peer, key.lane)
}

func (r *runtime) scheduleLaneRetry(peer core.NodeID, laneID uint16) {
	if r.isClosed() {
		return
	}

	key := PeerLaneKey{Peer: peer, LaneID: laneID}
	r.laneRetryMu.Lock()
	defer r.laneRetryMu.Unlock()

	state := r.laneRetryStateLocked(key)
	if state.timer != nil {
		return
	}
	state.timerVersion++
	version := state.timerVersion
	state.timer = time.AfterFunc(r.cfg.FollowerReplicationRetryInterval, func() {
		r.fireLaneRetry(key, version)
	})
}

func (r *runtime) fireLaneRetry(key PeerLaneKey, version uint64) {
	if r.isClosed() {
		return
	}

	r.laneRetryMu.Lock()
	state, ok := r.laneRetry[key]
	if !ok || state.timerVersion != version {
		r.laneRetryMu.Unlock()
		return
	}
	state.timer = nil
	delete(r.laneRetry, key)
	r.laneRetryMu.Unlock()

	r.scheduleLaneDispatch(key.Peer, key.LaneID)
}

func (r *runtime) laneRetryStateLocked(key PeerLaneKey) *laneRetryState {
	if state, ok := r.laneRetry[key]; ok {
		return state
	}
	state := &laneRetryState{}
	r.laneRetry[key] = state
	return state
}

func (r *runtime) clearLaneRetry(peer core.NodeID, laneID uint16) {
	r.laneRetryMu.Lock()
	defer r.laneRetryMu.Unlock()

	key := PeerLaneKey{Peer: peer, LaneID: laneID}
	state, ok := r.laneRetry[key]
	if !ok {
		return
	}
	state.timerVersion++
	if state.timer != nil {
		state.timer.Stop()
	}
	delete(r.laneRetry, key)
}

func (r *runtime) clearAllLaneRetries() []*time.Timer {
	r.laneRetryMu.Lock()
	defer r.laneRetryMu.Unlock()

	timers := make([]*time.Timer, 0, len(r.laneRetry))
	for key, state := range r.laneRetry {
		if state.timer != nil {
			timers = append(timers, state.timer)
		}
		delete(r.laneRetry, key)
	}
	return timers
}

// reset clears all queue state.
func (q *laneDispatchQueue) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.queue = nil
	q.head = 0
	q.queued = make(map[laneDispatchWorkKey]struct{})
	q.processing = make(map[laneDispatchWorkKey]struct{})
	q.dirty = make(map[laneDispatchWorkKey]struct{})
}
