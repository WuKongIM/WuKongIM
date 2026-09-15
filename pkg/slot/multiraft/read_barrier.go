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
	// next links callers sharing one fresh proof; guarded by the owning slot mu.
	next *readBarrierRequest
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

// issueReadBarrier retains the single-request seam for worker-level callers.
func (g *slot) issueReadBarrier(request *readBarrierRequest) {
	g.issueReadBarrierBatch([]controlAction{{kind: controlReadBarrier, readBarrier: request}})
}

// issueReadBarrierBatch shares a fresh proof only among consecutive read controls
// already detached by this worker. New arrivals cannot join an issued proof.
// The admission budget counts callers, including canceled unconfirmed callers.
func (g *slot) issueReadBarrierBatch(actions []controlAction) {
	batchErr := g.currentErr()
	var st raft.BasicStatus
	if batchErr == nil {
		st = g.rawNode.BasicStatus()
		if st.RaftState != raft.StateLeader {
			batchErr = ErrNotLeader
		} else {
			// Keep reads out of etcd's pre-current-term queue, which does not clear on
			// every term reset. Only the resettable readOnly queue may own our requests.
			committedTerm, err := g.storageView.memory.Term(st.Commit)
			if err != nil || committedTerm != st.Term {
				batchErr = ErrSlotBusy
			}
		}
	}
	g.mu.Lock()
	if batchErr == nil {
		batchErr = g.admissionErrLocked()
	}
	var head, tail *readBarrierRequest
	for _, action := range actions {
		request := action.readBarrier
		if request == nil || request.done.Load() {
			continue
		}
		if err := request.ctx.Err(); err != nil {
			request.finish(err)
			continue
		}
		if batchErr != nil {
			request.finish(batchErr)
			continue
		}
		if g.pendingReadCount >= maxPendingReadBarriers || g.readSequence == ^uint64(0) {
			request.finish(ErrSlotBusy)
			continue
		}
		request.term = st.Term
		if head == nil {
			head = request
		} else {
			tail.next = request
		}
		tail = request
		g.pendingReadCount++
	}
	if head == nil {
		g.mu.Unlock()
		return
	}
	g.readSequence++
	var key [16]byte
	binary.BigEndian.PutUint64(key[:8], st.Term)
	binary.BigEndian.PutUint64(key[8:], g.readSequence)
	if g.pendingReads == nil {
		g.pendingReads = make(map[string]*readBarrierRequest)
	}
	g.pendingReads[string(key[:])] = head
	g.mu.Unlock()
	g.rawNode.ReadIndex(key[:])
}
func (g *slot) acceptReadStates(states []raft.ReadState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, state := range states {
		for request := g.pendingReads[string(state.RequestCtx)]; request != nil; request = request.next {
			request.index = state.Index
		}
	}
	g.completeReadBarriersLocked()
}
func (g *slot) completeReadBarriersLocked() {
	for key, head := range g.pendingReads {
		link := &head
		for *link != nil {
			request := *link
			remove := false
			if g.status.Role != RoleLeader || request.term != g.status.Term {
				request.finish(ErrNotLeader)
				remove = true
			} else {
				if err := request.ctx.Err(); err != nil {
					request.finish(err)
				}
				if request.index > 0 && (request.done.Load() || g.durableAppliedIndex >= request.index) {
					request.finish(nil)
					remove = true
				}
			}
			if remove {
				*link = request.next
				request.next = nil
				g.pendingReadCount--
			} else {
				link = &request.next
			}
		}
		if head == nil {
			delete(g.pendingReads, key)
		} else {
			g.pendingReads[key] = head
		}
	}
}
func (g *slot) failReadBarriersLocked(err error) {
	for key, head := range g.pendingReads {
		for request := head; request != nil; {
			next := request.next
			request.finish(err)
			request.next = nil
			request = next
		}
		delete(g.pendingReads, key)
	}
	g.pendingReadCount = 0
}

// failUnconfirmedReadCallersLocked releases callers after a transient Ready
// failure while retaining every caller still owned by live RawNode.
func (g *slot) failUnconfirmedReadCallersLocked(err error) {
	for key, head := range g.pendingReads {
		confirmed := head.index > 0
		for request := head; request != nil; {
			next := request.next
			request.finish(err)
			if confirmed {
				request.next = nil
				g.pendingReadCount--
			}
			request = next
		}
		if confirmed {
			delete(g.pendingReads, key)
		}
	}
}
