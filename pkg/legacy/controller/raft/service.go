package raft

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/plane"
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

	rawNodeBootstrapMu sync.RWMutex
	rawNodeBootstrap   = func(_ uint64, rawNode *raft.RawNode, peers []raft.Peer) error {
		return rawNode.Bootstrap(peers)
	}

	// compactControllerLogHook is a narrow test hook for injecting local compaction failures; it must remain nil in production.
	compactControllerLogHookMu sync.RWMutex
	compactControllerLogHook   func() error

	// lifecycleWaitHook is a narrow test hook for observing Start waits; it must remain nil in production.
	lifecycleWaitHookMu sync.RWMutex
	lifecycleWaitHook   func(reason string)
)

const (
	// LogCompactionSkippedDisabled reports that local Controller Raft compaction is disabled.
	LogCompactionSkippedDisabled = "disabled"
	// LogCompactionSkippedNoAppliedIndex reports that no local applied entry can be snapshotted yet.
	LogCompactionSkippedNoAppliedIndex = "no_applied_index"
	// LogCompactionSkippedUpToDate reports that the latest local snapshot already covers applied entries.
	LogCompactionSkippedUpToDate = "up_to_date"
)

// LogCompactionResult describes one manual Controller Raft log compaction attempt.
type LogCompactionResult struct {
	// NodeID is the local Controller Raft node ID that handled the attempt.
	NodeID uint64
	// AppliedIndex is the applied index used as the manual compaction target.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether a new snapshot was created and local entries were compacted.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
}

type Service struct {
	cfg Config

	mu       sync.Mutex
	started  bool
	starting bool
	stopping bool
	// startDone closes when the in-flight startup attempt has published a result.
	startDone chan struct{}
	// startErr is the result observed by concurrent Start callers waiting on startDone.
	startErr error
	// stopRequested marks a Stop call that arrived before startup finished.
	stopRequested bool
	// stopDone closes after an in-flight Stop has released run-loop resources.
	stopDone chan struct{}
	stopCh   chan struct{}
	doneCh   chan struct{}
	err      error

	statusMu sync.RWMutex
	status   Status

	leaderID atomic.Uint64

	stepCh    chan raftpb.Message
	proposeCh chan proposalRequest
	compactCh chan logCompactionRequest

	transport *raftTransport
	assembler *controllerRaftSnapshotAssembler
}

type proposalRequest struct {
	ctx  context.Context
	cmd  slotcontroller.Command
	resp chan error
}

type trackedProposal struct {
	resp chan error
}

type logCompactionRequest struct {
	ctx  context.Context
	resp chan logCompactionResponse
}

type logCompactionResponse struct {
	result LogCompactionResult
	err    error
}

// runStartupState carries durable startup indexes into the Raft run loop.
type runStartupState struct {
	// State is the persisted Raft bootstrap state loaded before RawNode creation.
	State multiraft.BootstrapState
	// Snapshot is the persisted Raft snapshot, if one exists at startup.
	Snapshot raftpb.Snapshot
}

type storageAdapter struct {
	storage multiraft.Storage
	memory  *loadedMemoryStorage
}

type controllerLogCompactor struct {
	cfg             LogCompactionConfig
	lastCheck       time.Time
	lastSnapshotIdx uint64
	now             func() time.Time
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
	SlotID                      uint32    `json:"slot_id"`
	DesiredPeers                []uint64  `json:"desired_peers,omitempty"`
	ConfigEpoch                 uint64    `json:"config_epoch,omitempty"`
	BalanceVersion              uint64    `json:"balance_version,omitempty"`
	PreferredLeader             uint64    `json:"preferred_leader,omitempty"`
	LeaderTransferCooldownUntil time.Time `json:"leader_transfer_cooldown_until,omitempty"`
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
	return &Service{
		cfg:       cfg,
		status:    initialStatus(cfg),
		assembler: newControllerRaftSnapshotAssembler(defaultControllerRaftSnapshotChunkTTL, time.Now),
	}
}

func (s *Service) Start(ctx context.Context) error {
	for {
		s.mu.Lock()
		if s.started {
			if s.stopping {
				stopDone := s.stopDone
				s.mu.Unlock()
				if stopDone != nil {
					notifyLifecycleWait("stop")
					<-stopDone
				}
				continue
			}
			s.mu.Unlock()
			return nil
		}
		if s.stopping {
			stopDone := s.stopDone
			s.mu.Unlock()
			if stopDone != nil {
				notifyLifecycleWait("stop")
				<-stopDone
			}
			continue
		}
		if !s.starting {
			s.starting = true
			s.stopRequested = false
			s.startErr = nil
			s.startDone = make(chan struct{})
			s.mu.Unlock()
			break
		}
		startDone := s.startDone
		s.mu.Unlock()

		if startDone != nil {
			notifyLifecycleWait("start")
			<-startDone
		}
		s.mu.Lock()
		err := s.startErr
		s.mu.Unlock()
		return err
	}

	if err := s.cfg.validateCore(); err != nil {
		s.finishStart(err)
		return err
	}
	s.cfg.LogCompaction = NormalizeLogCompactionConfig(s.cfg.LogCompaction)
	if err := ValidateLogCompactionConfig(s.cfg.LogCompaction); err != nil {
		s.finishStart(err)
		return err
	}
	s.recordCompactionConfig(s.cfg.LogCompaction)

	store := s.cfg.LogDB.ForController()
	storageView := newStorageAdapter(store)
	state, snapshot, _, err := storageView.load(ctx)
	if err != nil {
		s.finishStart(err)
		return err
	}
	if !raft.IsEmptySnap(snapshot) {
		if err := s.cfg.StateMachine.Restore(ctx, append([]byte(nil), snapshot.Data...)); err != nil {
			s.recordRestoreFailure(snapshot, err)
			s.finishStart(err)
			return err
		}
		s.recordStartupRestoreSuccess(snapshot)
	}
	if !hasPersistedState(state) {
		if err := s.cfg.validateBootstrapPeers(); err != nil {
			s.finishStart(err)
			return err
		}
	}

	transport := newTransport(s.cfg.Pool, s.cfg.NodeID)
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	stepCh := make(chan raftpb.Message, 1024)
	proposeCh := make(chan proposalRequest)
	compactCh := make(chan logCompactionRequest)
	initCh := make(chan error, 1)
	startGate := make(chan struct{})

	go s.run(storageView, runStartupState{State: state, Snapshot: snapshot}, stopCh, doneCh, stepCh, proposeCh, compactCh, transport, initCh, startGate)
	if err := <-initCh; err != nil {
		s.finishStart(err)
		<-doneCh
		transport.Close()
		s.leaderID.Store(0)
		s.recordStoppedStatus()
		return err
	}

	s.mu.Lock()
	if s.stopRequested {
		s.finishStartLocked(ErrStopped)
		s.mu.Unlock()
		close(stopCh)
		<-doneCh
		transport.Close()
		s.leaderID.Store(0)
		s.recordStoppedStatus()
		return ErrStopped
	}
	s.transport = transport
	s.stopCh = stopCh
	s.doneCh = doneCh
	s.stepCh = stepCh
	s.proposeCh = proposeCh
	s.compactCh = compactCh
	s.err = nil

	s.assembler = newControllerRaftSnapshotAssembler(defaultControllerRaftSnapshotChunkTTL, time.Now)
	s.cfg.Server.Handle(msgTypeControllerRaft, s.handleMessage)
	s.cfg.Server.Handle(msgTypeControllerRaftSnapshotChunk, s.handleSnapshotChunkMessage)
	s.started = true
	s.finishStartLocked(nil)
	close(startGate)
	s.mu.Unlock()

	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if s.starting {
		s.stopRequested = true
		startDone := s.startDone
		s.mu.Unlock()
		if startDone != nil {
			<-startDone
		}
		s.leaderID.Store(0)
		s.recordStoppedStatus()
		return nil
	}
	if s.stopping {
		stopDone := s.stopDone
		s.mu.Unlock()
		if stopDone != nil {
			<-stopDone
		}
		return nil
	}
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	stopCh := s.stopCh
	doneCh := s.doneCh
	transport := s.transport
	s.stopping = true
	s.stopDone = make(chan struct{})
	s.mu.Unlock()

	close(stopCh)
	<-doneCh
	if transport != nil {
		transport.Close()
	}

	s.leaderID.Store(0)
	s.recordStoppedStatus()

	s.mu.Lock()
	s.started = false
	s.stopping = false
	s.stopCh = nil
	s.doneCh = nil
	s.stepCh = nil
	s.proposeCh = nil
	s.compactCh = nil
	s.transport = nil
	s.finishStopLocked()
	s.mu.Unlock()

	return nil
}

func (s *Service) LeaderID() uint64 {
	return s.leaderID.Load()
}

// Status returns a read-only snapshot of local Controller Raft status.
func (s *Service) Status() Status {
	if s == nil {
		return Status{Role: RoleUnknown}
	}
	s.statusMu.RLock()
	st := cloneStatus(s.status)
	s.statusMu.RUnlock()
	if st.NodeID == 0 && s.cfg.NodeID != 0 {
		return initialStatus(s.cfg)
	}
	if st.Role == "" {
		st.Role = RoleUnknown
	}
	return st
}

func (s *Service) Propose(ctx context.Context, cmd slotcontroller.Command) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrNotStarted
	}
	if s.stopping {
		s.mu.Unlock()
		return ErrStopped
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

// CompactLog manually snapshots the local Controller metadata and compacts applied Raft log entries.
func (s *Service) CompactLog(ctx context.Context) (LogCompactionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return LogCompactionResult{}, ErrNotStarted
	}
	if s.stopping {
		s.mu.Unlock()
		return LogCompactionResult{}, ErrStopped
	}
	compactCh := s.compactCh
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	req := logCompactionRequest{
		ctx:  ctx,
		resp: make(chan logCompactionResponse, 1),
	}

	select {
	case compactCh <- req:
	case <-ctx.Done():
		return LogCompactionResult{}, ctx.Err()
	case <-doneCh:
		return LogCompactionResult{}, s.currentError()
	case <-stopCh:
		return LogCompactionResult{}, ErrStopped
	}

	select {
	case resp := <-req.resp:
		return resp.result, resp.err
	case <-ctx.Done():
		return LogCompactionResult{}, ctx.Err()
	case <-doneCh:
		return LogCompactionResult{}, s.currentError()
	case <-stopCh:
		return LogCompactionResult{}, ErrStopped
	}
}

func (s *Service) finishStart(err error) {
	s.mu.Lock()
	s.finishStartLocked(err)
	s.mu.Unlock()
}

func (s *Service) finishStartLocked(err error) {
	s.startErr = err
	s.starting = false
	s.stopRequested = false
	startDone := s.startDone
	s.startDone = nil
	if startDone != nil {
		close(startDone)
	}
}

func (s *Service) finishStopLocked() {
	stopDone := s.stopDone
	s.stopDone = nil
	if stopDone != nil {
		close(stopDone)
	}
}

func (s *Service) currentError() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return s.err
	}
	return ErrStopped
}

func (s *Service) recordCompactionConfig(cfg LogCompactionConfig) {
	s.statusMu.Lock()
	s.status.NodeID = s.cfg.NodeID
	if s.status.Role == "" {
		s.status.Role = RoleUnknown
	}
	s.status.Compaction.Enabled = cfg.Enabled
	s.status.Compaction.TriggerEntries = cfg.TriggerEntries
	s.status.Compaction.CheckInterval = cfg.CheckInterval
	s.statusMu.Unlock()
}

func (s *Service) recordRaftStatus(rawNode *raft.RawNode) {
	if rawNode == nil {
		return
	}
	s.recordRaftStatusSnapshot(rawNode.Status())
}

func (s *Service) recordRaftStatusSnapshot(st raft.Status) {
	cached := Status{
		NodeID:       s.cfg.NodeID,
		Role:         raftRoleName(st.RaftState),
		LeaderID:     st.Lead,
		Term:         st.Term,
		CommitIndex:  st.Commit,
		AppliedIndex: st.Applied,
		Peers:        peerProgressFromRaft(s.cfg.NodeID, st.Progress),
	}
	s.statusMu.Lock()
	cached.Compaction = s.status.Compaction
	cached.Restore = s.status.Restore
	s.status = cached
	s.statusMu.Unlock()
}

func (s *Service) recordStoppedStatus() {
	s.statusMu.Lock()
	compaction := s.status.Compaction
	restore := s.status.Restore
	s.status = Status{
		NodeID:     s.cfg.NodeID,
		Role:       RoleUnknown,
		Compaction: compaction,
		Restore:    restore,
	}
	s.statusMu.Unlock()
}

func (s *Service) recordStartupRestoreSuccess(snap raftpb.Snapshot) {
	s.recordRestoreSuccess(snap)
}

func (s *Service) recordReadyRestoreSuccess(snap raftpb.Snapshot) {
	s.recordRestoreSuccess(snap)
}

func (s *Service) recordRestoreSuccess(snap raftpb.Snapshot) {
	if raft.IsEmptySnap(snap) {
		return
	}
	s.statusMu.Lock()
	s.status.NodeID = s.cfg.NodeID
	if s.status.Role == "" {
		s.status.Role = RoleUnknown
	}
	s.status.Restore.LastSnapshotIndex = snap.Metadata.Index
	s.status.Restore.LastSnapshotTerm = snap.Metadata.Term
	s.status.Restore.LastRestoredAt = time.Now()
	s.status.Restore.LastError = ""
	s.status.Restore.LastErrorAt = time.Time{}
	s.status.Restore.Failed = false
	s.statusMu.Unlock()
}

func (s *Service) recordRestoreFailure(snap raftpb.Snapshot, err error) {
	if err == nil {
		return
	}
	s.statusMu.Lock()
	s.status.NodeID = s.cfg.NodeID
	if s.status.Role == "" {
		s.status.Role = RoleUnknown
	}
	s.status.Restore.LastSnapshotIndex = snap.Metadata.Index
	s.status.Restore.LastSnapshotTerm = snap.Metadata.Term
	s.status.Restore.LastError = err.Error()
	s.status.Restore.LastErrorAt = time.Now()
	s.status.Restore.Failed = true
	s.statusMu.Unlock()
}

func (s *Service) recordCompactionFailure(err error, at time.Time) {
	if err == nil {
		return
	}
	s.statusMu.Lock()
	s.status.NodeID = s.cfg.NodeID
	if s.status.Role == "" {
		s.status.Role = RoleUnknown
	}
	s.status.Compaction.LastCheckAt = at
	s.status.Compaction.LastError = err.Error()
	s.status.Compaction.LastErrorAt = at
	s.status.Compaction.Degraded = true
	s.statusMu.Unlock()
}

func (s *Service) recordCompactionSuccess(index uint64, at time.Time) {
	s.statusMu.Lock()
	s.status.NodeID = s.cfg.NodeID
	if s.status.Role == "" {
		s.status.Role = RoleUnknown
	}
	s.status.Compaction.LastSnapshotIndex = index
	s.status.Compaction.LastSnapshotAt = at
	s.status.Compaction.LastCheckAt = at
	s.status.Compaction.LastError = ""
	s.status.Compaction.LastErrorAt = time.Time{}
	s.status.Compaction.Degraded = false
	s.statusMu.Unlock()
}

func (s *Service) handleMessage(body []byte) {
	var msg raftpb.Message
	if err := msg.Unmarshal(body); err != nil {
		return
	}
	s.stepMessage(msg)
}

func (s *Service) handleSnapshotChunkMessage(body []byte) {
	chunk, err := decodeControllerRaftSnapshotChunkBody(body)
	if err != nil {
		return
	}
	if s.assembler == nil {
		s.assembler = newControllerRaftSnapshotAssembler(defaultControllerRaftSnapshotChunkTTL, time.Now)
	}
	msg, ok, err := s.assembler.add(chunk)
	if err != nil || !ok {
		return
	}
	s.stepMessage(msg)
}

func (s *Service) stepMessage(msg raftpb.Message) {
	if s.dropInboundRaftMessage(msg) {
		return
	}

	s.mu.Lock()
	started := s.started && !s.stopping
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

func (s *Service) newRawNode(storageView *storageAdapter, startup runStartupState) (*raft.RawNode, error) {
	appliedIndex := startup.State.AppliedIndex
	if !raft.IsEmptySnap(startup.Snapshot) {
		appliedIndex = startup.Snapshot.Metadata.Index
	}
	return raft.NewRawNode(&raft.Config{
		ID:                       s.cfg.NodeID,
		ElectionTick:             electionTick,
		HeartbeatTick:            heartbeatTick,
		Storage:                  storageView.memory,
		Applied:                  appliedIndex,
		MaxSizePerMsg:            math.MaxUint64,
		MaxCommittedSizePerReady: math.MaxUint64,
		MaxInflightMsgs:          256,
		CheckQuorum:              true,
		PreVote:                  true,
		Logger:                   newEtcdRaftLogger(s.cfg.Logger, s.cfg.NodeID),
	})
}

func notifyRunInit(initCh chan<- error, err error) {
	if initCh == nil {
		return
	}
	initCh <- err
}

func notifyLifecycleWait(reason string) {
	lifecycleWaitHookMu.RLock()
	hook := lifecycleWaitHook
	lifecycleWaitHookMu.RUnlock()
	if hook != nil {
		hook(reason)
	}
}

func setLifecycleWaitHookForTest(h func(reason string)) {
	lifecycleWaitHookMu.Lock()
	defer lifecycleWaitHookMu.Unlock()
	lifecycleWaitHook = h
}

func (s *Service) run(storageView *storageAdapter, startup runStartupState, stopCh <-chan struct{}, doneCh chan struct{}, stepCh <-chan raftpb.Message, proposeCh <-chan proposalRequest, compactCh <-chan logCompactionRequest, transport *raftTransport, initCh chan<- error, startGate <-chan struct{}) {
	defer close(doneCh)

	rawNode, err := s.newRawNode(storageView, startup)
	if err != nil {
		notifyRunInit(initCh, err)
		return
	}
	if !hasPersistedState(startup.State) && s.cfg.AllowBootstrap && isSmallestPeer(s.cfg.NodeID, s.cfg.Peers) {
		if err := currentRawNodeBootstrap()(s.cfg.NodeID, rawNode, raftPeers(normalizePeers(s.cfg.Peers))); err != nil {
			notifyRunInit(initCh, err)
			return
		}
	}
	s.updateLeader(rawNode)
	notifyRunInit(initCh, nil)
	select {
	case <-startGate:
	case <-stopCh:
		return
	}

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	pendingQueue := make([]trackedProposal, 0, 8)
	pendingByIndex := make(map[uint64]trackedProposal)
	latestConfState := cloneConfState(startup.State.ConfState)
	if !raft.IsEmptySnap(startup.Snapshot) {
		latestConfState = cloneConfState(startup.Snapshot.Metadata.ConfState)
	}
	compactor := newControllerLogCompactor(s.cfg.LogCompaction, startup.Snapshot.Metadata.Index)

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

			lastApplied, err := s.applyReadyState(context.Background(), rawNode, ready, storageView, &latestConfState, pendingByIndex)
			if err != nil {
				failTracked(pendingQueue, pendingByIndex, err)
				return err
			}

			rawNode.Advance(ready)
			s.updateLeader(rawNode)
			failInflightProposalsOnLeaderLoss(rawNode.Status().RaftState, &pendingQueue, pendingByIndex)
			if compactor.shouldCompact(lastApplied) {
				if err := s.compactControllerLog(context.Background(), storageView, lastApplied, latestConfState); err != nil {
					s.recordCompactionFailure(err, time.Now())
					s.logCompactionWarning(err, lastApplied)
				} else {
					s.recordCompactionSuccess(lastApplied, time.Now())
					compactor.recordSnapshot(lastApplied)
				}
			}
		}
		return nil
	}

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
		case msg := <-stepCh:
			if err := rawNode.Step(msg); err != nil && !errors.Is(err, raft.ErrStepLocalMsg) {
				s.setError(err)
				failTracked(pendingQueue, pendingByIndex, err)
				return
			}
			s.updateLeader(rawNode)
			failInflightProposalsOnLeaderLoss(rawNode.Status().RaftState, &pendingQueue, pendingByIndex)
		case req := <-proposeCh:
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
		case req := <-compactCh:
			result, err := s.compactControllerLogManually(req.ctx, storageView, rawNode.Status().Applied, latestConfState)
			if err != nil {
				s.recordCompactionFailure(err, time.Now())
				s.logCompactionWarning(err, result.AppliedIndex)
			} else if result.Compacted {
				s.recordCompactionSuccess(result.AfterSnapshotIndex, time.Now())
				compactor.recordSnapshot(result.AfterSnapshotIndex)
			}
			req.resp <- logCompactionResponse{result: result, err: err}
		}
	}
}

// applyReadyState restores Ready snapshots, applies committed entries, and marks the durable applied index.
func (s *Service) applyReadyState(ctx context.Context, rawNode *raft.RawNode, ready raft.Ready, storageView *storageAdapter, latestConfState *raftpb.ConfState, pendingByIndex map[uint64]trackedProposal) (uint64, error) {
	lastApplied := uint64(0)
	if !raft.IsEmptySnap(ready.Snapshot) {
		applied, err := s.restoreReadySnapshot(ctx, ready.Snapshot, latestConfState)
		if err != nil {
			return 0, err
		}
		lastApplied = applied
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
				return 0, err
			}
			err = s.cfg.StateMachine.Apply(ctx, cmd)
			if err == nil && s.cfg.OnCommittedCommand != nil {
				s.cfg.OnCommittedCommand(cmd)
			}
			if tracked, ok := pendingByIndex[entry.Index]; ok {
				tracked.resp <- err
				delete(pendingByIndex, entry.Index)
			}
			if err != nil {
				return 0, err
			}
		case raftpb.EntryConfChange:
			var cc raftpb.ConfChange
			if err := cc.Unmarshal(entry.Data); err != nil {
				return 0, err
			}
			latest := rawNode.ApplyConfChange(cc)
			*latestConfState = cloneConfState(*latest)
		case raftpb.EntryConfChangeV2:
			var cc raftpb.ConfChangeV2
			if err := cc.Unmarshal(entry.Data); err != nil {
				return 0, err
			}
			latest := rawNode.ApplyConfChange(cc)
			*latestConfState = cloneConfState(*latest)
		}
	}

	if lastApplied == 0 {
		return 0, nil
	}
	if err := storageView.storage.MarkApplied(ctx, lastApplied); err != nil {
		return 0, err
	}
	return lastApplied, nil
}

// restoreReadySnapshot restores Controller metadata from a non-empty Ready snapshot.
func (s *Service) restoreReadySnapshot(ctx context.Context, snap raftpb.Snapshot, latestConfState *raftpb.ConfState) (uint64, error) {
	if raft.IsEmptySnap(snap) {
		return 0, nil
	}
	if err := s.cfg.StateMachine.Restore(ctx, append([]byte(nil), snap.Data...)); err != nil {
		s.recordRestoreFailure(snap, err)
		return 0, err
	}
	*latestConfState = cloneConfState(snap.Metadata.ConfState)
	s.recordReadyRestoreSuccess(snap)
	return snap.Metadata.Index, nil
}

func newControllerLogCompactor(cfg LogCompactionConfig, lastSnapshotIdx uint64) *controllerLogCompactor {
	return &controllerLogCompactor{
		cfg:             cfg,
		lastSnapshotIdx: lastSnapshotIdx,
		now:             time.Now,
	}
}

func (c *controllerLogCompactor) shouldCompact(applied uint64) bool {
	if c == nil || !c.cfg.Enabled || applied == 0 {
		return false
	}
	if applied < c.lastSnapshotIdx || applied-c.lastSnapshotIdx < c.cfg.TriggerEntries {
		return false
	}
	now := c.now()
	if !c.lastCheck.IsZero() && now.Sub(c.lastCheck) < c.cfg.CheckInterval {
		return false
	}
	c.lastCheck = now
	return true
}

func (c *controllerLogCompactor) recordSnapshot(index uint64) {
	if c == nil {
		return
	}
	c.lastSnapshotIdx = index
}

func (s *Service) compactControllerLogManually(ctx context.Context, storageView *storageAdapter, applied uint64, confState raftpb.ConfState) (LogCompactionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := LogCompactionResult{
		NodeID:       s.cfg.NodeID,
		AppliedIndex: applied,
	}
	snapshotIndex, _, err := storageSnapshotBoundary(ctx, storageView.storage)
	if err != nil {
		return result, err
	}
	if snapshotIndex > 0 {
		result.BeforeSnapshotIndex = snapshotIndex
		result.AfterSnapshotIndex = snapshotIndex
	}
	if !s.cfg.LogCompaction.Enabled {
		result.SkippedReason = LogCompactionSkippedDisabled
		return result, nil
	}
	if applied == 0 {
		result.SkippedReason = LogCompactionSkippedNoAppliedIndex
		return result, nil
	}
	if applied <= result.BeforeSnapshotIndex {
		result.SkippedReason = LogCompactionSkippedUpToDate
		return result, nil
	}
	if err := s.compactControllerLog(ctx, storageView, applied, confState); err != nil {
		return result, err
	}
	result.AfterSnapshotIndex = applied
	result.Compacted = true
	return result, nil
}

func (s *Service) compactControllerLog(ctx context.Context, storageView *storageAdapter, applied uint64, confState raftpb.ConfState) error {
	if hook := currentCompactControllerLogHook(); hook != nil {
		if err := hook(); err != nil {
			return err
		}
	}
	data, err := s.cfg.StateMachine.Snapshot(ctx)
	if err != nil {
		return err
	}
	term, err := storageView.memory.Term(applied)
	if err != nil {
		return err
	}
	snap := raftpb.Snapshot{
		Data: data,
		Metadata: raftpb.SnapshotMetadata{
			Index:     applied,
			Term:      term,
			ConfState: cloneConfState(confState),
		},
	}
	if err := storageView.storage.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil {
		return err
	}
	if _, err := storageView.memory.CreateSnapshot(applied, &snap.Metadata.ConfState, snap.Data); err != nil && !errors.Is(err, raft.ErrSnapOutOfDate) {
		return err
	}
	storageView.memory.confState = cloneConfState(snap.Metadata.ConfState)
	if err := storageView.memory.Compact(applied); err != nil && !errors.Is(err, raft.ErrCompacted) {
		return err
	}
	return nil
}

func storageSnapshotBoundary(ctx context.Context, storage multiraft.Storage) (uint64, uint64, error) {
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return 0, 0, err
	}
	if first <= 1 {
		return 0, 0, nil
	}
	snapshotIndex := first - 1
	snapshotTerm, err := storage.Term(ctx, snapshotIndex)
	if err != nil {
		return 0, 0, err
	}
	return snapshotIndex, snapshotTerm, nil
}

func currentCompactControllerLogHook() func() error {
	compactControllerLogHookMu.RLock()
	defer compactControllerLogHookMu.RUnlock()
	return compactControllerLogHook
}

func setCompactControllerLogHookForTest(h func() error) {
	compactControllerLogHookMu.Lock()
	defer compactControllerLogHookMu.Unlock()
	compactControllerLogHook = h
}

func currentRawNodeBootstrap() func(uint64, *raft.RawNode, []raft.Peer) error {
	rawNodeBootstrapMu.RLock()
	defer rawNodeBootstrapMu.RUnlock()
	return rawNodeBootstrap
}

func setRawNodeBootstrapForTest(h func(uint64, *raft.RawNode, []raft.Peer) error) {
	rawNodeBootstrapMu.Lock()
	defer rawNodeBootstrapMu.Unlock()
	rawNodeBootstrap = h
}

func (s *Service) logCompactionWarning(err error, applied uint64) {
	if s.cfg.Logger == nil {
		return
	}
	s.cfg.Logger.Named("raft").Warn("controller raft log compaction failed",
		wklog.RaftScope("controller"),
		wklog.NodeID(s.cfg.NodeID),
		wklog.Uint64("appliedIndex", applied),
		wklog.Error(err),
		wklog.Event("controller.raft.log_compaction.failed"),
	)
}

func (s *Service) setError(err error) {
	var transport *raftTransport
	s.mu.Lock()
	s.err = err
	if !s.stopping {
		transport = s.transport
		s.started = false
		s.stopCh = nil
		s.doneCh = nil
		s.stepCh = nil
		s.proposeCh = nil
		s.compactCh = nil
		s.transport = nil
	}
	s.mu.Unlock()
	if transport != nil {
		transport.Close()
	}
	s.leaderID.Store(0)
	s.recordStoppedStatus()
}

func (s *Service) updateLeader(rawNode *raft.RawNode) {
	status := rawNode.Status()
	s.recordRaftStatusSnapshot(status)
	newLeader := status.Lead
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

	loadedConfState := state.ConfState
	if !raft.IsEmptySnap(snap) {
		loadedConfState = snap.Metadata.ConfState
	}
	loaded := newLoadedMemoryStorage(memory, loadedConfState)
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
	return encodeCommandBinary(cmd)
}

func commandEnvelopeFromCommand(cmd slotcontroller.Command) commandEnvelope {
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
			NewSlotID:       cmd.AddSlot.NewSlotID,
			Peers:           append([]uint64(nil), cmd.AddSlot.Peers...),
			PreferredLeader: cmd.AddSlot.PreferredLeader,
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
	return envelope
}

func decodeCommand(data []byte) (slotcontroller.Command, error) {
	if hasCommandEnvelopeBinaryMagic(data) {
		return decodeCommandBinary(data)
	}
	envelope, err := decodeCommandEnvelopeLegacyJSON(data)
	if err != nil {
		return slotcontroller.Command{}, err
	}
	return commandFromEnvelope(envelope), nil
}

func commandFromEnvelope(envelope commandEnvelope) slotcontroller.Command {
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
			NewSlotID:       envelope.AddSlot.NewSlotID,
			Peers:           append([]uint64(nil), envelope.AddSlot.Peers...),
			PreferredLeader: envelope.AddSlot.PreferredLeader,
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
	return cmd
}

func assignmentEnvelopeFromMeta(assignment *controllermeta.SlotAssignment) *slotcontrollerAssignmentEnvelope {
	if assignment == nil {
		return nil
	}
	return &slotcontrollerAssignmentEnvelope{
		SlotID:                      assignment.SlotID,
		DesiredPeers:                append([]uint64(nil), assignment.DesiredPeers...),
		ConfigEpoch:                 assignment.ConfigEpoch,
		BalanceVersion:              assignment.BalanceVersion,
		PreferredLeader:             assignment.PreferredLeader,
		LeaderTransferCooldownUntil: assignment.LeaderTransferCooldownUntil,
	}
}

func assignmentEnvelopeToMeta(envelope *slotcontrollerAssignmentEnvelope) *controllermeta.SlotAssignment {
	if envelope == nil {
		return nil
	}
	return &controllermeta.SlotAssignment{
		SlotID:                      envelope.SlotID,
		DesiredPeers:                append([]uint64(nil), envelope.DesiredPeers...),
		ConfigEpoch:                 envelope.ConfigEpoch,
		BalanceVersion:              envelope.BalanceVersion,
		PreferredLeader:             envelope.PreferredLeader,
		LeaderTransferCooldownUntil: envelope.LeaderTransferCooldownUntil,
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
