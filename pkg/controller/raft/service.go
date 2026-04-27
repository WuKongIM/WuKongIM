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
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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
	Kind             slotcontroller.CommandKind              `json:"kind"`
	Report           *slotcontroller.AgentReport             `json:"report,omitempty"`
	Op               *slotcontroller.OperatorRequest         `json:"op,omitempty"`
	Advance          *taskAdvanceEnvelope                    `json:"advance,omitempty"`
	Assignment       *slotcontrollerAssignmentEnvelope       `json:"assignment,omitempty"`
	Task             *slotcontrollerReconcileTaskEnvelope    `json:"task,omitempty"`
	Migration        *slotcontroller.MigrationRequest        `json:"migration,omitempty"`
	AddSlot          *slotcontroller.AddSlotRequest          `json:"add_slot,omitempty"`
	RemoveSlot       *slotcontroller.RemoveSlotRequest       `json:"remove_slot,omitempty"`
	NodeStatusUpdate *slotcontroller.NodeStatusUpdate        `json:"node_status_update,omitempty"`
	NodeJoin         *slotcontroller.NodeJoinRequest         `json:"node_join,omitempty"`
	NodeJoinActivate *slotcontroller.NodeJoinActivateRequest `json:"node_join_activate,omitempty"`
	NodeOnboarding   *nodeOnboardingJobUpdateEnvelope        `json:"node_onboarding,omitempty"`
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

type nodeOnboardingJobUpdateEnvelope struct {
	Job            *nodeOnboardingJobEnvelope           `json:"job,omitempty"`
	ExpectedStatus *controllermeta.OnboardingJobStatus  `json:"expected_status,omitempty"`
	Assignment     *slotcontrollerAssignmentEnvelope    `json:"assignment,omitempty"`
	Task           *slotcontrollerReconcileTaskEnvelope `json:"task,omitempty"`
}

type nodeOnboardingJobEnvelope struct {
	JobID            string                               `json:"job_id"`
	TargetNodeID     uint64                               `json:"target_node_id"`
	RetryOfJobID     string                               `json:"retry_of_job_id,omitempty"`
	Status           controllermeta.OnboardingJobStatus   `json:"status"`
	CreatedAt        time.Time                            `json:"created_at"`
	UpdatedAt        time.Time                            `json:"updated_at"`
	StartedAt        time.Time                            `json:"started_at,omitempty"`
	CompletedAt      time.Time                            `json:"completed_at,omitempty"`
	PlanVersion      uint32                               `json:"plan_version"`
	PlanFingerprint  string                               `json:"plan_fingerprint"`
	Plan             nodeOnboardingPlanEnvelope           `json:"plan"`
	Moves            []nodeOnboardingMoveEnvelope         `json:"moves,omitempty"`
	CurrentMoveIndex int                                  `json:"current_move_index"`
	ResultCounts     nodeOnboardingResultCountsEnvelope   `json:"result_counts"`
	CurrentTask      *slotcontrollerReconcileTaskEnvelope `json:"current_task,omitempty"`
	LastError        string                               `json:"last_error,omitempty"`
}

type nodeOnboardingPlanEnvelope struct {
	TargetNodeID   uint64                                `json:"target_node_id"`
	Summary        nodeOnboardingPlanSummaryEnvelope     `json:"summary"`
	Moves          []nodeOnboardingPlanMoveEnvelope      `json:"moves,omitempty"`
	BlockedReasons []nodeOnboardingBlockedReasonEnvelope `json:"blocked_reasons,omitempty"`
}

type nodeOnboardingPlanSummaryEnvelope struct {
	CurrentTargetSlotCount   int `json:"current_target_slot_count"`
	PlannedTargetSlotCount   int `json:"planned_target_slot_count"`
	CurrentTargetLeaderCount int `json:"current_target_leader_count"`
	PlannedLeaderGain        int `json:"planned_leader_gain"`
}

type nodeOnboardingPlanMoveEnvelope struct {
	SlotID                 uint32   `json:"slot_id"`
	SourceNodeID           uint64   `json:"source_node_id"`
	TargetNodeID           uint64   `json:"target_node_id"`
	Reason                 string   `json:"reason,omitempty"`
	DesiredPeersBefore     []uint64 `json:"desired_peers_before,omitempty"`
	DesiredPeersAfter      []uint64 `json:"desired_peers_after,omitempty"`
	CurrentLeaderID        uint64   `json:"current_leader_id,omitempty"`
	LeaderTransferRequired bool     `json:"leader_transfer_required,omitempty"`
}

type nodeOnboardingBlockedReasonEnvelope struct {
	Code    string `json:"code"`
	Scope   string `json:"scope"`
	SlotID  uint32 `json:"slot_id,omitempty"`
	NodeID  uint64 `json:"node_id,omitempty"`
	Message string `json:"message,omitempty"`
}

type nodeOnboardingMoveEnvelope struct {
	SlotID                 uint32                              `json:"slot_id"`
	SourceNodeID           uint64                              `json:"source_node_id"`
	TargetNodeID           uint64                              `json:"target_node_id"`
	Status                 controllermeta.OnboardingMoveStatus `json:"status"`
	TaskKind               controllermeta.TaskKind             `json:"task_kind,omitempty"`
	TaskSlotID             uint32                              `json:"task_slot_id,omitempty"`
	StartedAt              time.Time                           `json:"started_at,omitempty"`
	CompletedAt            time.Time                           `json:"completed_at,omitempty"`
	LastError              string                              `json:"last_error,omitempty"`
	DesiredPeersBefore     []uint64                            `json:"desired_peers_before,omitempty"`
	DesiredPeersAfter      []uint64                            `json:"desired_peers_after,omitempty"`
	LeaderBefore           uint64                              `json:"leader_before,omitempty"`
	LeaderAfter            uint64                              `json:"leader_after,omitempty"`
	LeaderTransferRequired bool                                `json:"leader_transfer_required,omitempty"`
}

type nodeOnboardingResultCountsEnvelope struct {
	Pending   int `json:"pending,omitempty"`
	Running   int `json:"running,omitempty"`
	Completed int `json:"completed,omitempty"`
	Failed    int `json:"failed,omitempty"`
	Skipped   int `json:"skipped,omitempty"`
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

	s.transport = newTransport(s.cfg.Pool, s.cfg.NodeID)
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
	if s.dropInboundRaftMessage(msg) {
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

// dropInboundRaftMessage rejects stale or looped network frames before RawNode.Step.
func (s *Service) dropInboundRaftMessage(msg raftpb.Message) bool {
	localNodeID := s.cfg.NodeID
	if localNodeID == 0 {
		return false
	}

	reason := ""
	switch {
	case msg.To != localNodeID:
		reason = "target_mismatch"
	case msg.From == localNodeID:
		reason = "loopback"
	case msg.To == msg.From:
		reason = "self_addressed"
	}
	if reason == "" {
		return false
	}

	// Network-delivered controller raft traffic must always be addressed to this
	// node and come from a different peer. Dropping stale or looped frames here
	// prevents RawNode from panicking on self-addressed heartbeat responses.
	if s.cfg.Logger != nil {
		s.cfg.Logger.Named("raft").Warn("drop misrouted controller raft message",
			wklog.RaftScope("controller"),
			wklog.NodeID(localNodeID),
			wklog.TargetNodeID(msg.To),
			wklog.PeerNodeID(msg.From),
			wklog.String("raftMsgType", msg.Type.String()),
			wklog.Reason(reason),
			wklog.Event("controller.raft.message.drop"),
		)
	}
	return true
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
	envelope.Assignment = assignmentEnvelopeFromMeta(cmd.Assignment)
	envelope.Task = taskEnvelopeFromMeta(cmd.Task)
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
	if cmd.NodeJoin != nil {
		envelope.NodeJoin = cloneNodeJoinRequest(cmd.NodeJoin)
	}
	if cmd.NodeJoinActivate != nil {
		envelope.NodeJoinActivate = cloneNodeJoinActivateRequest(cmd.NodeJoinActivate)
	}
	if cmd.NodeOnboarding != nil {
		envelope.NodeOnboarding = nodeOnboardingUpdateEnvelopeFromPlane(cmd.NodeOnboarding)
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
	cmd.Assignment = assignmentEnvelopeToMeta(envelope.Assignment)
	cmd.Task = taskEnvelopeToMeta(envelope.Task)
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
	if envelope.NodeJoin != nil {
		cmd.NodeJoin = cloneNodeJoinRequest(envelope.NodeJoin)
	}
	if envelope.NodeJoinActivate != nil {
		cmd.NodeJoinActivate = cloneNodeJoinActivateRequest(envelope.NodeJoinActivate)
	}
	if envelope.NodeOnboarding != nil {
		cmd.NodeOnboarding = nodeOnboardingUpdateEnvelopeToPlane(envelope.NodeOnboarding)
	}
	return cmd, nil
}

func assignmentEnvelopeFromMeta(assignment *controllermeta.SlotAssignment) *slotcontrollerAssignmentEnvelope {
	if assignment == nil {
		return nil
	}
	return &slotcontrollerAssignmentEnvelope{
		SlotID:         assignment.SlotID,
		DesiredPeers:   append([]uint64(nil), assignment.DesiredPeers...),
		ConfigEpoch:    assignment.ConfigEpoch,
		BalanceVersion: assignment.BalanceVersion,
	}
}

func assignmentEnvelopeToMeta(envelope *slotcontrollerAssignmentEnvelope) *controllermeta.SlotAssignment {
	if envelope == nil {
		return nil
	}
	return &controllermeta.SlotAssignment{
		SlotID:         envelope.SlotID,
		DesiredPeers:   append([]uint64(nil), envelope.DesiredPeers...),
		ConfigEpoch:    envelope.ConfigEpoch,
		BalanceVersion: envelope.BalanceVersion,
	}
}

func taskEnvelopeFromMeta(task *controllermeta.ReconcileTask) *slotcontrollerReconcileTaskEnvelope {
	if task == nil {
		return nil
	}
	return &slotcontrollerReconcileTaskEnvelope{
		SlotID:     task.SlotID,
		Kind:       task.Kind,
		Step:       task.Step,
		SourceNode: task.SourceNode,
		TargetNode: task.TargetNode,
		Attempt:    task.Attempt,
		NextRunAt:  task.NextRunAt,
		Status:     task.Status,
		LastError:  task.LastError,
	}
}

func taskEnvelopeToMeta(envelope *slotcontrollerReconcileTaskEnvelope) *controllermeta.ReconcileTask {
	if envelope == nil {
		return nil
	}
	return &controllermeta.ReconcileTask{
		SlotID:     envelope.SlotID,
		Kind:       envelope.Kind,
		Step:       envelope.Step,
		SourceNode: envelope.SourceNode,
		TargetNode: envelope.TargetNode,
		Attempt:    envelope.Attempt,
		NextRunAt:  envelope.NextRunAt,
		Status:     controllermeta.TaskStatus(envelope.Status),
		LastError:  envelope.LastError,
	}
}

func nodeOnboardingUpdateEnvelopeFromPlane(update *slotcontroller.NodeOnboardingJobUpdate) *nodeOnboardingJobUpdateEnvelope {
	if update == nil {
		return nil
	}
	out := &nodeOnboardingJobUpdateEnvelope{
		Job:        nodeOnboardingJobEnvelopeFromMeta(update.Job),
		Assignment: assignmentEnvelopeFromMeta(update.Assignment),
		Task:       taskEnvelopeFromMeta(update.Task),
	}
	if update.ExpectedStatus != nil {
		expected := *update.ExpectedStatus
		out.ExpectedStatus = &expected
	}
	return out
}

func nodeOnboardingUpdateEnvelopeToPlane(envelope *nodeOnboardingJobUpdateEnvelope) *slotcontroller.NodeOnboardingJobUpdate {
	if envelope == nil {
		return nil
	}
	out := &slotcontroller.NodeOnboardingJobUpdate{
		Job:        nodeOnboardingJobEnvelopeToMeta(envelope.Job),
		Assignment: assignmentEnvelopeToMeta(envelope.Assignment),
		Task:       taskEnvelopeToMeta(envelope.Task),
	}
	if envelope.ExpectedStatus != nil {
		expected := *envelope.ExpectedStatus
		out.ExpectedStatus = &expected
	}
	return out
}

func nodeOnboardingJobEnvelopeFromMeta(job *controllermeta.NodeOnboardingJob) *nodeOnboardingJobEnvelope {
	if job == nil {
		return nil
	}
	moves := make([]nodeOnboardingMoveEnvelope, 0, len(job.Moves))
	for _, move := range job.Moves {
		moves = append(moves, nodeOnboardingMoveEnvelopeFromMeta(move))
	}
	return &nodeOnboardingJobEnvelope{
		JobID:            job.JobID,
		TargetNodeID:     job.TargetNodeID,
		RetryOfJobID:     job.RetryOfJobID,
		Status:           job.Status,
		CreatedAt:        job.CreatedAt,
		UpdatedAt:        job.UpdatedAt,
		StartedAt:        job.StartedAt,
		CompletedAt:      job.CompletedAt,
		PlanVersion:      job.PlanVersion,
		PlanFingerprint:  job.PlanFingerprint,
		Plan:             nodeOnboardingPlanEnvelopeFromMeta(job.Plan),
		Moves:            moves,
		CurrentMoveIndex: job.CurrentMoveIndex,
		ResultCounts:     nodeOnboardingResultCountsEnvelopeFromMeta(job.ResultCounts),
		CurrentTask:      taskEnvelopeFromMeta(job.CurrentTask),
		LastError:        job.LastError,
	}
}

func nodeOnboardingJobEnvelopeToMeta(envelope *nodeOnboardingJobEnvelope) *controllermeta.NodeOnboardingJob {
	if envelope == nil {
		return nil
	}
	moves := make([]controllermeta.NodeOnboardingMove, 0, len(envelope.Moves))
	for _, move := range envelope.Moves {
		moves = append(moves, nodeOnboardingMoveEnvelopeToMeta(move))
	}
	return &controllermeta.NodeOnboardingJob{
		JobID:            envelope.JobID,
		TargetNodeID:     envelope.TargetNodeID,
		RetryOfJobID:     envelope.RetryOfJobID,
		Status:           envelope.Status,
		CreatedAt:        envelope.CreatedAt,
		UpdatedAt:        envelope.UpdatedAt,
		StartedAt:        envelope.StartedAt,
		CompletedAt:      envelope.CompletedAt,
		PlanVersion:      envelope.PlanVersion,
		PlanFingerprint:  envelope.PlanFingerprint,
		Plan:             nodeOnboardingPlanEnvelopeToMeta(envelope.Plan),
		Moves:            moves,
		CurrentMoveIndex: envelope.CurrentMoveIndex,
		ResultCounts:     nodeOnboardingResultCountsEnvelopeToMeta(envelope.ResultCounts),
		CurrentTask:      taskEnvelopeToMeta(envelope.CurrentTask),
		LastError:        envelope.LastError,
	}
}

func nodeOnboardingPlanEnvelopeFromMeta(plan controllermeta.NodeOnboardingPlan) nodeOnboardingPlanEnvelope {
	moves := make([]nodeOnboardingPlanMoveEnvelope, 0, len(plan.Moves))
	for _, move := range plan.Moves {
		moves = append(moves, nodeOnboardingPlanMoveEnvelopeFromMeta(move))
	}
	reasons := make([]nodeOnboardingBlockedReasonEnvelope, 0, len(plan.BlockedReasons))
	for _, reason := range plan.BlockedReasons {
		reasons = append(reasons, nodeOnboardingBlockedReasonEnvelopeFromMeta(reason))
	}
	return nodeOnboardingPlanEnvelope{
		TargetNodeID:   plan.TargetNodeID,
		Summary:        nodeOnboardingPlanSummaryEnvelopeFromMeta(plan.Summary),
		Moves:          moves,
		BlockedReasons: reasons,
	}
}

func nodeOnboardingPlanEnvelopeToMeta(envelope nodeOnboardingPlanEnvelope) controllermeta.NodeOnboardingPlan {
	moves := make([]controllermeta.NodeOnboardingPlanMove, 0, len(envelope.Moves))
	for _, move := range envelope.Moves {
		moves = append(moves, nodeOnboardingPlanMoveEnvelopeToMeta(move))
	}
	reasons := make([]controllermeta.NodeOnboardingBlockedReason, 0, len(envelope.BlockedReasons))
	for _, reason := range envelope.BlockedReasons {
		reasons = append(reasons, nodeOnboardingBlockedReasonEnvelopeToMeta(reason))
	}
	return controllermeta.NodeOnboardingPlan{
		TargetNodeID:   envelope.TargetNodeID,
		Summary:        nodeOnboardingPlanSummaryEnvelopeToMeta(envelope.Summary),
		Moves:          moves,
		BlockedReasons: reasons,
	}
}

func nodeOnboardingPlanSummaryEnvelopeFromMeta(summary controllermeta.NodeOnboardingPlanSummary) nodeOnboardingPlanSummaryEnvelope {
	return nodeOnboardingPlanSummaryEnvelope{
		CurrentTargetSlotCount:   summary.CurrentTargetSlotCount,
		PlannedTargetSlotCount:   summary.PlannedTargetSlotCount,
		CurrentTargetLeaderCount: summary.CurrentTargetLeaderCount,
		PlannedLeaderGain:        summary.PlannedLeaderGain,
	}
}

func nodeOnboardingPlanSummaryEnvelopeToMeta(envelope nodeOnboardingPlanSummaryEnvelope) controllermeta.NodeOnboardingPlanSummary {
	return controllermeta.NodeOnboardingPlanSummary{
		CurrentTargetSlotCount:   envelope.CurrentTargetSlotCount,
		PlannedTargetSlotCount:   envelope.PlannedTargetSlotCount,
		CurrentTargetLeaderCount: envelope.CurrentTargetLeaderCount,
		PlannedLeaderGain:        envelope.PlannedLeaderGain,
	}
}

func nodeOnboardingPlanMoveEnvelopeFromMeta(move controllermeta.NodeOnboardingPlanMove) nodeOnboardingPlanMoveEnvelope {
	return nodeOnboardingPlanMoveEnvelope{
		SlotID:                 move.SlotID,
		SourceNodeID:           move.SourceNodeID,
		TargetNodeID:           move.TargetNodeID,
		Reason:                 move.Reason,
		DesiredPeersBefore:     append([]uint64(nil), move.DesiredPeersBefore...),
		DesiredPeersAfter:      append([]uint64(nil), move.DesiredPeersAfter...),
		CurrentLeaderID:        move.CurrentLeaderID,
		LeaderTransferRequired: move.LeaderTransferRequired,
	}
}

func nodeOnboardingPlanMoveEnvelopeToMeta(envelope nodeOnboardingPlanMoveEnvelope) controllermeta.NodeOnboardingPlanMove {
	return controllermeta.NodeOnboardingPlanMove{
		SlotID:                 envelope.SlotID,
		SourceNodeID:           envelope.SourceNodeID,
		TargetNodeID:           envelope.TargetNodeID,
		Reason:                 envelope.Reason,
		DesiredPeersBefore:     append([]uint64(nil), envelope.DesiredPeersBefore...),
		DesiredPeersAfter:      append([]uint64(nil), envelope.DesiredPeersAfter...),
		CurrentLeaderID:        envelope.CurrentLeaderID,
		LeaderTransferRequired: envelope.LeaderTransferRequired,
	}
}

func nodeOnboardingBlockedReasonEnvelopeFromMeta(reason controllermeta.NodeOnboardingBlockedReason) nodeOnboardingBlockedReasonEnvelope {
	return nodeOnboardingBlockedReasonEnvelope{
		Code:    reason.Code,
		Scope:   reason.Scope,
		SlotID:  reason.SlotID,
		NodeID:  reason.NodeID,
		Message: reason.Message,
	}
}

func nodeOnboardingBlockedReasonEnvelopeToMeta(envelope nodeOnboardingBlockedReasonEnvelope) controllermeta.NodeOnboardingBlockedReason {
	return controllermeta.NodeOnboardingBlockedReason{
		Code:    envelope.Code,
		Scope:   envelope.Scope,
		SlotID:  envelope.SlotID,
		NodeID:  envelope.NodeID,
		Message: envelope.Message,
	}
}

func nodeOnboardingMoveEnvelopeFromMeta(move controllermeta.NodeOnboardingMove) nodeOnboardingMoveEnvelope {
	return nodeOnboardingMoveEnvelope{
		SlotID:                 move.SlotID,
		SourceNodeID:           move.SourceNodeID,
		TargetNodeID:           move.TargetNodeID,
		Status:                 move.Status,
		TaskKind:               move.TaskKind,
		TaskSlotID:             move.TaskSlotID,
		StartedAt:              move.StartedAt,
		CompletedAt:            move.CompletedAt,
		LastError:              move.LastError,
		DesiredPeersBefore:     append([]uint64(nil), move.DesiredPeersBefore...),
		DesiredPeersAfter:      append([]uint64(nil), move.DesiredPeersAfter...),
		LeaderBefore:           move.LeaderBefore,
		LeaderAfter:            move.LeaderAfter,
		LeaderTransferRequired: move.LeaderTransferRequired,
	}
}

func nodeOnboardingMoveEnvelopeToMeta(envelope nodeOnboardingMoveEnvelope) controllermeta.NodeOnboardingMove {
	return controllermeta.NodeOnboardingMove{
		SlotID:                 envelope.SlotID,
		SourceNodeID:           envelope.SourceNodeID,
		TargetNodeID:           envelope.TargetNodeID,
		Status:                 envelope.Status,
		TaskKind:               envelope.TaskKind,
		TaskSlotID:             envelope.TaskSlotID,
		StartedAt:              envelope.StartedAt,
		CompletedAt:            envelope.CompletedAt,
		LastError:              envelope.LastError,
		DesiredPeersBefore:     append([]uint64(nil), envelope.DesiredPeersBefore...),
		DesiredPeersAfter:      append([]uint64(nil), envelope.DesiredPeersAfter...),
		LeaderBefore:           envelope.LeaderBefore,
		LeaderAfter:            envelope.LeaderAfter,
		LeaderTransferRequired: envelope.LeaderTransferRequired,
	}
}

func nodeOnboardingResultCountsEnvelopeFromMeta(counts controllermeta.OnboardingResultCounts) nodeOnboardingResultCountsEnvelope {
	return nodeOnboardingResultCountsEnvelope{
		Pending:   counts.Pending,
		Running:   counts.Running,
		Completed: counts.Completed,
		Failed:    counts.Failed,
		Skipped:   counts.Skipped,
	}
}

func nodeOnboardingResultCountsEnvelopeToMeta(envelope nodeOnboardingResultCountsEnvelope) controllermeta.OnboardingResultCounts {
	return controllermeta.OnboardingResultCounts{
		Pending:   envelope.Pending,
		Running:   envelope.Running,
		Completed: envelope.Completed,
		Failed:    envelope.Failed,
		Skipped:   envelope.Skipped,
	}
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

func cloneNodeJoinRequest(req *slotcontroller.NodeJoinRequest) *slotcontroller.NodeJoinRequest {
	if req == nil {
		return nil
	}
	return &slotcontroller.NodeJoinRequest{
		NodeID:         req.NodeID,
		Name:           req.Name,
		Addr:           req.Addr,
		CapacityWeight: req.CapacityWeight,
		JoinedAt:       req.JoinedAt,
	}
}

func cloneNodeJoinActivateRequest(req *slotcontroller.NodeJoinActivateRequest) *slotcontroller.NodeJoinActivateRequest {
	if req == nil {
		return nil
	}
	return &slotcontroller.NodeJoinActivateRequest{
		NodeID:      req.NodeID,
		ActivatedAt: req.ActivatedAt,
	}
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
