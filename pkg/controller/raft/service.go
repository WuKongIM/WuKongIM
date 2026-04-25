package raft

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	tickInterval  time.Duration = 100 * time.Millisecond
	electionTick  int           = 10
	heartbeatTick int           = 1
)

var (
	ErrInvalidConfig       = errors.New("controllerraft: invalid config")
	ErrNotStarted          = errors.New("controllerraft: not started")
	ErrStopped             = errors.New("controllerraft: stopped")
	ErrNotLeader           = errors.New("controllerraft: not leader")
	ErrSnapshotUnsupported = errors.New("controllerraft: snapshot unsupported")

	rawNodeBootstrap = func(_ uint64, rawNode *raft.RawNode, peers []raft.Peer) error {
		return rawNode.Bootstrap(peers)
	}
)

type Service struct {
	cfg Config

	mu      sync.Mutex
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	err     error

	leaderID atomic.Uint64

	stepCh    chan raftpb.Message
	proposeCh chan proposalRequest

	transport *raftTransport
}

type proposalRequest struct {
	ctx  context.Context
	cmd  slotcontroller.Command
	resp chan error
}

type trackedProposal struct {
	resp chan error
}

type storageAdapter struct {
	storage multiraft.Storage
	memory  *loadedMemoryStorage
}

type loadedMemoryStorage struct {
	*raft.MemoryStorage
	confState raftpb.ConfState
}

type commandEnvelope struct {
	Kind             slotcontroller.CommandKind           `json:"kind"`
	Report           *slotcontroller.AgentReport          `json:"report,omitempty"`
	Op               *slotcontroller.OperatorRequest      `json:"op,omitempty"`
	Advance          *taskAdvanceEnvelope                 `json:"advance,omitempty"`
	Assignment       *slotcontrollerAssignmentEnvelope    `json:"assignment,omitempty"`
	Task             *slotcontrollerReconcileTaskEnvelope `json:"task,omitempty"`
	Migration        *slotcontroller.MigrationRequest     `json:"migration,omitempty"`
	AddSlot          *slotcontroller.AddSlotRequest       `json:"add_slot,omitempty"`
	RemoveSlot       *slotcontroller.RemoveSlotRequest    `json:"remove_slot,omitempty"`
	NodeStatusUpdate *slotcontroller.NodeStatusUpdate     `json:"node_status_update,omitempty"`
}

type taskAdvanceEnvelope struct {
	SlotID  uint32    `json:"slot_id"`
	Attempt uint32    `json:"attempt,omitempty"`
	Now     time.Time `json:"now"`
	Err     string    `json:"err,omitempty"`
}

type slotcontrollerAssignmentEnvelope struct {
	SlotID         uint32   `json:"slot_id"`
	DesiredPeers   []uint64 `json:"desired_peers,omitempty"`
	ConfigEpoch    uint64   `json:"config_epoch,omitempty"`
	BalanceVersion uint64   `json:"balance_version,omitempty"`
}

type slotcontrollerReconcileTaskEnvelope struct {
	SlotID     uint32                    `json:"slot_id"`
	Kind       controllermeta.TaskKind   `json:"kind"`
	Step       controllermeta.TaskStep   `json:"step"`
	SourceNode uint64                    `json:"source_node,omitempty"`
	TargetNode uint64                    `json:"target_node,omitempty"`
	Attempt    uint32                    `json:"attempt,omitempty"`
	NextRunAt  time.Time                 `json:"next_run_at,omitempty"`
	Status     controllermeta.TaskStatus `json:"status,omitempty"`
	LastError  string                    `json:"last_error,omitempty"`
}

func NewService(cfg Config) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) Start(ctx context.Context) error {
	if err := s.cfg.validateCore(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}

	store := s.cfg.LogDB.ForController()
	storageView := newStorageAdapter(store)
	state, snapshot, _, err := storageView.load(ctx)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if !raft.IsEmptySnap(snapshot) {
		s.mu.Unlock()
		return ErrSnapshotUnsupported
	}
	if !hasPersistedState(state) {
		if err := s.cfg.validateBootstrapPeers(); err != nil {
			s.mu.Unlock()
			return err
		}
	}

	rawNode, err := raft.NewRawNode(&raft.Config{
		ID:                       s.cfg.NodeID,
		ElectionTick:             electionTick,
		HeartbeatTick:            heartbeatTick,
		Storage:                  storageView.memory,
		Applied:                  state.AppliedIndex,
		MaxSizePerMsg:            math.MaxUint64,
		MaxCommittedSizePerReady: math.MaxUint64,
		MaxInflightMsgs:          256,
		CheckQuorum:              true,
		PreVote:                  true,
		Logger:                   newEtcdRaftLogger(s.cfg.Logger, s.cfg.NodeID),
	})
	if err != nil {
		s.mu.Unlock()
		return err
	}

	s.transport = newTransport(s.cfg.Pool)
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.stepCh = make(chan raftpb.Message, 1024)
	s.proposeCh = make(chan proposalRequest)
	s.err = nil

	s.cfg.Server.Handle(msgTypeControllerRaft, s.handleMessage)
	s.started = true
	s.mu.Unlock()

	if !hasPersistedState(state) && s.cfg.AllowBootstrap && isSmallestPeer(s.cfg.NodeID, s.cfg.Peers) {
		if err := rawNodeBootstrap(s.cfg.NodeID, rawNode, raftPeers(normalizePeers(s.cfg.Peers))); err != nil {
			s.cleanupFailedStart()
			return err
		}
		s.updateLeader(rawNode)
	}

	go s.run(rawNode, storageView, s.stopCh, s.doneCh, s.transport)
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	stopCh := s.stopCh
	doneCh := s.doneCh
	transport := s.transport
	s.started = false
	s.mu.Unlock()

	close(stopCh)
	<-doneCh
	if transport != nil {
		transport.Close()
	}

	s.mu.Lock()
	s.stopCh = nil
	s.doneCh = nil
	s.transport = nil
	s.mu.Unlock()

	s.leaderID.Store(0)
	return nil
}

func (s *Service) LeaderID() uint64 {
	return s.leaderID.Load()
}

func (s *Service) Propose(ctx context.Context, cmd slotcontroller.Command) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrNotStarted
	}
	proposeCh := s.proposeCh
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	req := proposalRequest{
		ctx:  ctx,
		cmd:  cmd,
		resp: make(chan error, 1),
	}

	select {
	case proposeCh <- req:
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

func (s *Service) cleanupFailedStart() {
	s.mu.Lock()
	transport := s.transport
	s.started = false
	s.stopCh = nil
	s.doneCh = nil
	s.stepCh = nil
	s.proposeCh = nil
	s.transport = nil
	s.err = nil
	s.mu.Unlock()

	if transport != nil {
		transport.Close()
	}
	s.leaderID.Store(0)
}

func (s *Service) currentError() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return s.err
	}
	return ErrStopped
}

func (s *Service) handleMessage(body []byte) {
	var msg raftpb.Message
	if err := msg.Unmarshal(body); err != nil {
		return
	}

	s.mu.Lock()
	started := s.started
	stepCh := s.stepCh
	stopCh := s.stopCh
	s.mu.Unlock()
	if !started {
		return
	}

	select {
	case stepCh <- msg:
	case <-stopCh:
	default:
		// Drop on backpressure; raft will retry.
	}
}

func (s *Service) run(rawNode *raft.RawNode, storageView *storageAdapter, stopCh <-chan struct{}, doneCh chan struct{}, transport *raftTransport) {
	defer close(doneCh)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	pendingQueue := make([]trackedProposal, 0, 8)
	pendingByIndex := make(map[uint64]trackedProposal)

	processReady := func() error {
		for rawNode.HasReady() {
			ready := rawNode.Ready()
			if err := storageView.persistReady(context.Background(), ready); err != nil {
				failTracked(pendingQueue, pendingByIndex, err)
				return err
			}

			for _, entry := range ready.Entries {
				if entry.Type != raftpb.EntryNormal || len(entry.Data) == 0 {
					continue
				}
				if len(pendingQueue) == 0 {
					continue
				}
				next := pendingQueue[0]
				pendingQueue = pendingQueue[1:]
				pendingByIndex[entry.Index] = next
			}

			if len(ready.Messages) > 0 {
				if err := transport.Send(context.Background(), ready.Messages); err != nil {
					failTracked(pendingQueue, pendingByIndex, err)
					return err
				}
			}

			lastApplied := uint64(0)
			if !raft.IsEmptySnap(ready.Snapshot) {
				failTracked(pendingQueue, pendingByIndex, ErrSnapshotUnsupported)
				return ErrSnapshotUnsupported
			}

			for _, entry := range ready.CommittedEntries {
				lastApplied = entry.Index
				switch entry.Type {
				case raftpb.EntryNormal:
					if len(entry.Data) == 0 {
						continue
					}
					cmd, err := decodeCommand(entry.Data)
					if err != nil {
						failTracked(pendingQueue, pendingByIndex, err)
						return err
					}
					err = s.cfg.StateMachine.Apply(context.Background(), cmd)
					if err == nil && s.cfg.OnCommittedCommand != nil {
						s.cfg.OnCommittedCommand(cmd)
					}
					if tracked, ok := pendingByIndex[entry.Index]; ok {
						tracked.resp <- err
						delete(pendingByIndex, entry.Index)
					}
					if err != nil {
						failTracked(pendingQueue, pendingByIndex, err)
						return err
					}
				case raftpb.EntryConfChange:
					var cc raftpb.ConfChange
					if err := cc.Unmarshal(entry.Data); err != nil {
						failTracked(pendingQueue, pendingByIndex, err)
						return err
					}
					rawNode.ApplyConfChange(cc)
				}
			}

			if lastApplied > 0 {
				if err := storageView.storage.MarkApplied(context.Background(), lastApplied); err != nil {
					failTracked(pendingQueue, pendingByIndex, err)
					return err
				}
			}

			rawNode.Advance(ready)
			s.updateLeader(rawNode)
			failInflightProposalsOnLeaderLoss(rawNode.Status().RaftState, &pendingQueue, pendingByIndex)
		}
		return nil
	}

	s.updateLeader(rawNode)
	for {
		if err := processReady(); err != nil {
			s.setError(err)
			return
		}

		select {
		case <-stopCh:
			failTracked(pendingQueue, pendingByIndex, ErrStopped)
			return
		case <-ticker.C:
			rawNode.Tick()
			s.updateLeader(rawNode)
			failInflightProposalsOnLeaderLoss(rawNode.Status().RaftState, &pendingQueue, pendingByIndex)
		case msg := <-s.stepCh:
			if err := rawNode.Step(msg); err != nil && !errors.Is(err, raft.ErrStepLocalMsg) {
				s.setError(err)
				failTracked(pendingQueue, pendingByIndex, err)
				return
			}
			s.updateLeader(rawNode)
			failInflightProposalsOnLeaderLoss(rawNode.Status().RaftState, &pendingQueue, pendingByIndex)
		case req := <-s.proposeCh:
			if err := req.ctx.Err(); err != nil {
				req.resp <- err
				continue
			}
			if rawNode.Status().RaftState != raft.StateLeader {
				req.resp <- ErrNotLeader
				continue
			}
			data, err := encodeCommand(req.cmd)
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

func (s *Service) setError(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *Service) updateLeader(rawNode *raft.RawNode) {
	newLeader := rawNode.Status().Lead
	oldLeader := s.leaderID.Swap(newLeader)
	if oldLeader != newLeader && s.cfg.OnLeaderChange != nil {
		s.cfg.OnLeaderChange(oldLeader, newLeader)
	}
}

func newStorageAdapter(storage multiraft.Storage) *storageAdapter {
	return &storageAdapter{storage: storage}
}

func (s *storageAdapter) load(ctx context.Context) (multiraft.BootstrapState, raftpb.Snapshot, *loadedMemoryStorage, error) {
	state, err := s.storage.InitialState(ctx)
	if err != nil {
		return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
	}

	memory := raft.NewMemoryStorage()
	snap, err := s.storage.Snapshot(ctx)
	if err != nil {
		return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
	}
	if !raft.IsEmptySnap(snap) {
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

	if !raft.IsEmptyHardState(state.HardState) {
		if err := memory.SetHardState(state.HardState); err != nil {
			return multiraft.BootstrapState{}, raftpb.Snapshot{}, nil, err
		}
	}

	loaded := newLoadedMemoryStorage(memory, state.ConfState)
	s.memory = loaded
	return state, snap, loaded, nil
}

func (s *storageAdapter) persistReady(ctx context.Context, ready raft.Ready) error {
	persist := multiraft.PersistentState{}
	needsSave := false

	if !raft.IsEmptyHardState(ready.HardState) {
		hs := ready.HardState
		persist.HardState = &hs
		needsSave = true
	}
	if len(ready.Entries) > 0 {
		persist.Entries = append([]raftpb.Entry(nil), ready.Entries...)
		needsSave = true
	}
	if !raft.IsEmptySnap(ready.Snapshot) {
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

func newLoadedMemoryStorage(memory *raft.MemoryStorage, confState raftpb.ConfState) *loadedMemoryStorage {
	return &loadedMemoryStorage{
		MemoryStorage: memory,
		confState:     cloneConfState(confState),
	}
}

func (s *loadedMemoryStorage) InitialState() (raftpb.HardState, raftpb.ConfState, error) {
	hardState, _, err := s.MemoryStorage.InitialState()
	if err != nil {
		return raftpb.HardState{}, raftpb.ConfState{}, err
	}
	return hardState, cloneConfState(s.confState), nil
}

func (s *loadedMemoryStorage) ApplySnapshot(snapshot raftpb.Snapshot) error {
	if err := s.MemoryStorage.ApplySnapshot(snapshot); err != nil {
		return err
	}
	s.confState = cloneConfState(snapshot.Metadata.ConfState)
	return nil
}

func encodeCommand(cmd slotcontroller.Command) ([]byte, error) {
	envelope := commandEnvelope{
		Kind:   cmd.Kind,
		Report: cmd.Report,
		Op:     cmd.Op,
	}
	if cmd.Advance != nil {
		envelope.Advance = &taskAdvanceEnvelope{
			SlotID:  cmd.Advance.SlotID,
			Attempt: cmd.Advance.Attempt,
			Now:     cmd.Advance.Now,
		}
		if cmd.Advance.Err != nil {
			envelope.Advance.Err = cmd.Advance.Err.Error()
		}
	}
	if cmd.Assignment != nil {
		envelope.Assignment = &slotcontrollerAssignmentEnvelope{
			SlotID:         cmd.Assignment.SlotID,
			DesiredPeers:   append([]uint64(nil), cmd.Assignment.DesiredPeers...),
			ConfigEpoch:    cmd.Assignment.ConfigEpoch,
			BalanceVersion: cmd.Assignment.BalanceVersion,
		}
	}
	if cmd.Task != nil {
		envelope.Task = &slotcontrollerReconcileTaskEnvelope{
			SlotID:     cmd.Task.SlotID,
			Kind:       cmd.Task.Kind,
			Step:       cmd.Task.Step,
			SourceNode: cmd.Task.SourceNode,
			TargetNode: cmd.Task.TargetNode,
			Attempt:    cmd.Task.Attempt,
			NextRunAt:  cmd.Task.NextRunAt,
			Status:     cmd.Task.Status,
			LastError:  cmd.Task.LastError,
		}
	}
	if cmd.Migration != nil {
		envelope.Migration = &slotcontroller.MigrationRequest{
			HashSlot: cmd.Migration.HashSlot,
			Source:   cmd.Migration.Source,
			Target:   cmd.Migration.Target,
			Phase:    cmd.Migration.Phase,
		}
	}
	if cmd.AddSlot != nil {
		envelope.AddSlot = &slotcontroller.AddSlotRequest{
			NewSlotID: cmd.AddSlot.NewSlotID,
			Peers:     append([]uint64(nil), cmd.AddSlot.Peers...),
		}
	}
	if cmd.RemoveSlot != nil {
		envelope.RemoveSlot = &slotcontroller.RemoveSlotRequest{
			SlotID: cmd.RemoveSlot.SlotID,
		}
	}
	if cmd.NodeStatusUpdate != nil {
		envelope.NodeStatusUpdate = cloneNodeStatusUpdate(cmd.NodeStatusUpdate)
	}
	return json.Marshal(envelope)
}

func decodeCommand(data []byte) (slotcontroller.Command, error) {
	var envelope commandEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return slotcontroller.Command{}, err
	}
	cmd := slotcontroller.Command{
		Kind:   envelope.Kind,
		Report: envelope.Report,
		Op:     envelope.Op,
	}
	if envelope.Advance != nil {
		advance := &slotcontroller.TaskAdvance{
			SlotID:  envelope.Advance.SlotID,
			Attempt: envelope.Advance.Attempt,
			Now:     envelope.Advance.Now,
		}
		if envelope.Advance.Err != "" {
			advance.Err = errors.New(envelope.Advance.Err)
		}
		cmd.Advance = advance
	}
	if envelope.Assignment != nil {
		cmd.Assignment = &controllermeta.SlotAssignment{
			SlotID:         envelope.Assignment.SlotID,
			DesiredPeers:   append([]uint64(nil), envelope.Assignment.DesiredPeers...),
			ConfigEpoch:    envelope.Assignment.ConfigEpoch,
			BalanceVersion: envelope.Assignment.BalanceVersion,
		}
	}
	if envelope.Task != nil {
		cmd.Task = &controllermeta.ReconcileTask{
			SlotID:     envelope.Task.SlotID,
			Kind:       envelope.Task.Kind,
			Step:       envelope.Task.Step,
			SourceNode: envelope.Task.SourceNode,
			TargetNode: envelope.Task.TargetNode,
			Attempt:    envelope.Task.Attempt,
			NextRunAt:  envelope.Task.NextRunAt,
			Status:     controllermeta.TaskStatus(envelope.Task.Status),
			LastError:  envelope.Task.LastError,
		}
	}
	if envelope.Migration != nil {
		cmd.Migration = &slotcontroller.MigrationRequest{
			HashSlot: envelope.Migration.HashSlot,
			Source:   envelope.Migration.Source,
			Target:   envelope.Migration.Target,
			Phase:    envelope.Migration.Phase,
		}
	}
	if envelope.AddSlot != nil {
		cmd.AddSlot = &slotcontroller.AddSlotRequest{
			NewSlotID: envelope.AddSlot.NewSlotID,
			Peers:     append([]uint64(nil), envelope.AddSlot.Peers...),
		}
	}
	if envelope.RemoveSlot != nil {
		cmd.RemoveSlot = &slotcontroller.RemoveSlotRequest{
			SlotID: envelope.RemoveSlot.SlotID,
		}
	}
	if envelope.NodeStatusUpdate != nil {
		cmd.NodeStatusUpdate = cloneNodeStatusUpdate(envelope.NodeStatusUpdate)
	}
	return cmd, nil
}

func raftPeers(peers []Peer) []raft.Peer {
	out := make([]raft.Peer, 0, len(peers))
	for _, peer := range peers {
		out = append(out, raft.Peer{ID: peer.NodeID})
	}
	return out
}

func isSmallestPeer(nodeID uint64, peers []Peer) bool {
	if len(peers) == 0 {
		return false
	}
	minID := peers[0].NodeID
	for _, peer := range peers[1:] {
		if peer.NodeID < minID {
			minID = peer.NodeID
		}
	}
	return nodeID == minID
}

func hasPersistedState(state multiraft.BootstrapState) bool {
	if !raft.IsEmptyHardState(state.HardState) {
		return true
	}
	if state.AppliedIndex > 0 {
		return true
	}
	return len(state.ConfState.Voters) > 0 || len(state.ConfState.Learners) > 0
}

func cloneConfState(state raftpb.ConfState) raftpb.ConfState {
	return raftpb.ConfState{
		Voters:         append([]uint64(nil), state.Voters...),
		Learners:       append([]uint64(nil), state.Learners...),
		VotersOutgoing: append([]uint64(nil), state.VotersOutgoing...),
		LearnersNext:   append([]uint64(nil), state.LearnersNext...),
		AutoLeave:      state.AutoLeave,
	}
}

func cloneNodeStatusUpdate(update *slotcontroller.NodeStatusUpdate) *slotcontroller.NodeStatusUpdate {
	if update == nil {
		return nil
	}
	cloned := &slotcontroller.NodeStatusUpdate{
		Transitions: make([]slotcontroller.NodeStatusTransition, 0, len(update.Transitions)),
	}
	for _, transition := range update.Transitions {
		next := slotcontroller.NodeStatusTransition{
			NodeID:         transition.NodeID,
			NewStatus:      transition.NewStatus,
			EvaluatedAt:    transition.EvaluatedAt,
			Addr:           transition.Addr,
			CapacityWeight: transition.CapacityWeight,
		}
		if transition.ExpectedStatus != nil {
			expected := *transition.ExpectedStatus
			next.ExpectedStatus = &expected
		}
		cloned.Transitions = append(cloned.Transitions, next)
	}
	return cloned
}

func failTracked(queue []trackedProposal, byIndex map[uint64]trackedProposal, err error) {
	for _, tracked := range queue {
		tracked.resp <- err
	}
	for index, tracked := range byIndex {
		tracked.resp <- err
		delete(byIndex, index)
	}
}

func failInflightProposalsOnLeaderLoss(state raft.StateType, queue *[]trackedProposal, byIndex map[uint64]trackedProposal) {
	if state == raft.StateLeader {
		return
	}
	if len(*queue) == 0 && len(byIndex) == 0 {
		return
	}
	failTracked(*queue, byIndex, ErrNotLeader)
	*queue = (*queue)[:0]
}
