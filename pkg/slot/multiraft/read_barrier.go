package multiraft

import (
	"context"
	"encoding/binary"
	"sync/atomic"

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
}

func (r *readBarrierRequest) finish(err error) {
	if r.done.CompareAndSwap(false, true) {
		r.resp <- err
	}
}

// ReadBarrier obtains a fresh quorum read index from the local leader and waits
// for durable application. It neither forwards nor appends a log entry.
func (r *Runtime) ReadBarrier(ctx context.Context, slotID SlotID) error {
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
	if request == nil || request.done.Load() {
		return
	}
	if err := request.ctx.Err(); err != nil {
		request.finish(err)
		return
	}
	if err := g.currentErr(); err != nil {
		request.finish(err)
		return
	}
	st := g.rawNode.BasicStatus()
	if st.RaftState != raft.StateLeader {
		request.finish(ErrNotLeader)
		return
	}
	// etcd queues pre-current-term reads separately and does not clear that
	// queue on every term reset. Admit only after a durable current-term commit,
	// so the bounded requests live exclusively in Raft's resettable readOnly queue.
	committedTerm, termErr := g.storageView.memory.Term(st.Commit)
	if termErr != nil || committedTerm != st.Term {
		request.finish(ErrSlotBusy)
		return
	}
	g.mu.Lock()
	if err := g.admissionErrLocked(); err != nil {
		g.mu.Unlock()
		request.finish(err)
		return
	}
	if len(g.pendingReads) >= maxPendingReadBarriers || g.readSequence == ^uint64(0) {
		g.mu.Unlock()
		request.finish(ErrSlotBusy)
		return
	}
	g.readSequence++
	var key [16]byte
	binary.BigEndian.PutUint64(key[:8], st.Term)
	binary.BigEndian.PutUint64(key[8:], g.readSequence)
	request.term = st.Term
	if g.pendingReads == nil {
		g.pendingReads = make(map[string]*readBarrierRequest)
	}
	g.pendingReads[string(key[:])] = request
	g.mu.Unlock()
	g.rawNode.ReadIndex(key[:])
}
func (g *slot) acceptReadStates(states []raft.ReadState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, state := range states {
		if request := g.pendingReads[string(state.RequestCtx)]; request != nil {
			request.index = state.Index
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
