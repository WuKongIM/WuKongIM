package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"go.etcd.io/raft/v3/raftpb"
)

// RuntimeRole declares how the local control runtime participates in ControllerV2.
type RuntimeRole string

const (
	// RuntimeRoleVoter runs ControllerV2 Raft and serves authoritative state.
	RuntimeRoleVoter RuntimeRole = "voter"
	// RuntimeRoleMirror mirrors ControllerV2 state from Controller voters.
	RuntimeRoleMirror RuntimeRole = "mirror"
)

// RuntimeVoter identifies one ControllerV2 voter endpoint.
type RuntimeVoter struct {
	// NodeID is the stable non-zero node identity of the Controller voter.
	NodeID uint64
	// Addr is the cluster RPC address used to reach this Controller voter.
	Addr string
}

// RuntimeConfig wires a ControllerV2-backed control runtime.
type RuntimeConfig struct {
	// NodeID is the local node identity.
	NodeID uint64
	// Addr is the local cluster RPC address.
	Addr string
	// StateDir stores ControllerV2 state and Raft files.
	StateDir string
	// ClusterID is the stable cluster identity.
	ClusterID string
	// Role declares voter or mirror behavior.
	Role RuntimeRole
	// Voters lists ControllerV2 Raft voters.
	Voters []RuntimeVoter
	// AllowBootstrap permits this node to initialize a new ControllerV2 Raft log.
	AllowBootstrap bool
	// InitialSlotCount is the number of physical slots created during bootstrap.
	InitialSlotCount uint32
	// HashSlotCount is the number of logical hash slots in the initial table.
	HashSlotCount uint16
	// ReplicaCount is the desired replica count for each physical slot.
	ReplicaCount uint16
	// TickInterval controls ControllerV2 Raft ticking.
	TickInterval time.Duration
	// RaftTransport sends ControllerV2 Raft messages.
	RaftTransport cv2.Transport
	// RaftObserver receives local ControllerV2 Raft queue metrics.
	RaftObserver cv2.RaftObserver
	// TaskTransitionObserver receives task edges after applied metadata is persisted.
	TaskTransitionObserver cv2.TaskTransitionObserver
	// SyncClient mirrors ControllerV2 state for non-voter nodes.
	SyncClient *cv2.SyncClient
	// SyncPeers resolves ControllerV2 state sync endpoints for mirror nodes.
	SyncPeers cv2.PeerPicker
	// TaskClient forwards task writes and creation intents to the current Controller leader.
	TaskClient *TaskClient
	// ControlWriteClient forwards generic control writes to the current Controller leader.
	ControlWriteClient *ControlWriteClient
	// HealthReportTTL bounds how long adapted node health reports remain fresh.
	HealthReportTTL time.Duration
	// Now returns timestamps used for ControllerV2 commands.
	Now func() time.Time
}

// Runtime adapts the root ControllerV2 runtime facade to control.Controller.
type Runtime struct {
	cfg         RuntimeConfig
	backend     *cv2.Runtime
	taskClient  *TaskClient
	writeClient *ControlWriteClient

	mu       sync.RWMutex
	snapshot Snapshot
	watch    chan SnapshotEvent

	watchCancel context.CancelFunc
	watchWG     sync.WaitGroup
}

// NewRuntime creates a ControllerV2-backed control runtime.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	backend, err := cv2.NewRuntime(cv2.RuntimeConfig{
		NodeID:                 cfg.NodeID,
		Addr:                   cfg.Addr,
		StateDir:               cfg.StateDir,
		ClusterID:              cfg.ClusterID,
		Role:                   cv2.RuntimeRole(cfg.Role),
		Voters:                 runtimeFacadeVoters(cfg.Voters),
		AllowBootstrap:         cfg.AllowBootstrap,
		InitialSlotCount:       cfg.InitialSlotCount,
		HashSlotCount:          cfg.HashSlotCount,
		ReplicaCount:           cfg.ReplicaCount,
		TickInterval:           cfg.TickInterval,
		RaftTransport:          cfg.RaftTransport,
		RaftObserver:           cfg.RaftObserver,
		TaskTransitionObserver: cfg.TaskTransitionObserver,
		SyncClient:             cfg.SyncClient,
		SyncPeers:              cfg.SyncPeers,
		Now:                    cfg.Now,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{
		cfg:         cfg,
		backend:     backend,
		taskClient:  cfg.TaskClient,
		writeClient: cfg.ControlWriteClient,
		watch:       make(chan SnapshotEvent, 16),
	}, nil
}

// Start starts the local ControllerV2 runtime.
func (r *Runtime) Start(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return fmt.Errorf("control runtime: controllerv2 runtime is required")
	}
	if err := r.backend.Start(ctx); err != nil {
		return err
	}
	st, err := r.backend.LocalState(ctx)
	if err != nil {
		_ = r.backend.Stop(context.Background())
		return err
	}
	if !emptyControllerV2State(st) {
		if err := r.publishState(st); err != nil {
			_ = r.backend.Stop(context.Background())
			return err
		}
	}
	r.startWatchLoop()
	return nil
}

// Stop stops local ControllerV2 resources.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r.watchCancel != nil {
		r.watchCancel()
		r.watchWG.Wait()
		r.watchCancel = nil
	}
	if r == nil || r.backend == nil {
		return nil
	}
	return r.backend.Stop(ctx)
}

// LocalSnapshot returns the latest adapted ControllerV2 control snapshot.
func (r *Runtime) LocalSnapshot(ctx context.Context) (Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return Snapshot{}, err
	}
	if r == nil {
		return Snapshot{}, cv2.ErrNotStarted
	}
	if r.backend != nil {
		st, err := r.backend.LocalState(ctx)
		if err != nil && !errors.Is(err, cv2.ErrNotStarted) {
			return Snapshot{}, err
		}
		if err == nil && !emptyControllerV2State(st) {
			snap, err := SnapshotFromControllerV2WithHealthTTL(st, r.cfg.HealthReportTTL)
			if err != nil {
				return Snapshot{}, err
			}
			r.mu.Lock()
			r.snapshot = snap.Clone()
			r.mu.Unlock()
			return snap.Clone(), nil
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.Clone(), nil
}

// LeaderID returns the best-known ControllerV2 leader ID.
func (r *Runtime) LeaderID() uint64 {
	if r != nil && r.backend != nil {
		return r.backend.LeaderID()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.ControllerID
}

// ProbePropose verifies the hosted ControllerV2 proposal path when this runtime is a voter.
func (r *Runtime) ProbePropose(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	return r.backend.ProbePropose(ctx)
}

// Step applies an inbound ControllerV2 Raft message to the local Raft service.
func (r *Runtime) Step(ctx context.Context, msg raftpb.Message) error {
	if r == nil || r.backend == nil {
		return nil
	}
	return r.backend.Step(ctx, msg)
}

// GetState serves ControllerV2 state sync requests from the local voter state.
func (r *Runtime) GetState(ctx context.Context, req cv2.GetStateRequest) (cv2.GetStateResponse, error) {
	if r == nil || r.backend == nil {
		return cv2.GetStateResponse{NotReady: true}, nil
	}
	return r.backend.GetState(ctx, req)
}

// Watch returns snapshot update events.
func (r *Runtime) Watch() <-chan SnapshotEvent { return r.watch }

// ReportNode submits a bounded low-frequency node health report.
func (r *Runtime) ReportNode(ctx context.Context, report NodeReport) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	if r.canForwardControlWriteToLeader() {
		_, err := r.forwardControlWrite(ctx, ControlWriteRequest{
			Action:           ControlWriteActionReportNodeHealth,
			ReportNodeHealth: report,
		})
		return err
	}
	_, err := r.backend.ReportNodeHealth(ctx, cv2.ReportNodeHealthRequest{
		NodeID:                  report.NodeID,
		Status:                  cv2.NodeStatus(report.Status),
		RuntimeReady:            report.RuntimeReady,
		ObservedControlRevision: report.ObservedControlRevision,
		ObservedSlotRevision:    report.ObservedSlotRevision,
		ReportSeq:               report.ReportSeq,
		ErrorCode:               report.ErrorCode,
	})
	if shouldForwardControlWrite(err) {
		_, forwardErr := r.forwardControlWriteAfterError(ctx, ControlWriteRequest{
			Action:           ControlWriteActionReportNodeHealth,
			ReportNodeHealth: report,
		}, err)
		return forwardErr
	}
	return err
}

// ReportSlots is currently a best-effort no-op until ControllerV2 exposes report commands.
func (r *Runtime) ReportSlots(ctx context.Context, report SlotRuntimeReport) error {
	return ctxErr(ctx)
}

// CompleteTask submits a fenced global task completion result.
func (r *Runtime) CompleteTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	err := r.backend.CompleteTask(ctx, result)
	if shouldForwardTaskWrite(err) {
		return r.forwardTaskRequest(ctx, TaskRequest{Action: TaskActionComplete, Result: result})
	}
	return err
}

// FailTask submits a fenced global task failure result.
func (r *Runtime) FailTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	err := r.backend.FailTask(ctx, result)
	if shouldForwardTaskWrite(err) {
		return r.forwardTaskRequest(ctx, TaskRequest{Action: TaskActionFail, Result: result})
	}
	return err
}

// ReportTaskProgress submits one participant's fenced progress report.
func (r *Runtime) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	err := r.backend.ReportTaskProgress(ctx, progress)
	if shouldForwardTaskWrite(err) {
		return r.forwardTaskRequest(ctx, TaskRequest{Action: TaskActionProgress, Progress: progress})
	}
	return err
}

// AdvanceSlotReplicaMovePhase submits one fenced Slot replica move phase observation.
func (r *Runtime) AdvanceSlotReplicaMovePhase(ctx context.Context, phase SlotReplicaMovePhaseAdvance) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	err := r.backend.AdvanceSlotReplicaMovePhase(ctx, phase)
	if shouldForwardTaskWrite(err) {
		return r.forwardTaskRequest(ctx, TaskRequest{Action: TaskActionReplicaMovePhase, ReplicaMovePhase: phase})
	}
	return err
}

// CommitSlotReplicaMove submits the final fenced Slot replica move assignment commit.
func (r *Runtime) CommitSlotReplicaMove(ctx context.Context, commit SlotReplicaMoveCommit) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	err := r.backend.CommitSlotReplicaMove(ctx, commit)
	if shouldForwardTaskWrite(err) {
		return r.forwardTaskRequest(ctx, TaskRequest{Action: TaskActionReplicaMoveCommit, ReplicaMoveCommit: commit})
	}
	return err
}

// RequestSlotLeaderTransfer submits a Controller-backed Slot leader transfer intent.
func (r *Runtime) RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLeaderTransferResult{}, err
	}
	if r == nil || r.backend == nil {
		return SlotLeaderTransferResult{}, cv2.ErrNotStarted
	}
	result, err := r.backend.RequestSlotLeaderTransfer(ctx, cv2.SlotLeaderTransferRequest{
		SlotID:        req.SlotID,
		SourceNode:    req.SourceNode,
		TargetNode:    req.TargetNode,
		TargetPeers:   append([]uint64(nil), req.TargetPeers...),
		ConfigEpoch:   req.ConfigEpoch,
		StateRevision: req.StateRevision,
	})
	if shouldForwardTaskWrite(err) {
		if err := r.forwardTaskRequest(ctx, TaskRequest{
			Action:         TaskActionLeaderTransfer,
			LeaderTransfer: req,
		}); err != nil {
			return SlotLeaderTransferResult{}, err
		}
		task := leaderTransferTaskFromRequest(req)
		return SlotLeaderTransferResult{Created: true, Task: &task}, nil
	}
	if err != nil {
		return SlotLeaderTransferResult{}, err
	}
	task := leaderTransferTaskFromRequest(req)
	if result.Task != nil {
		task = ReconcileTask{
			TaskID:           result.Task.TaskID,
			SlotID:           result.Task.SlotID,
			Kind:             TaskKind(result.Task.Kind),
			Step:             TaskStep(result.Task.Step),
			SourceNode:       result.Task.SourceNode,
			TargetNode:       result.Task.TargetNode,
			TargetPeers:      append([]uint64(nil), result.Task.TargetPeers...),
			CompletionPolicy: TaskCompletionPolicy(result.Task.CompletionPolicy),
			ParticipantProgress: append([]TaskParticipantProgress(nil),
				result.Task.ParticipantProgress...),
			ConfigEpoch: result.Task.ConfigEpoch,
			Attempt:     result.Task.Attempt,
			Status:      TaskStatus(result.Task.Status),
			LastError:   result.Task.LastError,
		}
	}
	return SlotLeaderTransferResult{Created: result.Created, Task: &task}, nil
}

// RequestSlotReplicaMove submits a Controller-backed staged Slot replica move intent.
func (r *Runtime) RequestSlotReplicaMove(ctx context.Context, req SlotReplicaMoveRequest) (SlotReplicaMoveResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotReplicaMoveResult{}, err
	}
	if r == nil || r.backend == nil {
		return SlotReplicaMoveResult{}, cv2.ErrNotStarted
	}
	if r.canForwardControlWriteToLeader() {
		resp, err := r.forwardControlWrite(ctx, ControlWriteRequest{
			Action:          ControlWriteActionSlotReplicaMove,
			SlotReplicaMove: req,
		})
		if err != nil {
			return SlotReplicaMoveResult{}, err
		}
		return resp.SlotReplicaMove, nil
	}
	result, err := r.backend.RequestSlotReplicaMove(ctx, cv2.SlotReplicaMoveRequest{
		SlotID:        req.SlotID,
		SourceNode:    req.SourceNode,
		TargetNode:    req.TargetNode,
		TargetPeers:   append([]uint64(nil), req.TargetPeers...),
		ConfigEpoch:   req.ConfigEpoch,
		StateRevision: req.StateRevision,
	})
	if shouldForwardControlWrite(err) {
		resp, err := r.forwardControlWriteAfterError(ctx, ControlWriteRequest{
			Action:          ControlWriteActionSlotReplicaMove,
			SlotReplicaMove: req,
		}, err)
		if err != nil {
			return SlotReplicaMoveResult{}, err
		}
		return resp.SlotReplicaMove, nil
	}
	if err != nil {
		return SlotReplicaMoveResult{}, err
	}
	task := slotReplicaMoveTaskFromRequest(req)
	if result.Task != nil {
		task = reconcileTaskFromControllerV2(*result.Task)
	}
	return SlotReplicaMoveResult{Created: result.Created, Task: &task}, nil
}

// JoinNode submits a data-node join intent.
func (r *Runtime) JoinNode(ctx context.Context, req JoinNodeRequest) (JoinNodeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return JoinNodeResult{}, err
	}
	if r == nil || r.backend == nil {
		return JoinNodeResult{}, cv2.ErrNotStarted
	}
	if r.canForwardControlWriteToLeader() {
		resp, err := r.forwardControlWrite(ctx, ControlWriteRequest{
			Action:   ControlWriteActionJoinNode,
			JoinNode: req,
		})
		if err != nil {
			return JoinNodeResult{}, err
		}
		return resp.JoinNode, nil
	}
	result, err := r.backend.JoinNode(ctx, cv2JoinNodeRequest(req))
	if shouldForwardControlWrite(err) {
		resp, err := r.forwardControlWriteAfterError(ctx, ControlWriteRequest{
			Action:   ControlWriteActionJoinNode,
			JoinNode: req,
		}, err)
		if err != nil {
			return JoinNodeResult{}, err
		}
		return resp.JoinNode, nil
	}
	if err != nil {
		return JoinNodeResult{}, err
	}
	return joinNodeResultFromCV2(result), nil
}

// ActivateNode submits a node activation intent.
func (r *Runtime) ActivateNode(ctx context.Context, req ActivateNodeRequest) (ActivateNodeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ActivateNodeResult{}, err
	}
	if r == nil || r.backend == nil {
		return ActivateNodeResult{}, cv2.ErrNotStarted
	}
	if r.canForwardControlWriteToLeader() {
		resp, err := r.forwardControlWrite(ctx, ControlWriteRequest{
			Action:       ControlWriteActionActivateNode,
			ActivateNode: req,
		})
		if err != nil {
			return ActivateNodeResult{}, err
		}
		return resp.ActivateNode, nil
	}
	result, err := r.backend.ActivateNode(ctx, cv2ActivateNodeRequest(req))
	if shouldForwardControlWrite(err) {
		resp, err := r.forwardControlWriteAfterError(ctx, ControlWriteRequest{
			Action:       ControlWriteActionActivateNode,
			ActivateNode: req,
		}, err)
		if err != nil {
			return ActivateNodeResult{}, err
		}
		return resp.ActivateNode, nil
	}
	if err != nil {
		return ActivateNodeResult{}, err
	}
	return activateNodeResultFromCV2(result), nil
}

// MarkNodeLeaving submits a node leaving intent.
func (r *Runtime) MarkNodeLeaving(ctx context.Context, req MarkNodeLeavingRequest) (MarkNodeLeavingResult, error) {
	if err := ctxErr(ctx); err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if r == nil || r.backend == nil {
		return MarkNodeLeavingResult{}, cv2.ErrNotStarted
	}
	if r.canForwardControlWriteToLeader() {
		resp, err := r.forwardControlWrite(ctx, ControlWriteRequest{
			Action:          ControlWriteActionMarkNodeLeaving,
			MarkNodeLeaving: req,
		})
		if err != nil {
			return MarkNodeLeavingResult{}, err
		}
		return resp.MarkNodeLeaving, nil
	}
	result, err := r.backend.MarkNodeLeaving(ctx, cv2MarkNodeLeavingRequest(req))
	if shouldForwardControlWrite(err) {
		resp, err := r.forwardControlWriteAfterError(ctx, ControlWriteRequest{
			Action:          ControlWriteActionMarkNodeLeaving,
			MarkNodeLeaving: req,
		}, err)
		if err != nil {
			return MarkNodeLeavingResult{}, err
		}
		return resp.MarkNodeLeaving, nil
	}
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	return markNodeLeavingResultFromCV2(result), nil
}

// MarkNodeRemoved submits a node removed intent.
func (r *Runtime) MarkNodeRemoved(ctx context.Context, req MarkNodeRemovedRequest) (MarkNodeRemovedResult, error) {
	if err := ctxErr(ctx); err != nil {
		return MarkNodeRemovedResult{}, err
	}
	if r == nil || r.backend == nil {
		return MarkNodeRemovedResult{}, cv2.ErrNotStarted
	}
	if r.canForwardControlWriteToLeader() {
		resp, err := r.forwardControlWrite(ctx, ControlWriteRequest{
			Action:          ControlWriteActionMarkNodeRemoved,
			MarkNodeRemoved: req,
		})
		if err != nil {
			return MarkNodeRemovedResult{}, err
		}
		return resp.MarkNodeRemoved, nil
	}
	result, err := r.backend.MarkNodeRemoved(ctx, cv2MarkNodeRemovedRequest(req))
	if shouldForwardControlWrite(err) {
		resp, err := r.forwardControlWriteAfterError(ctx, ControlWriteRequest{
			Action:          ControlWriteActionMarkNodeRemoved,
			MarkNodeRemoved: req,
		}, err)
		if err != nil {
			return MarkNodeRemovedResult{}, err
		}
		return resp.MarkNodeRemoved, nil
	}
	if err != nil {
		return MarkNodeRemovedResult{}, err
	}
	return markNodeRemovedResultFromCV2(result), nil
}

func shouldForwardTaskWrite(err error) bool {
	return errors.Is(err, cv2.ErrNotLeader) || errors.Is(err, cv2.ErrNotStarted)
}

func shouldForwardControlWrite(err error) bool {
	return errors.Is(err, cv2.ErrNotLeader) || errors.Is(err, cv2.ErrNotStarted)
}

func (r *Runtime) canForwardControlWriteToLeader() bool {
	if r == nil || r.writeClient == nil {
		return false
	}
	leaderID := r.LeaderID()
	return leaderID != 0 && leaderID != r.cfg.NodeID
}

func (r *Runtime) forwardTaskRequest(ctx context.Context, req TaskRequest) error {
	if r == nil || r.taskClient == nil {
		return cv2.ErrNotLeader
	}
	leaderID := r.LeaderID()
	if leaderID == 0 || leaderID == r.cfg.NodeID {
		return cv2.ErrNotLeader
	}
	return r.taskClient.SubmitTask(ctx, leaderID, req)
}

func (r *Runtime) forwardControlWrite(ctx context.Context, req ControlWriteRequest) (ControlWriteResponse, error) {
	if r == nil || r.writeClient == nil {
		return ControlWriteResponse{}, cv2.ErrNotLeader
	}
	leaderID := r.LeaderID()
	if leaderID == 0 || leaderID == r.cfg.NodeID {
		return ControlWriteResponse{}, cv2.ErrNotLeader
	}
	return r.writeClient.Submit(ctx, leaderID, req)
}

func (r *Runtime) forwardControlWriteAfterError(ctx context.Context, req ControlWriteRequest, fallback error) (ControlWriteResponse, error) {
	if r == nil || r.writeClient == nil {
		return ControlWriteResponse{}, fallbackControlWriteError(fallback)
	}
	leaderID := r.LeaderID()
	if leaderID == 0 || leaderID == r.cfg.NodeID {
		return ControlWriteResponse{}, fallbackControlWriteError(fallback)
	}
	return r.writeClient.Submit(ctx, leaderID, req)
}

func fallbackControlWriteError(fallback error) error {
	if fallback != nil {
		return fallback
	}
	return cv2.ErrNotLeader
}

func (r *Runtime) startWatchLoop() {
	if r.watchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.watchCancel = cancel
	r.watchWG.Add(1)
	go func() {
		defer r.watchWG.Done()
		watch := r.backend.Watch()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watch:
				if !ok {
					return
				}
				_ = r.publishState(event.State)
			}
		}
	}()
}

func (r *Runtime) publishState(st cv2.ClusterState) error {
	snap, err := SnapshotFromControllerV2WithHealthTTL(st, r.cfg.HealthReportTTL)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.snapshot = snap.Clone()
	r.mu.Unlock()
	select {
	case r.watch <- SnapshotEvent{Snapshot: snap.Clone()}:
	default:
	}
	return nil
}

func runtimeFacadeVoters(voters []RuntimeVoter) []cv2.Voter {
	out := make([]cv2.Voter, 0, len(voters))
	for _, voter := range voters {
		out = append(out, cv2.Voter{NodeID: voter.NodeID, Addr: voter.Addr})
	}
	return out
}

func emptyControllerV2State(st cv2.ClusterState) bool {
	return st.SchemaVersion == 0 &&
		st.ClusterID == "" &&
		st.Revision == 0 &&
		st.AppliedRaftIndex == 0 &&
		len(st.Controllers) == 0 &&
		len(st.Nodes) == 0 &&
		len(st.NodeHealthReports) == 0 &&
		len(st.Slots) == 0 &&
		st.HashSlots.SlotCount == 0 &&
		len(st.HashSlots.Ranges) == 0 &&
		len(st.Tasks) == 0
}

func reconcileTaskFromControllerV2(task cv2.ReconcileTask) ReconcileTask {
	return ReconcileTask{
		TaskID:              task.TaskID,
		SlotID:              task.SlotID,
		Kind:                TaskKind(task.Kind),
		Step:                TaskStep(task.Step),
		SourceNode:          task.SourceNode,
		TargetNode:          task.TargetNode,
		TargetPeers:         append([]uint64(nil), task.TargetPeers...),
		CompletionPolicy:    TaskCompletionPolicy(task.CompletionPolicy),
		ParticipantProgress: append([]TaskParticipantProgress(nil), task.ParticipantProgress...),
		ConfigEpoch:         task.ConfigEpoch,
		Attempt:             task.Attempt,
		Status:              TaskStatus(task.Status),
		LastError:           task.LastError,
		PhaseIndex:          task.PhaseIndex,
		ObservedConfigIndex: task.ObservedConfigIndex,
		ObservedVoters:      append([]uint64(nil), task.ObservedVoters...),
		ObservedLearners:    append([]uint64(nil), task.ObservedLearners...),
	}
}

var _ Controller = (*Runtime)(nil)
