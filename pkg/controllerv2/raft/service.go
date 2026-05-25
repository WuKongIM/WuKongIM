package raft

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	etcdraft "go.etcd.io/raft/v3"
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
	ctx  context.Context
	cmd  command.Command
	resp chan error
}

type trackedProposal struct {
	resp chan error
}

type runStartupState struct {
	HardState    raftpb.HardState
	ConfState    raftpb.ConfState
	Snapshot     raftpb.Snapshot
	LastIndex    uint64
	AppliedIndex uint64
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
	if ctx == nil {
		ctx = context.Background()
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
	proposalCh := s.proposal
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	req := proposalRequest{ctx: ctx, cmd: cmd, resp: make(chan error, 1)}
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

func (s *Service) run(store *raftstore.Store, startup runStartupState, stopCh <-chan struct{}, doneCh chan struct{}, stepCh <-chan raftpb.Message, proposalCh <-chan proposalRequest, initCh chan<- error) {
	defer close(doneCh)
	defer store.Close()
	rawNode, err := s.newRawNode(store, startup)
	if err != nil {
		initCh <- err
		return
	}
	if shouldBootstrap(startup) && s.cfg.AllowBootstrap && isSmallestPeer(s.cfg.NodeID, s.cfg.Peers) {
		if err := rawNode.Bootstrap(raftPeers(s.cfg.Peers)); err != nil {
			initCh <- err
			return
		}
	}

	tracker := newProposalTracker()
	var trackerMu sync.Mutex
	complete := func(index uint64, err error) {
		trackerMu.Lock()
		defer trackerMu.Unlock()
		tracker.complete(index, err)
	}
	scheduler := newApplyScheduler(applySchedulerConfig{MaxEntries: s.cfg.MaxApplyBatchEntries, MaxBytes: s.cfg.MaxApplyBatchBytes, MaxDelay: s.cfg.MaxApplyDelay}, s.cfg.StateMachine, store, complete)
	scheduler.onApplied = func(ctx context.Context, index uint64) error { return s.maybeSnapshot(ctx, store, index) }
	scheduler.start(context.Background())
	defer scheduler.stop()

	s.updateStatus(rawNode, nil)
	initCh <- nil

	ticker := time.NewTicker(s.cfg.TickInterval)
	defer ticker.Stop()

	failAll := func(err error) {
		trackerMu.Lock()
		tracker.failAll(err)
		trackerMu.Unlock()
	}
	failOnLeaderLoss := func() {
		if rawNode.Status().RaftState == etcdraft.StateLeader {
			return
		}
		trackerMu.Lock()
		tracker.failAll(ErrNotLeader)
		trackerMu.Unlock()
	}
	processReady := func() error {
		for rawNode.HasReady() {
			ready := rawNode.Ready()
			isLeader := rawNode.Status().RaftState == etcdraft.StateLeader
			if isLeader {
				s.sendReadyMessages(ready.Messages)
			}
			if err := store.SaveReady(context.Background(), ready.HardState, ready.Entries, ready.Snapshot); err != nil {
				failAll(err)
				return err
			}
			trackerMu.Lock()
			tracker.bindAppended(ready.Entries)
			trackerMu.Unlock()
			if !isLeader {
				s.sendReadyMessages(ready.Messages)
			}
			job := toApply{entries: ready.CommittedEntries, snapshot: ready.Snapshot}
			confCount := countConfChanges(ready.CommittedEntries)
			if confCount > 0 {
				job.confChangeC = make(chan confChangeRequest, confCount)
			}
			if err := scheduler.enqueue(context.Background(), job); err != nil {
				failAll(err)
				return err
			}
			for i := 0; i < confCount; i++ {
				req := <-job.confChangeC
				cs, err := applyConfChange(rawNode, req.entry)
				req.resp <- confChangeResult{state: cs, err: err}
			}
			rawNode.Advance(ready)
			s.updateStatus(rawNode, nil)
			failOnLeaderLoss()
		}
		return nil
	}

	for {
		if err := processReady(); err != nil {
			s.setRunError(err)
			return
		}
		select {
		case <-stopCh:
			failAll(ErrStopped)
			return
		case <-ticker.C:
			rawNode.Tick()
			s.updateStatus(rawNode, nil)
			failOnLeaderLoss()
		case msg := <-stepCh:
			if err := rawNode.Step(msg); err != nil && !errors.Is(err, etcdraft.ErrStepLocalMsg) {
				failAll(err)
				s.setRunError(err)
				return
			}
			s.updateStatus(rawNode, nil)
			failOnLeaderLoss()
		case req := <-proposalCh:
			if err := req.ctx.Err(); err != nil {
				req.resp <- err
				continue
			}
			if rawNode.Status().RaftState != etcdraft.StateLeader {
				req.resp <- ErrNotLeader
				continue
			}
			data, err := command.Encode(req.cmd)
			if err != nil {
				req.resp <- err
				continue
			}
			if err := rawNode.Propose(data); err != nil {
				req.resp <- err
				continue
			}
			trackerMu.Lock()
			tracker.enqueue(trackedProposal{resp: req.resp})
			trackerMu.Unlock()
		}
	}
}

func (s *Service) newRawNode(store etcdraft.Storage, startup runStartupState) (*etcdraft.RawNode, error) {
	applied := startup.AppliedIndex
	if !etcdraft.IsEmptySnap(startup.Snapshot) && startup.Snapshot.Metadata.Index > applied {
		applied = startup.Snapshot.Metadata.Index
	}
	return etcdraft.NewRawNode(&etcdraft.Config{
		ID:                       s.cfg.NodeID,
		ElectionTick:             electionTick,
		HeartbeatTick:            heartbeatTick,
		Storage:                  store,
		Applied:                  applied,
		MaxSizePerMsg:            math.MaxUint64,
		MaxCommittedSizePerReady: math.MaxUint64,
		MaxInflightMsgs:          256,
		CheckQuorum:              true,
		PreVote:                  true,
	})
}

func (s *Service) recoverStartup(ctx context.Context, store *raftstore.Store) (runStartupState, error) {
	if err := s.cfg.StateMachine.Load(ctx); err != nil {
		return runStartupState{}, err
	}
	hs, cs, err := store.InitialState()
	if err != nil {
		return runStartupState{}, err
	}
	snap, err := store.Snapshot()
	if err != nil {
		return runStartupState{}, err
	}
	last, err := store.LastIndex()
	if err != nil {
		return runStartupState{}, err
	}
	stateSnap := s.cfg.StateMachine.Snapshot(ctx)
	if stateSnap.Revision == 0 && !etcdraft.IsEmptySnap(snap) && len(snap.Data) > 0 {
		restored, err := state.Decode(snap.Data)
		if err != nil {
			return runStartupState{}, err
		}
		if restored.AppliedRaftIndex == 0 || restored.AppliedRaftIndex < snap.Metadata.Index {
			restored.AppliedRaftIndex = snap.Metadata.Index
		}
		if err := s.cfg.StateMachine.Restore(ctx, restored); err != nil {
			return runStartupState{}, err
		}
		stateSnap = s.cfg.StateMachine.Snapshot(ctx)
	}
	if stateSnap.Revision != 0 {
		if stateSnap.AppliedRaftIndex > hs.Commit {
			return runStartupState{}, fmt.Errorf("controllerv2/raft: state file applied raft index %d is ahead of committed raft index %d", stateSnap.AppliedRaftIndex, hs.Commit)
		}
		if stateSnap.AppliedRaftIndex > last {
			return runStartupState{}, fmt.Errorf("controllerv2/raft: state file applied raft index %d is ahead of local last raft index %d", stateSnap.AppliedRaftIndex, last)
		}
	}
	replayFrom := store.AppliedIndex() + 1
	if stateSnap.Revision != 0 {
		replayFrom = stateSnap.AppliedRaftIndex + 1
	}
	if hs.Commit >= replayFrom {
		entries, err := store.Entries(replayFrom, hs.Commit+1, 0)
		if err != nil {
			return runStartupState{}, err
		}
		replayer := newApplyScheduler(applySchedulerConfig{MaxEntries: s.cfg.MaxApplyBatchEntries, MaxBytes: s.cfg.MaxApplyBatchBytes, MaxDelay: s.cfg.MaxApplyDelay}, s.cfg.StateMachine, store, nil)
		if err := replayer.applyEntries(ctx, entries, nil); err != nil {
			return runStartupState{}, err
		}
	}
	return runStartupState{HardState: hs, ConfState: cs, Snapshot: snap, LastIndex: last, AppliedIndex: store.AppliedIndex()}, nil
}

func (s *Service) maybeSnapshot(ctx context.Context, store *raftstore.Store, applied uint64) error {
	if s.cfg.SnapshotCount == 0 || applied < s.cfg.SnapshotCount {
		return nil
	}
	snap, err := store.Snapshot()
	if err != nil {
		return err
	}
	if applied < snap.Metadata.Index+s.cfg.SnapshotCount {
		return nil
	}
	now := time.Now()
	s.snapshotMu.Lock()
	if !s.lastSnapshot.IsZero() && now.Sub(s.lastSnapshot) < s.cfg.SnapshotMinInterval {
		s.snapshotMu.Unlock()
		return nil
	}
	s.lastSnapshot = now
	s.snapshotMu.Unlock()

	st := s.cfg.StateMachine.Snapshot(ctx)
	if st.Revision == 0 {
		return nil
	}
	data, err := state.Encode(st)
	if err != nil {
		return err
	}
	term, err := store.Term(applied)
	if err != nil {
		term = s.Status().Term
	}
	_, conf, err := store.InitialState()
	if err != nil {
		return err
	}
	if err := store.SaveSnapshot(ctx, raftpb.Snapshot{Data: data, Metadata: raftpb.SnapshotMetadata{Index: applied, Term: term, ConfState: conf}}); err != nil {
		return err
	}
	if applied > s.cfg.SnapshotCatchUpEntries {
		return store.Compact(ctx, applied-s.cfg.SnapshotCatchUpEntries)
	}
	return nil
}

func (s *Service) sendReadyMessages(messages []raftpb.Message) {
	if len(messages) == 0 {
		return
	}
	s.cfg.Transport.Send(messages)
}

func countConfChanges(entries []raftpb.Entry) int {
	count := 0
	for _, entry := range entries {
		if entry.Type == raftpb.EntryConfChange || entry.Type == raftpb.EntryConfChangeV2 {
			count++
		}
	}
	return count
}

func applyConfChange(rawNode *etcdraft.RawNode, entry raftpb.Entry) (raftpb.ConfState, error) {
	switch entry.Type {
	case raftpb.EntryConfChange:
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(entry.Data); err != nil {
			return raftpb.ConfState{}, err
		}
		return *rawNode.ApplyConfChange(cc), nil
	case raftpb.EntryConfChangeV2:
		var cc raftpb.ConfChangeV2
		if err := cc.Unmarshal(entry.Data); err != nil {
			return raftpb.ConfState{}, err
		}
		return *rawNode.ApplyConfChange(cc), nil
	default:
		return raftpb.ConfState{}, nil
	}
}

func (s *Service) setRunError(err error) {
	s.mu.Lock()
	s.err = err
	s.started = false
	s.mu.Unlock()
	s.recordDegraded(err)
}

func (s *Service) currentError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	return ErrStopped
}

func (s *Service) recordDegraded(err error) {
	if err == nil {
		return
	}
	s.statusMu.Lock()
	st := s.status
	if st.NodeID == 0 {
		st.NodeID = s.cfg.NodeID
	}
	if st.Role == "" {
		st.Role = RoleUnknown
	}
	st.Degraded = true
	st.ErrorReason = err.Error()
	s.status = st
	s.statusMu.Unlock()
}

func (s *Service) updateStatus(rawNode *etcdraft.RawNode, err error) {
	status := rawNode.Status()
	s.leaderID.Store(status.Lead)
	s.statusMu.Lock()
	st := s.status
	st.NodeID = s.cfg.NodeID
	st.Role = raftRoleName(status.RaftState)
	st.LeaderID = status.Lead
	st.Term = status.Term
	st.CommitIndex = status.Commit
	st.AppliedIndex = status.Applied
	if err != nil {
		st.Degraded = true
		st.ErrorReason = err.Error()
	}
	s.status = st
	s.statusMu.Unlock()
}

func shouldBootstrap(startup runStartupState) bool {
	return startup.LastIndex == 0 && startup.AppliedIndex == 0 && etcdraft.IsEmptyHardState(startup.HardState) && isZeroConfState(startup.ConfState) && etcdraft.IsEmptySnap(startup.Snapshot)
}

func isSmallestPeer(nodeID uint64, peers []Peer) bool {
	if len(peers) == 0 {
		return false
	}
	min := peers[0].NodeID
	for _, peer := range peers[1:] {
		if peer.NodeID < min {
			min = peer.NodeID
		}
	}
	return nodeID == min
}

func raftPeers(peers []Peer) []etcdraft.Peer {
	out := make([]etcdraft.Peer, 0, len(peers))
	for _, peer := range peers {
		out = append(out, etcdraft.Peer{ID: peer.NodeID})
	}
	return out
}

func isZeroConfState(state raftpb.ConfState) bool {
	return len(state.Voters) == 0 && len(state.Learners) == 0 && len(state.VotersOutgoing) == 0 && len(state.LearnersNext) == 0 && !state.AutoLeave
}
