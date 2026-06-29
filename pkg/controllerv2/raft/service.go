package raft

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"go.etcd.io/raft/v3/raftpb"
)

var (
	// ErrInvalidConfig indicates that the service configuration is incomplete or inconsistent.
	ErrInvalidConfig = errors.New("controllerv2/raft: invalid config")
	// ErrNotStarted indicates that the service has not been started.
	ErrNotStarted = errors.New("controllerv2/raft: not started")
	// ErrStopped indicates that the service has stopped.
	ErrStopped = errors.New("controllerv2/raft: stopped")
	// ErrNotLeader indicates that proposals must be sent to the current leader.
	ErrNotLeader = errors.New("controllerv2/raft: not leader")
	// ErrProposalRejected indicates that a committed proposal was semantically rejected by the state machine.
	ErrProposalRejected = errors.New("controllerv2/raft: proposal rejected")
)

// ProposalRejectedError describes a committed proposal that did not satisfy state-machine semantics.
type ProposalRejectedError struct {
	// Index is the committed Raft log index of the rejected proposal.
	Index uint64
	// Reason is the deterministic state-machine rejection reason.
	Reason string
}

func (e ProposalRejectedError) Error() string {
	return fmt.Sprintf("%s at index %d: %s", ErrProposalRejected.Error(), e.Index, e.Reason)
}

func (e ProposalRejectedError) Unwrap() error { return ErrProposalRejected }

// ProposalResult describes the FSM outcome for one committed ControllerV2 proposal.
type ProposalResult struct {
	// Changed is true when the proposal advanced the logical cluster-state revision.
	Changed bool
	// Updated is true when the command changed durable state without advancing the logical cluster-state revision.
	Updated bool
	// Noop is true when the proposal was committed but idempotent or obsolete.
	Noop bool
	// Rejected is true when the FSM rejected the committed proposal.
	Rejected bool
	// Reason describes a no-op or reject outcome.
	Reason string
	// Revision is the resulting logical cluster-state revision.
	Revision uint64
	// AppliedRaftIndex is the resulting durable Raft applied index.
	AppliedRaftIndex uint64
}

// Service owns one ControllerV2 RawNode and applies committed commands to the state machine.
type Service struct {
	cfg Config

	mu       sync.Mutex
	started  bool
	stopping bool
	stopped  bool
	stopCh   chan struct{}
	doneCh   chan struct{}
	stepCh   chan raftpb.Message
	proposal chan proposalRequest
	compact  chan compactRequest
	err      error
	store    *raftstore.Store

	statusMu sync.RWMutex
	status   Status
	leaderID atomic.Uint64

	snapshotMu   sync.Mutex
	lastSnapshot time.Time
}

type proposalRequest struct {
	ctx   context.Context
	cmd   command.Command
	probe bool
	resp  chan proposalResponse
}

type compactRequest struct {
	ctx  context.Context
	resp chan compactResponse
}

type compactResponse struct {
	result LogCompactionResult
	err    error
}

type trackedProposal struct {
	resp  chan proposalResponse
	probe bool
}

type proposalResponse struct {
	result ProposalResult
	err    error
}

// NewService validates cfg and creates a ControllerV2 Raft service. Call Start before use.
func NewService(cfg Config) (*Service, error) {
	cfg = cfg.normalized()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, status: initialStatus(cfg)}, nil
}

// Start loads durable state, recovers cluster-state.json if required, and starts the Raft run loop.
func (s *Service) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := s.cfg

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return ErrStopped
	}
	if s.started {
		return nil
	}
	s.stopped = false
	s.err = nil
	s.statusMu.Lock()
	s.status = initialStatus(cfg)
	s.statusMu.Unlock()

	store, err := raftstore.Open(ctx, raftstore.Config{Dir: cfg.RaftDir, NodeID: cfg.NodeID, SegmentSize: cfg.WALSegmentSize})
	if err != nil {
		s.recordDegraded(err)
		return err
	}
	s.store = store
	startup, err := s.recoverStartup(ctx, store)
	if err != nil {
		_ = store.Close()
		s.store = nil
		s.recordDegraded(err)
		return err
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	stepCh := make(chan raftpb.Message, 1024)
	proposalCh := make(chan proposalRequest)
	compactCh := make(chan compactRequest)
	initCh := make(chan error, 1)
	goroutine.SafeGo(s.cfg.Goroutines, "controller", "raft_run", func() {
		s.run(store, startup, stopCh, doneCh, stepCh, proposalCh, compactCh, initCh)
	})
	if err := <-initCh; err != nil {
		close(stopCh)
		<-doneCh
		s.store = nil
		s.recordDegraded(err)
		return err
	}

	s.stopCh = stopCh
	s.doneCh = doneCh
	s.stepCh = stepCh
	s.proposal = proposalCh
	s.compact = compactCh
	s.started = true
	return nil
}

// Stop terminates the Raft run loop and waits for all local resources to stop.
func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	if s.stopping {
		doneCh := s.doneCh
		s.mu.Unlock()
		if doneCh != nil {
			<-doneCh
		}
		return nil
	}
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.stopping = true
	s.mu.Unlock()

	close(stopCh)
	<-doneCh

	s.mu.Lock()
	s.started = false
	s.stopping = false
	s.stopped = true
	s.stopCh = nil
	s.doneCh = nil
	s.stepCh = nil
	s.proposal = nil
	s.compact = nil
	s.store = nil
	s.mu.Unlock()

	s.leaderID.Store(0)
	s.statusMu.Lock()
	st := s.status
	st.Role = RoleUnknown
	st.LeaderID = 0
	s.status = st
	s.statusMu.Unlock()
	return nil
}

// Propose appends a ControllerV2 command on the leader and waits until it is applied.
func (s *Service) Propose(ctx context.Context, cmd command.Command) error {
	_, err := s.submitProposal(ctx, proposalRequest{cmd: cmd})
	return err
}

// ProposeResult appends a ControllerV2 command and returns its FSM apply outcome.
func (s *Service) ProposeResult(ctx context.Context, cmd command.Command) (ProposalResult, error) {
	return s.submitProposal(ctx, proposalRequest{cmd: cmd})
}

// ProbePropose appends an empty Raft entry and waits until it is applied.
// It verifies the Controller proposal write path without mutating ControllerV2 cluster state.
func (s *Service) ProbePropose(ctx context.Context) error {
	_, err := s.submitProposal(ctx, proposalRequest{probe: true})
	return err
}

// CompactLog forces a local ControllerV2 Raft log compaction attempt.
func (s *Service) CompactLog(ctx context.Context) (LogCompactionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := LogCompactionResult{NodeID: s.cfg.NodeID}
	s.mu.Lock()
	if !s.started {
		stopped := s.stopped
		s.mu.Unlock()
		result.SkippedReason = LogCompactionSkipNotStarted
		err := ErrNotStarted
		if stopped {
			err = ErrStopped
		}
		if err != nil {
			result.Error = err.Error()
		}
		s.recordCompactionStatus(LogCompactionTriggerManual, result, err)
		return result, err
	}
	if s.stopping {
		s.mu.Unlock()
		result.SkippedReason = LogCompactionSkipNotStarted
		result.Error = ErrStopped.Error()
		s.recordCompactionStatus(LogCompactionTriggerManual, result, ErrStopped)
		return result, ErrStopped
	}
	compactCh := s.compact
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()
	if compactCh == nil {
		err := fmt.Errorf("%w: compaction loop unavailable", ErrNotStarted)
		result.SkippedReason = LogCompactionSkipNotStarted
		result.Error = err.Error()
		s.recordCompactionStatus(LogCompactionTriggerManual, result, err)
		return result, err
	}

	req := compactRequest{ctx: ctx, resp: make(chan compactResponse, 1)}
	select {
	case compactCh <- req:
	case <-ctx.Done():
		return result, ctx.Err()
	case <-doneCh:
		return result, s.currentError()
	case <-stopCh:
		return result, ErrStopped
	}

	select {
	case resp := <-req.resp:
		return resp.result, resp.err
	case <-ctx.Done():
		return result, ctx.Err()
	case <-doneCh:
		return result, s.currentError()
	case <-stopCh:
		return result, ErrStopped
	}
}

func (s *Service) submitProposal(ctx context.Context, req proposalRequest) (ProposalResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.started {
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return ProposalResult{}, ErrStopped
		}
		return ProposalResult{}, ErrNotStarted
	}
	if s.stopping {
		s.mu.Unlock()
		return ProposalResult{}, ErrStopped
	}
	proposalCh := s.proposal
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	req.ctx = ctx
	req.resp = make(chan proposalResponse, 1)
	select {
	case proposalCh <- req:
	case <-ctx.Done():
		return ProposalResult{}, ctx.Err()
	case <-doneCh:
		return ProposalResult{}, s.currentError()
	case <-stopCh:
		return ProposalResult{}, ErrStopped
	}

	select {
	case resp := <-req.resp:
		return resp.result, resp.err
	case <-ctx.Done():
		return ProposalResult{}, ctx.Err()
	case <-doneCh:
		return ProposalResult{}, s.currentError()
	case <-stopCh:
		return ProposalResult{}, ErrStopped
	}
}

// Step delivers an inbound Raft protocol message to the local RawNode run loop.
func (s *Service) Step(ctx context.Context, msg raftpb.Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if msg.To != 0 && msg.To != s.cfg.NodeID {
		return nil
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrNotStarted
	}
	if s.stopping {
		s.mu.Unlock()
		return ErrStopped
	}
	stepCh := s.stepCh
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	started := time.Now()
	select {
	case stepCh <- msg:
		s.observeStepQueue(stepCh)
		s.observeStepEnqueue("ok", time.Since(started))
		return nil
	case <-ctx.Done():
		s.observeStepQueue(stepCh)
		s.observeStepEnqueue("err", time.Since(started))
		return ctx.Err()
	case <-doneCh:
		s.observeStepQueue(stepCh)
		s.observeStepEnqueue("err", time.Since(started))
		return s.currentError()
	case <-stopCh:
		s.observeStepQueue(stepCh)
		s.observeStepEnqueue("err", time.Since(started))
		return ErrStopped
	}
}

func (s *Service) observeStepQueue(stepCh chan raftpb.Message) {
	if s == nil || s.cfg.Observer == nil || stepCh == nil {
		return
	}
	s.cfg.Observer.SetStepQueueDepth(len(stepCh), cap(stepCh))
}

func (s *Service) observeStepEnqueue(result string, d time.Duration) {
	if s == nil || s.cfg.Observer == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	s.cfg.Observer.ObserveStepEnqueue(result, d)
}

// LeaderID returns the leader currently known to this service.
func (s *Service) LeaderID() uint64 {
	if s == nil {
		return 0
	}
	return s.leaderID.Load()
}

// Status returns a goroutine-safe snapshot of the local ControllerV2 Raft status.
func (s *Service) Status() Status {
	if s == nil {
		return Status{Role: RoleUnknown}
	}
	s.statusMu.RLock()
	st := cloneStatus(s.status)
	s.statusMu.RUnlock()
	if s.cfg.StateMachine != nil && s.cfg.StateMachine.IsDegraded() {
		st.Degraded = true
	}
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	if store != nil {
		if first, err := store.FirstIndex(); err == nil {
			st.FirstIndex = first
		}
		if last, err := store.LastIndex(); err == nil {
			st.LastIndex = last
		}
		if snap, err := store.Snapshot(); err == nil {
			st.SnapshotIndex = snap.Metadata.Index
			st.SnapshotTerm = snap.Metadata.Term
		}
	}
	if st.NodeID == 0 {
		st.NodeID = s.cfg.NodeID
	}
	if st.Role == "" {
		st.Role = RoleUnknown
	}
	return st
}

func (s *Service) recordCompactionStatus(trigger string, result LogCompactionResult, err error) {
	if s == nil {
		return
	}
	now := time.Now()
	s.statusMu.Lock()
	st := s.status
	status := st.Compaction
	status.Enabled = s.cfg.SnapshotCount > 0
	status.TriggerEntries = s.cfg.SnapshotCount
	status.CheckInterval = s.cfg.SnapshotMinInterval
	status.LastTrigger = trigger
	status.LastAttemptAt = now
	status.LastAppliedIndex = result.AppliedIndex
	status.BeforeSnapshotIndex = result.BeforeSnapshotIndex
	status.AfterSnapshotIndex = result.AfterSnapshotIndex
	status.Compacted = result.Compacted
	status.SkippedReason = result.SkippedReason
	if err != nil {
		status.LastError = err.Error()
		status.LastErrorAt = now
	} else if result.Error != "" {
		status.LastError = result.Error
		status.LastErrorAt = now
	} else if result.Compacted {
		status.LastSuccessAt = now
		status.LastError = ""
		status.LastErrorAt = time.Time{}
	} else {
		status.LastError = ""
		status.LastErrorAt = time.Time{}
	}
	st.Compaction = status
	s.status = st
	s.statusMu.Unlock()
}
