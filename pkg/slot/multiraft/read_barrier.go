package multiraft

import (
	"context"
	"encoding/binary"
	"errors"
	"sync/atomic"
	"time"

	raft "go.etcd.io/raft/v3"
)

const maxPendingReadBarriers = 256

// readBarrierRequest completes only after ReadIndex quorum confirmation and
// durable local apply. Canceled unconfirmed requests remain counted until Raft
// answers or leadership changes, bounding Raft's own uncancelable read queue.
type readBarrierRequest struct {
	ctx   context.Context
	resp  chan error
	done  atomic.Bool
	term  uint64
	index uint64
	// batchKeys names earlier callers admitted in the same control batch before
	// this fresh ReadIndex was issued. mu guards confirmation and release.
	batchKeys []string
}

func (r *readBarrierRequest) finish(err error) {
	if r.done.CompareAndSwap(false, true) {
		r.resp <- err
	}
}

// ReadBarrierObserver optionally receives the caller-visible wait of each
// read barrier with a fixed result label; implementations must not block.
type ReadBarrierObserver interface {
	ObserveSlotReadBarrier(result string, d time.Duration)
}

// observeReadBarrier maps err onto a fixed label set so series stay bounded.
func observeReadBarrier(observer SchedulerObserver, err error, d time.Duration) {
	o, ok := observer.(ReadBarrierObserver)
	if !ok || o == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	result := "error"
	switch {
	case err == nil:
		result = "ok"
	case errors.Is(err, ErrNotLeader):
		result = "not_leader"
	case errors.Is(err, ErrSlotBusy):
		result = "busy"
	case errors.Is(err, context.Canceled):
		result = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		result = "deadline"
	}
	o.ObserveSlotReadBarrier(result, d)
}

// ReadBarrier obtains a fresh quorum read index from the local leader and waits
// for durable application. It neither forwards nor appends a log entry.
func (r *Runtime) ReadBarrier(ctx context.Context, slotID SlotID) (err error) {
	started := time.Now()
	defer func() { observeReadBarrier(r.opts.Observer, err, time.Since(started)) }()
	return r.readBarrier(ctx, slotID)
}

func (r *Runtime) readBarrier(ctx context.Context, slotID SlotID) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return ErrRuntimeClosed
	}
	g, ok := r.slots[slotID]
	r.mu.RUnlock()
	if !ok {
		return ErrSlotNotFound
	}
	request := &readBarrierRequest{ctx: ctx, resp: make(chan error, 1)}
	if err := g.enqueueControl(controlAction{kind: controlReadBarrier, readBarrier: request}); err != nil {
		return err
	}
	r.scheduler.enqueue(slotID)
	select {
	case err := <-request.resp:
		return err
	case <-ctx.Done():
		request.finish(ctx.Err())
		return <-request.resp
	}
}

// issueReadBarrier runs exclusively on the owning Raft worker.
func (g *slot) issueReadBarrier(request *readBarrierRequest) {
	if key := g.admitReadBarrier(request); key != "" {
		g.rawNode.ReadIndex([]byte(key))
	}
}

// issueReadBarrierBatch shares one new quorum round only among contiguous
// controls already taken by this worker. Later arrivals require a new ReadIndex;
// every caller still owns its cancellation and one of the 256 pending positions.
func (g *slot) issueReadBarrierBatch(controls []controlAction) {
	if len(controls) == 1 {
		g.issueReadBarrier(controls[0].readBarrier)
		return
	}
	keys := make([]string, 0, min(len(controls), maxPendingReadBarriers))
	var last *readBarrierRequest
	for _, action := range controls {
		if key := g.admitReadBarrier(action.readBarrier); key != "" {
			keys = append(keys, key)
			last = action.readBarrier
		}
	}
	if len(keys) == 0 {
		return
	}
	g.mu.Lock()
	last.batchKeys = keys[:len(keys)-1]
	g.mu.Unlock()
	g.rawNode.ReadIndex([]byte(keys[len(keys)-1]))
}

// admitReadBarrier retains one caller under the existing leadership, durable
// current-term and pending-count fences before the worker issues a quorum round.
func (g *slot) admitReadBarrier(request *readBarrierRequest) string {
	if request == nil || request.done.Load() {
		return ""
	}
	if err := request.ctx.Err(); err != nil {
		request.finish(err)
		return ""
	}
	if err := g.currentErr(); err != nil {
		request.finish(err)
		return ""
	}
	st := g.rawNode.BasicStatus()
	if st.RaftState != raft.StateLeader {
		request.finish(ErrNotLeader)
		return ""
	}
	// etcd queues pre-current-term reads separately and does not clear that
	// queue on every term reset. Admit only after a durable current-term commit,
	// so the bounded requests live exclusively in Raft's resettable readOnly queue.
	committedTerm, termErr := g.storageView.memory.Term(st.Commit)
	if termErr != nil || committedTerm != st.Term {
		request.finish(ErrSlotBusy)
		return ""
	}
	g.mu.Lock()
	if err := g.admissionErrLocked(); err != nil {
		g.mu.Unlock()
		request.finish(err)
		return ""
	}
	if len(g.pendingReads) >= maxPendingReadBarriers || g.readSequence == ^uint64(0) {
		g.mu.Unlock()
		request.finish(ErrSlotBusy)
		return ""
	}
	g.readSequence++
	var key [16]byte
	binary.BigEndian.PutUint64(key[:8], st.Term)
	binary.BigEndian.PutUint64(key[8:], g.readSequence)
	request.term = st.Term
	if g.pendingReads == nil {
		g.pendingReads = make(map[string]*readBarrierRequest)
	}
	encoded := string(key[:])
	g.pendingReads[encoded] = request
	g.mu.Unlock()
	return encoded
}
func (g *slot) acceptReadStates(states []raft.ReadState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, state := range states {
		if request := g.pendingReads[string(state.RequestCtx)]; request != nil {
			request.index = state.Index
			for _, key := range request.batchKeys {
				if sibling := g.pendingReads[key]; sibling != nil {
					sibling.index = state.Index
				}
			}
			request.batchKeys = nil
		}
	}
	g.completeReadBarriersLocked()
}
func (g *slot) completeReadBarriersLocked() {
	for key, request := range g.pendingReads {
		if g.status.Role != RoleLeader || request.term != g.status.Term {
			request.finish(ErrNotLeader)
			delete(g.pendingReads, key)
			continue
		}
		if err := request.ctx.Err(); err != nil {
			request.finish(err)
		}
		if request.index > 0 && (request.done.Load() || g.durableAppliedIndex >= request.index) {
			request.finish(nil)
			delete(g.pendingReads, key)
		}
	}
}
func (g *slot) failReadBarriersLocked(err error) {
	for key, request := range g.pendingReads {
		request.finish(err)
		delete(g.pendingReads, key)
	}
}

// failUnconfirmedReadCallersLocked releases callers after a transient Ready
// failure while retaining the count of requests still owned by live RawNode.
func (g *slot) failUnconfirmedReadCallersLocked(err error) {
	for key, request := range g.pendingReads {
		request.finish(err)
		if request.index > 0 {
			delete(g.pendingReads, key)
		}
	}
}
