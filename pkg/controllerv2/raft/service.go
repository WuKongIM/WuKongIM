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
	err      error

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
	resp  chan error
}

type trackedProposal struct {
	resp  chan error
	probe bool
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
	startup, err := s.recoverStartup(ctx, store)
	if err != nil {
		_ = store.Close()
		s.recordDegraded(err)
		return err
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	stepCh := make(chan raftpb.Message, 1024)
	proposalCh := make(chan proposalRequest)
	initCh := make(chan error, 1)
	go s.run(store, startup, stopCh, doneCh, stepCh, proposalCh, initCh)
	if err := <-initCh; err != nil {
		close(stopCh)
		<-doneCh
		s.recordDegraded(err)
		return err
	}

	s.stopCh = stopCh
	s.doneCh = doneCh
	s.stepCh = stepCh
	s.proposal = proposalCh
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
	return s.submitProposal(ctx, proposalRequest{cmd: cmd})
}

// ProbePropose appends an empty Raft entry and waits until it is applied.
// It verifies the Controller proposal write path without mutating ControllerV2 cluster state.
func (s *Service) ProbePropose(ctx context.Context) error {
	return s.submitProposal(ctx, proposalRequest{probe: true})
}

func (s *Service) submitProposal(ctx context.Context, req proposalRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.started {
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return ErrStopped
		}
		return ErrNotStarted
	}
	if s.stopping {
		s.mu.Unlock()
		return ErrStopped
	}
	proposalCh := s.proposal
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	req.ctx = ctx
	req.resp = make(chan error, 1)
	select {
	case proposalCh <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-doneCh:
		return s.currentError()
	case <-stopCh:
		return ErrStopped
	}

	select {
	case err := <-req.resp:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-doneCh:
		return s.currentError()
	case <-stopCh:
		return ErrStopped
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

	select {
	case stepCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-doneCh:
		return s.currentError()
	case <-stopCh:
		return ErrStopped
	}
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
	if st.NodeID == 0 {
		st.NodeID = s.cfg.NodeID
	}
	if st.Role == "" {
		st.Role = RoleUnknown
	}
	return st
}
