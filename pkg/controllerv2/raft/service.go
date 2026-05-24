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
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
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
)

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
	State     multiraft.BootstrapState
	Snapshot  raftpb.Snapshot
	LastIndex uint64
}

type storageAdapter struct {
	storage multiraft.Storage
	memory  *loadedMemoryStorage
}

type loadedMemoryStorage struct {
	*etcdraft.MemoryStorage
	confState raftpb.ConfState
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
	cfg := s.cfg.normalized()
	if err := cfg.validate(); err != nil {
		s.recordDegraded(err)
		return err
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	if s.stopping {
		s.mu.Unlock()
		return ErrStopped
	}
	s.cfg = cfg
	s.err = nil
	s.statusMu.Lock()
	s.status = initialStatus(cfg)
	s.statusMu.Unlock()
	s.mu.Unlock()

	if err := s.recoverStartup(ctx); err != nil {
		s.recordDegraded(err)
		return err
	}

	storageView := newStorageAdapter(cfg.Storage)
	bootstrapState, snapshot, _, err := storageView.load(ctx)
	if err != nil {
		s.recordDegraded(err)
		return err
	}
	lastIndex, err := cfg.Storage.LastIndex(ctx)
	if err != nil {
		s.recordDegraded(err)
		return err
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	stepCh := make(chan raftpb.Message, 1024)
	proposalCh := make(chan proposalRequest)
	initCh := make(chan error, 1)
	go s.run(storageView, runStartupState{State: bootstrapState, Snapshot: snapshot, LastIndex: lastIndex}, stopCh, doneCh, stepCh, proposalCh, initCh)
	if err := <-initCh; err != nil {
		close(stopCh)
		<-doneCh
		s.recordDegraded(err)
		return err
	}

	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		close(stopCh)
		<-doneCh
		return ErrStopped
	}
	s.stopCh = stopCh
	s.doneCh = doneCh
	s.stepCh = stepCh
	s.proposal = proposalCh
	s.started = true
	s.mu.Unlock()
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

func (s *Service) run(storageView *storageAdapter, startup runStartupState, stopCh <-chan struct{}, doneCh chan struct{}, stepCh <-chan raftpb.Message, proposalCh <-chan proposalRequest, initCh chan<- error) {
	defer close(doneCh)
	rawNode, err := s.newRawNode(storageView, startup)
	if err != nil {
		initCh <- err
		return
	}
	if shouldBootstrap(startup.State, startup.Snapshot, startup.LastIndex) && s.cfg.AllowBootstrap && isSmallestPeer(s.cfg.NodeID, s.cfg.Peers) {
		if err := rawNode.Bootstrap(raftPeers(s.cfg.Peers)); err != nil {
			initCh <- err
			return
		}
	}
	s.updateStatus(rawNode, nil)
	initCh <- nil

	ticker := time.NewTicker(s.cfg.TickInterval)
	defer ticker.Stop()

	pendingQueue := make([]trackedProposal, 0, 8)
	pendingByIndex := make(map[uint64]trackedProposal)
	latestConfState := cloneConfState(startup.State.ConfState)
	if !etcdraft.IsEmptySnap(startup.Snapshot) {
		latestConfState = cloneConfState(startup.Snapshot.Metadata.ConfState)
	}

	processReady := func() error {
		for rawNode.HasReady() {
			ready := rawNode.Ready()
			if err := storageView.persistReady(context.Background(), ready); err != nil {
				failTracked(pendingQueue, pendingByIndex, err)
				return err
			}
			for _, entry := range ready.Entries {
				if entry.Type != raftpb.EntryNormal || len(entry.Data) == 0 || len(pendingQueue) == 0 {
					continue
				}
				tracked := pendingQueue[0]
				pendingQueue = pendingQueue[1:]
				pendingByIndex[entry.Index] = tracked
			}
			if len(ready.Messages) > 0 {
				if err := s.cfg.Transport.Send(context.Background(), ready.Messages); err != nil {
					failTracked(pendingQueue, pendingByIndex, err)
					return err
				}
			}
			if err := s.applyCommittedEntries(context.Background(), rawNode, ready.CommittedEntries, storageView, &latestConfState, pendingByIndex); err != nil {
				failTracked(pendingQueue, pendingByIndex, err)
				return err
			}
			rawNode.Advance(ready)
			s.updateStatus(rawNode, nil)
			failInflightProposalsOnLeaderLoss(rawNode.Status().RaftState, &pendingQueue, pendingByIndex)
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
			failTracked(pendingQueue, pendingByIndex, ErrStopped)
			return
		case <-ticker.C:
			rawNode.Tick()
			s.updateStatus(rawNode, nil)
			failInflightProposalsOnLeaderLoss(rawNode.Status().RaftState, &pendingQueue, pendingByIndex)
		case msg := <-stepCh:
			if err := rawNode.Step(msg); err != nil && !errors.Is(err, etcdraft.ErrStepLocalMsg) {
				failTracked(pendingQueue, pendingByIndex, err)
				s.setRunError(err)
				return
			}
			s.updateStatus(rawNode, nil)
			failInflightProposalsOnLeaderLoss(rawNode.Status().RaftState, &pendingQueue, pendingByIndex)
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
			pendingQueue = append(pendingQueue, trackedProposal{resp: req.resp})
		}
	}
}

func (s *Service) newRawNode(storageView *storageAdapter, startup runStartupState) (*etcdraft.RawNode, error) {
	applied := startup.State.AppliedIndex
	if !etcdraft.IsEmptySnap(startup.Snapshot) && startup.Snapshot.Metadata.Index > applied {
		applied = startup.Snapshot.Metadata.Index
	}
	return etcdraft.NewRawNode(&etcdraft.Config{
		ID:                       s.cfg.NodeID,
		ElectionTick:             electionTick,
		HeartbeatTick:            heartbeatTick,
		Storage:                  storageView.memory,
		Applied:                  applied,
		MaxSizePerMsg:            math.MaxUint64,
		MaxCommittedSizePerReady: math.MaxUint64,
		MaxInflightMsgs:          256,
		CheckQuorum:              true,
		PreVote:                  true,
	})
}

func (s *Service) applyCommittedEntries(ctx context.Context, rawNode *etcdraft.RawNode, entries []raftpb.Entry, storageView *storageAdapter, latestConfState *raftpb.ConfState, pendingByIndex map[uint64]trackedProposal) error {
	for _, entry := range entries {
		tracked, hasTracked := pendingByIndex[entry.Index]
		applyErr := s.applyCommittedEntry(ctx, rawNode, entry, latestConfState)
		if applyErr != nil {
			if hasTracked {
				tracked.resp <- applyErr
				delete(pendingByIndex, entry.Index)
			}
			return applyErr
		}
		if err := storageView.storage.MarkApplied(ctx, entry.Index); err != nil {
			if hasTracked {
				tracked.resp <- err
				delete(pendingByIndex, entry.Index)
			}
			return err
		}
		if hasTracked {
			tracked.resp <- nil
			delete(pendingByIndex, entry.Index)
		}
	}
	return nil
}

func (s *Service) applyCommittedEntry(ctx context.Context, rawNode *etcdraft.RawNode, entry raftpb.Entry, latestConfState *raftpb.ConfState) error {
	switch entry.Type {
	case raftpb.EntryNormal:
		if len(entry.Data) == 0 {
			return s.advanceStateApplied(ctx, entry.Index)
		}
		cmd, err := command.Decode(entry.Data)
		if err != nil {
			return err
		}
		_, err = s.cfg.StateMachine.Apply(ctx, entry.Index, cmd)
		return err
	case raftpb.EntryConfChange:
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(entry.Data); err != nil {
			return err
		}
		latest := rawNode.ApplyConfChange(cc)
		*latestConfState = cloneConfState(*latest)
		return s.advanceStateApplied(ctx, entry.Index)
	case raftpb.EntryConfChangeV2:
		var cc raftpb.ConfChangeV2
		if err := cc.Unmarshal(entry.Data); err != nil {
			return err
		}
		latest := rawNode.ApplyConfChange(cc)
		*latestConfState = cloneConfState(*latest)
		return s.advanceStateApplied(ctx, entry.Index)
	}
	return nil
}

func (s *Service) advanceStateApplied(ctx context.Context, index uint64) error {
	snap := s.cfg.StateMachine.Snapshot(ctx)
	if snap.Revision == 0 || index <= snap.AppliedRaftIndex {
		return nil
	}
	_, err := s.cfg.StateMachine.Apply(ctx, index, command.Command{
		Kind:        command.KindUpdateControllerVoters,
		Controllers: snap.Controllers,
	})
	return err
}

func (s *Service) recoverStartup(ctx context.Context) error {
	boot, err := s.cfg.Storage.InitialState(ctx)
	if err != nil {
		return err
	}
	last, err := s.cfg.Storage.LastIndex(ctx)
	if err != nil {
		return err
	}
	if err := s.cfg.StateMachine.Load(ctx); err != nil {
		target := recoveryTarget(boot)
		if target == 0 {
			return fmt.Errorf("controllerv2/raft: corrupt state file without committed log history: %w", err)
		}
		if err := s.replayCompleteHistory(ctx, target, target > boot.AppliedIndex); err != nil {
			return fmt.Errorf("controllerv2/raft: rebuild corrupt state: %w", err)
		}
		return nil
	}

	snap := s.cfg.StateMachine.Snapshot(ctx)
	if snap.Revision == 0 {
		if isEmptyRaftLog(boot, last) {
			if s.cfg.AllowBootstrap {
				return nil
			}
			return fmt.Errorf("%w: empty ControllerV2 state requires bootstrap", ErrInvalidConfig)
		}
		target := recoveryTarget(boot)
		if target == 0 {
			return fmt.Errorf("controllerv2/raft: missing state file without committed log history")
		}
		return s.replayCompleteHistory(ctx, target, target > boot.AppliedIndex)
	}

	if snap.AppliedRaftIndex < boot.AppliedIndex {
		return s.replayRange(ctx, snap.AppliedRaftIndex+1, boot.AppliedIndex, false, false)
	}
	return nil
}

func recoveryTarget(boot multiraft.BootstrapState) uint64 {
	if boot.AppliedIndex > 0 {
		return boot.AppliedIndex
	}
	return boot.HardState.Commit
}

func (s *Service) replayCompleteHistory(ctx context.Context, target uint64, markApplied bool) error {
	first, err := s.cfg.Storage.FirstIndex(ctx)
	if err != nil {
		return err
	}
	return s.replayRange(ctx, first, target, true, markApplied)
}

func (s *Service) replayRange(ctx context.Context, lo, hi uint64, requireInit bool, markApplied bool) error {
	if hi == 0 || lo > hi {
		return nil
	}
	entries, err := s.cfg.Storage.Entries(ctx, lo, hi+1, 0)
	if err != nil {
		return err
	}
	expected := lo
	seenInit := false
	for _, entry := range entries {
		if entry.Index != expected {
			return fmt.Errorf("controllerv2/raft: missing required log entry %d", expected)
		}
		expected++
		if entry.Type == raftpb.EntryNormal && len(entry.Data) > 0 {
			cmd, err := command.Decode(entry.Data)
			if err != nil {
				return err
			}
			if requireInit && !seenInit {
				if cmd.Kind != command.KindInitClusterState {
					return fmt.Errorf("controllerv2/raft: missing init command before entry %d", entry.Index)
				}
				seenInit = true
			}
			if _, err := s.cfg.StateMachine.Apply(ctx, entry.Index, cmd); err != nil {
				return err
			}
		} else if err := s.advanceStateApplied(ctx, entry.Index); err != nil {
			return err
		}
		if markApplied {
			if err := s.cfg.Storage.MarkApplied(ctx, entry.Index); err != nil {
				return err
			}
		}
	}
	if expected != hi+1 {
		return fmt.Errorf("controllerv2/raft: missing required log entry %d", expected)
	}
	if requireInit && !seenInit {
		return fmt.Errorf("controllerv2/raft: complete history does not contain init command")
	}
	if requireInit && s.cfg.StateMachine.Snapshot(ctx).Revision == 0 {
		return fmt.Errorf("controllerv2/raft: replay did not rebuild cluster state")
	}
	return nil
}

func newStorageAdapter(storage multiraft.Storage) *storageAdapter {
	return &storageAdapter{storage: storage}
}

func (s *storageAdapter) load(ctx context.Context) (multiraft.BootstrapState, raftpb.Snapshot, *loadedMemoryStorage, error) {
	state, err := s.storage.InitialState(ctx)
	if err != nil {
		return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
	}
	memory := etcdraft.NewMemoryStorage()
	snap, err := s.storage.Snapshot(ctx)
	if err != nil {
		return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
	}
	if !etcdraft.IsEmptySnap(snap) {
		if err := memory.ApplySnapshot(snap); err != nil {
			return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
		}
	}
	first, err := s.storage.FirstIndex(ctx)
	if err != nil {
		return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
	}
	last, err := s.storage.LastIndex(ctx)
	if err != nil {
		return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
	}
	if last >= first && last > 0 {
		entries, err := s.storage.Entries(ctx, first, last+1, 0)
		if err != nil {
			return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
		}
		if len(entries) > 0 {
			if err := memory.Append(entries); err != nil {
				return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
			}
		}
	}
	if !etcdraft.IsEmptyHardState(state.HardState) {
		if err := memory.SetHardState(state.HardState); err != nil {
			return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
		}
	}
	loadedConfState := state.ConfState
	if !etcdraft.IsEmptySnap(snap) {
		loadedConfState = snap.Metadata.ConfState
	}
	loaded := newLoadedMemoryStorage(memory, loadedConfState)
	s.memory = loaded
	return state, snap, loaded, nil
}

func (s *storageAdapter) persistReady(ctx context.Context, ready etcdraft.Ready) error {
	persist := multiraft.PersistentState{}
	needsSave := false
	if !etcdraft.IsEmptyHardState(ready.HardState) {
		hs := ready.HardState
		persist.HardState = &hs
		needsSave = true
	}
	if len(ready.Entries) > 0 {
		persist.Entries = append([]raftpb.Entry(nil), ready.Entries...)
		needsSave = true
	}
	if !etcdraft.IsEmptySnap(ready.Snapshot) {
		snap := ready.Snapshot
		persist.Snapshot = &snap
		needsSave = true
	}
	if needsSave {
		if err := s.storage.Save(ctx, persist); err != nil {
			return err
		}
	}
	if persist.Snapshot != nil {
		if err := s.memory.ApplySnapshot(*persist.Snapshot); err != nil {
			return err
		}
	}
	if len(persist.Entries) > 0 {
		if err := s.memory.Append(persist.Entries); err != nil {
			return err
		}
	}
	if persist.HardState != nil {
		if err := s.memory.SetHardState(*persist.HardState); err != nil {
			return err
		}
	}
	return nil
}

func newLoadedMemoryStorage(memory *etcdraft.MemoryStorage, confState raftpb.ConfState) *loadedMemoryStorage {
	return &loadedMemoryStorage{MemoryStorage: memory, confState: cloneConfState(confState)}
}

func (s *loadedMemoryStorage) InitialState() (raftpb.HardState, raftpb.ConfState, error) {
	hs, _, err := s.MemoryStorage.InitialState()
	if err != nil {
		return raftpb.HardState{}, raftpb.ConfState{}, err
	}
	return hs, cloneConfState(s.confState), nil
}

func (s *loadedMemoryStorage) ApplySnapshot(snapshot raftpb.Snapshot) error {
	if err := s.MemoryStorage.ApplySnapshot(snapshot); err != nil {
		return err
	}
	s.confState = cloneConfState(snapshot.Metadata.ConfState)
	return nil
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

func failTracked(queue []trackedProposal, byIndex map[uint64]trackedProposal, err error) {
	for _, tracked := range queue {
		tracked.resp <- err
	}
	for idx, tracked := range byIndex {
		tracked.resp <- err
		delete(byIndex, idx)
	}
}

func failInflightProposalsOnLeaderLoss(role etcdraft.StateType, queue *[]trackedProposal, byIndex map[uint64]trackedProposal) {
	if role == etcdraft.StateLeader {
		return
	}
	for _, tracked := range *queue {
		tracked.resp <- ErrNotLeader
	}
	*queue = (*queue)[:0]
	for idx, tracked := range byIndex {
		tracked.resp <- ErrNotLeader
		delete(byIndex, idx)
	}
}

func shouldBootstrap(state multiraft.BootstrapState, snapshot raftpb.Snapshot, last uint64) bool {
	return last == 0 && state.AppliedIndex == 0 && etcdraft.IsEmptyHardState(state.HardState) && isZeroConfState(state.ConfState) && etcdraft.IsEmptySnap(snapshot)
}

func isEmptyRaftLog(state multiraft.BootstrapState, last uint64) bool {
	return last == 0 && state.AppliedIndex == 0 && etcdraft.IsEmptyHardState(state.HardState) && isZeroConfState(state.ConfState)
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

func cloneConfState(state raftpb.ConfState) raftpb.ConfState {
	cloned := state
	cloned.Voters = append([]uint64(nil), state.Voters...)
	cloned.Learners = append([]uint64(nil), state.Learners...)
	cloned.VotersOutgoing = append([]uint64(nil), state.VotersOutgoing...)
	cloned.LearnersNext = append([]uint64(nil), state.LearnersNext...)
	return cloned
}

func isZeroConfState(state raftpb.ConfState) bool {
	return len(state.Voters) == 0 && len(state.Learners) == 0 && len(state.VotersOutgoing) == 0 && len(state.LearnersNext) == 0 && !state.AutoLeave
}
