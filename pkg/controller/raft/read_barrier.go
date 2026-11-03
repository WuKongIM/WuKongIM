package raft

import (
	"context"
	"encoding/binary"
	"errors"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/raft/raftstore"
	etcdraft "go.etcd.io/raft/v3"
)

const maxReadBarriers = 64

// ErrReadBarrierBusy bounds all outstanding Raft read tokens, including canceled
// requests whose token is still retained by the Raft library awaiting quorum.
var ErrReadBarrierBusy = errors.New("controller/raft: read barrier capacity exhausted")

// ErrReadBarrierForwardingUnsupported rejects unsupported peer-originated read
// requests/responses; only the bounded leader-local API creates read tokens.
var ErrReadBarrierForwardingUnsupported = errors.New("controller/raft: forwarded read barrier unsupported")

type readBarrierResponse struct {
	index uint64
	err   error
}

// readBarrierRequest retains no caller context or context values. Cancellation is
// signaled by the waiting caller; no per-read goroutine or AfterFunc is needed.
type readBarrierRequest struct {
	token    [24]byte
	deadline time.Time
	canceled atomic.Uint32 // 1 = canceled, 2 = original deadline expired
	response chan readBarrierResponse
	permit   chan struct{}
}

func (c *readBarrierRequest) cancellation() error {
	switch c.canceled.Load() {
	case 1:
		return context.Canceled
	case 2:
		return context.DeadlineExceeded
	}
	if !c.deadline.IsZero() && !time.Now().Before(c.deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

// ReadBarrier waits for current-leader quorum confirmation and actual durable
// application of its read index. It appends no log entry. ProbePropose remains
// the separate API for testing the proposal write path.
func (s *Service) ReadBarrier(ctx context.Context) (uint64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	if !s.started || s.stopping {
		err := ErrNotStarted
		if s.stopped || s.stopping {
			err = ErrStopped
		}
		if s.err != nil {
			err = s.err
		}
		s.mu.Unlock()
		return 0, err
	}
	requests, permits, wake := s.readRequests, s.readPermits, s.readWake
	startID, stop, done := s.readStartID, s.stopCh, s.doneCh
	s.mu.Unlock()
	select {
	case permits <- struct{}{}:
	default:
		return 0, ErrReadBarrierBusy
	}
	sequence, ok := s.nextReadSequence()
	if !ok {
		<-permits
		return 0, ErrReadBarrierBusy
	}
	call := &readBarrierRequest{response: make(chan readBarrierResponse, 1), permit: permits}
	copy(call.token[:16], startID[:])
	binary.BigEndian.PutUint64(call.token[16:], sequence)
	call.deadline, _ = ctx.Deadline()
	handedOff := false
	defer func() {
		if !handedOff {
			<-permits
		}
	}()
	select {
	case requests <- call:
		handedOff = true
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-stop:
		return 0, ErrStopped
	case <-done:
		return 0, s.currentError()
	}
	select {
	case result := <-call.response:
		return result.index, result.err
	case <-ctx.Done():
		reason := uint32(1)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = 2
		}
		call.canceled.Store(reason)
		select {
		case wake <- struct{}{}:
		default:
		}
		return 0, ctx.Err()
	case <-stop:
		return 0, ErrStopped
	case <-done:
		return 0, s.currentError()
	}
}

// pendingReadBarrier is owned only by the RawNode loop. Once a caller cancels,
// an issued token retains only its fixed identity and permit until Raft retires
// it. This prevents rapid cancellations from bypassing the 64-token bound.
type pendingReadBarrier struct {
	token  string
	term   uint64
	index  uint64
	issued bool
	call   *readBarrierRequest
	permit chan struct{}
}

type readBarrierTracker struct {
	pending map[string]*pendingReadBarrier
}

func newReadBarrierTracker() *readBarrierTracker {
	return &readBarrierTracker{pending: make(map[string]*pendingReadBarrier, maxReadBarriers)}
}
func (r *readBarrierTracker) add(call *readBarrierRequest, term uint64) {
	token := string(call.token[:])
	r.pending[token] = &pendingReadBarrier{token: token, term: term, call: call, permit: call.permit}
}
func (r *readBarrierTracker) finish(p *pendingReadBarrier, index uint64, err error) {
	if p.call != nil {
		p.call.response <- readBarrierResponse{index: index, err: err}
		p.call = nil
	}
	delete(r.pending, p.token)
	<-p.permit
}
func (r *readBarrierTracker) fail(err error) {
	for _, p := range r.pending {
		r.finish(p, 0, err)
	}
}
func (r *readBarrierTracker) accept(states []etcdraft.ReadState) {
	for _, state := range states {
		if p := r.pending[string(state.RequestCtx)]; p != nil && p.issued {
			p.index = state.Index
		}
	}
}

// advance runs after Ready persistence and before select. RawNode.Advance may
// lead the asynchronous FSM; only the successful scheduler watermark can finish
// these reads. The current-term guard also keeps canceled calls out of etcd's
// pendingReadIndexMessages, which is not cleared by a Raft term reset.
func (r *readBarrierTracker) advance(raw *etcdraft.RawNode, store *raftstore.Store, applied uint64) bool {
	if len(r.pending) == 0 {
		return false
	}
	status := raw.BasicStatus()
	var commitTerm uint64
	if status.RaftState == etcdraft.StateLeader && status.Commit != 0 {
		commitTerm, _ = store.Term(status.Commit)
	}
	issued := false
	for _, p := range r.pending {
		if status.RaftState != etcdraft.StateLeader || p.term != status.Term {
			r.finish(p, 0, ErrNotLeader)
			continue
		}
		if p.call != nil {
			if err := p.call.cancellation(); err != nil {
				if !p.issued || p.index != 0 {
					r.finish(p, 0, err)
					continue
				}
				p.call.response <- readBarrierResponse{err: err}
				p.call = nil
			}
		}
		if p.index != 0 {
			if p.call == nil {
				r.finish(p, 0, nil)
			} else if applied >= p.index {
				r.finish(p, p.index, nil)
			}
			continue
		}
		if !p.issued && commitTerm == status.Term {
			p.issued = true
			raw.ReadIndex([]byte(p.token))
			issued = true
		}
	}
	return issued
}

// Sequence exhaustion fails admission permanently instead of reusing a token.
func (s *Service) nextReadSequence() (uint64, bool) {
	for {
		old := s.readSequence.Load()
		if old == ^uint64(0) {
			return 0, false
		}
		if s.readSequence.CompareAndSwap(old, old+1) {
			return old + 1, true
		}
	}
}
