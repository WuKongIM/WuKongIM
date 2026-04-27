package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type controllerAPI interface {
	Report(ctx context.Context, report slotcontroller.AgentReport) error
	ReportRuntimeObservation(ctx context.Context, report runtimeObservationReport) error
	ListNodes(ctx context.Context) ([]controllermeta.ClusterNode, error)
	RefreshAssignments(ctx context.Context) ([]controllermeta.SlotAssignment, error)
	ListRuntimeViews(ctx context.Context) ([]controllermeta.SlotRuntimeView, error)
	ListTasks(ctx context.Context) ([]controllermeta.ReconcileTask, error)
	FetchObservationDelta(ctx context.Context, req observationDeltaRequest) (observationDeltaResponse, error)
	Operator(ctx context.Context, op slotcontroller.OperatorRequest) error
	GetTask(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error)
	ForceReconcile(ctx context.Context, slotID uint32) error
	ReportTaskResult(ctx context.Context, task controllermeta.ReconcileTask, taskErr error) error
	StartMigration(ctx context.Context, req slotcontroller.MigrationRequest) error
	AdvanceMigration(ctx context.Context, req slotcontroller.MigrationRequest) error
	FinalizeMigration(ctx context.Context, req slotcontroller.MigrationRequest) error
	AbortMigration(ctx context.Context, req slotcontroller.MigrationRequest) error
	AddSlot(ctx context.Context, req slotcontroller.AddSlotRequest) error
	RemoveSlot(ctx context.Context, req slotcontroller.RemoveSlotRequest) error
	JoinCluster(ctx context.Context, req joinClusterRequest) (joinClusterResponse, error)
	ListNodeOnboardingCandidates(ctx context.Context) ([]NodeOnboardingCandidate, error)
	CreateNodeOnboardingPlan(ctx context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error)
	StartNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
	ListNodeOnboardingJobs(ctx context.Context, limit int, cursor string) ([]controllermeta.NodeOnboardingJob, string, bool, error)
	GetNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
	RetryNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
}

type controllerClient struct {
	cluster *Cluster
	cache   *assignmentCache
	peersMu sync.RWMutex
	peers   []multiraft.NodeID

	leader         atomic.Uint64
	onLeaderChange func(multiraft.NodeID)
}

func newControllerClient(cluster *Cluster, peers []NodeConfig, cache *assignmentCache) *controllerClient {
	ids := make([]multiraft.NodeID, 0, len(peers))
	for _, peer := range peers {
		ids = append(ids, peer.NodeID)
	}
	return &controllerClient{
		cluster: cluster,
		cache:   cache,
		peers:   ids,
	}
}

func (c *controllerClient) Report(ctx context.Context, report slotcontroller.AgentReport) error {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind:   controllerRPCHeartbeat,
		Report: &report,
	})
	if err != nil {
		return err
	}
	return c.cluster.applyHashSlotTablePayload(resp.HashSlotTable)
}

func (c *controllerClient) ReportRuntimeObservation(ctx context.Context, report runtimeObservationReport) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:          controllerRPCRuntimeReport,
		RuntimeReport: &report,
	})
	return err
}

func (c *controllerClient) ListNodes(ctx context.Context) ([]controllermeta.ClusterNode, error) {
	resp, err := c.call(ctx, controllerRPCRequest{Kind: controllerRPCListNodes})
	if err != nil {
		return nil, err
	}
	c.cluster.applyClusterNodes(resp.Nodes)
	return resp.Nodes, nil
}

func (c *controllerClient) RefreshAssignments(ctx context.Context) ([]controllermeta.SlotAssignment, error) {
	resp, err := c.call(ctx, controllerRPCRequest{Kind: controllerRPCListAssignments})
	if err != nil {
		return nil, err
	}
	if err := c.cluster.applyHashSlotTablePayload(resp.HashSlotTable); err != nil {
		return nil, err
	}
	if c.cache != nil {
		c.cache.SetAssignments(resp.Assignments)
	}
	return resp.Assignments, nil
}

func (c *controllerClient) ListRuntimeViews(ctx context.Context) ([]controllermeta.SlotRuntimeView, error) {
	resp, err := c.call(ctx, controllerRPCRequest{Kind: controllerRPCListRuntimeViews})
	if err != nil {
		return nil, err
	}
	if resp.ObservationNotReady {
		return nil, ErrObservationNotReady
	}
	return resp.RuntimeViews, nil
}

func (c *controllerClient) ListTasks(ctx context.Context) ([]controllermeta.ReconcileTask, error) {
	resp, err := c.call(ctx, controllerRPCRequest{Kind: controllerRPCListTasks})
	if err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

func (c *controllerClient) FetchObservationDelta(ctx context.Context, req observationDeltaRequest) (observationDeltaResponse, error) {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind:             controllerRPCFetchObservationDelta,
		ObservationDelta: &req,
	})
	if err != nil {
		return observationDeltaResponse{}, err
	}
	if resp.ObservationDelta == nil {
		return observationDeltaResponse{}, ErrInvalidConfig
	}
	return *resp.ObservationDelta, nil
}

func (c *controllerClient) Operator(ctx context.Context, op slotcontroller.OperatorRequest) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind: controllerRPCOperator,
		Op:   &op,
	})
	return err
}

func (c *controllerClient) GetTask(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind:   controllerRPCGetTask,
		SlotID: slotID,
	})
	if err != nil {
		return controllermeta.ReconcileTask{}, err
	}
	if resp.NotFound || resp.Task == nil {
		return controllermeta.ReconcileTask{}, controllermeta.ErrNotFound
	}
	return *resp.Task, nil
}

func (c *controllerClient) ForceReconcile(ctx context.Context, slotID uint32) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:   controllerRPCForceReconcile,
		SlotID: slotID,
	})
	return err
}

func (c *controllerClient) ReportTaskResult(ctx context.Context, task controllermeta.ReconcileTask, taskErr error) error {
	advance := &controllerTaskAdvance{
		SlotID:  task.SlotID,
		Attempt: task.Attempt,
		Now:     time.Now(),
	}
	if taskErr != nil {
		advance.Err = taskErr.Error()
	}
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:    controllerRPCTaskResult,
		SlotID:  task.SlotID,
		Advance: advance,
	})
	return err
}

func (c *controllerClient) StartMigration(ctx context.Context, req slotcontroller.MigrationRequest) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:      controllerRPCStartMigration,
		Migration: &req,
	})
	return err
}

func (c *controllerClient) AdvanceMigration(ctx context.Context, req slotcontroller.MigrationRequest) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:      controllerRPCAdvanceMigration,
		Migration: &req,
	})
	return err
}

func (c *controllerClient) FinalizeMigration(ctx context.Context, req slotcontroller.MigrationRequest) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:      controllerRPCFinalizeMigration,
		Migration: &req,
	})
	return err
}

func (c *controllerClient) AbortMigration(ctx context.Context, req slotcontroller.MigrationRequest) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:      controllerRPCAbortMigration,
		Migration: &req,
	})
	return err
}

func (c *controllerClient) AddSlot(ctx context.Context, req slotcontroller.AddSlotRequest) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:    controllerRPCAddSlot,
		AddSlot: &req,
	})
	return err
}

func (c *controllerClient) RemoveSlot(ctx context.Context, req slotcontroller.RemoveSlotRequest) error {
	_, err := c.call(ctx, controllerRPCRequest{
		Kind:       controllerRPCRemoveSlot,
		RemoveSlot: &req,
	})
	return err
}

func (c *controllerClient) JoinCluster(ctx context.Context, req joinClusterRequest) (joinClusterResponse, error) {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind: controllerRPCJoinCluster,
		Join: &req,
	})
	if err != nil {
		return joinClusterResponse{}, err
	}
	if resp.Join == nil {
		return joinClusterResponse{}, ErrInvalidConfig
	}
	if resp.Join.JoinErrorCode != joinErrorNone {
		return *resp.Join, &joinClusterError{
			Code:    resp.Join.JoinErrorCode,
			Message: resp.Join.JoinErrorMessage,
		}
	}
	if err := validateJoinedMembership(resp.Join.Nodes, req); err != nil {
		return joinClusterResponse{}, err
	}
	if err := c.cluster.applyHashSlotTablePayload(resp.Join.HashSlotTable); err != nil {
		return joinClusterResponse{}, err
	}
	c.cluster.applyClusterNodes(resp.Join.Nodes)
	return *resp.Join, nil
}

func (c *controllerClient) ListNodeOnboardingCandidates(ctx context.Context) ([]NodeOnboardingCandidate, error) {
	resp, err := c.call(ctx, controllerRPCRequest{Kind: controllerRPCListOnboardingCandidates})
	if err != nil {
		return nil, err
	}
	if err := onboardingErrorFromCode(resp.OnboardingErrorCode); err != nil {
		return nil, err
	}
	return resp.OnboardingCandidates, nil
}

func (c *controllerClient) CreateNodeOnboardingPlan(ctx context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error) {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind: controllerRPCCreateOnboardingPlan,
		OnboardingPlan: &nodeOnboardingPlanRequest{
			TargetNodeID: targetNodeID,
			RetryOfJobID: retryOfJobID,
		},
	})
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	return onboardingJobFromResponse(resp)
}

func (c *controllerClient) StartNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error) {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind:          controllerRPCStartOnboardingJob,
		OnboardingJob: &nodeOnboardingJobRequest{JobID: jobID},
	})
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	return onboardingJobFromResponse(resp)
}

func (c *controllerClient) ListNodeOnboardingJobs(ctx context.Context, limit int, cursor string) ([]controllermeta.NodeOnboardingJob, string, bool, error) {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind: controllerRPCListOnboardingJobs,
		OnboardingJobs: &nodeOnboardingJobsRequest{
			Limit:  limit,
			Cursor: cursor,
		},
	})
	if err != nil {
		return nil, "", false, err
	}
	if err := onboardingErrorFromCode(resp.OnboardingErrorCode); err != nil {
		return nil, "", false, err
	}
	return resp.OnboardingJobs, resp.OnboardingCursor, resp.OnboardingHasMore, nil
}

func (c *controllerClient) GetNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error) {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind:          controllerRPCGetOnboardingJob,
		OnboardingJob: &nodeOnboardingJobRequest{JobID: jobID},
	})
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	return onboardingJobFromResponse(resp)
}

func (c *controllerClient) RetryNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error) {
	resp, err := c.call(ctx, controllerRPCRequest{
		Kind:          controllerRPCRetryOnboardingJob,
		OnboardingJob: &nodeOnboardingJobRequest{JobID: jobID},
	})
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	return onboardingJobFromResponse(resp)
}

func onboardingJobFromResponse(resp controllerRPCResponse) (controllermeta.NodeOnboardingJob, error) {
	if resp.NotFound {
		return controllermeta.NodeOnboardingJob{}, controllermeta.ErrNotFound
	}
	if err := onboardingErrorFromCode(resp.OnboardingErrorCode); err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	if resp.OnboardingJob == nil {
		return controllermeta.NodeOnboardingJob{}, ErrInvalidConfig
	}
	return *resp.OnboardingJob, nil
}

func (c *controllerClient) UpdatePeers(peers []multiraft.NodeID) {
	if c == nil {
		return
	}
	next := make([]multiraft.NodeID, 0, len(peers))
	for _, peer := range peers {
		if peer == 0 {
			continue
		}
		next = append(next, peer)
	}
	c.peersMu.Lock()
	c.peers = next
	c.peersMu.Unlock()
}

func (c *controllerClient) call(ctx context.Context, req controllerRPCRequest) (resp controllerRPCResponse, err error) {
	start := time.Now()
	defer func() {
		if c != nil && c.cluster != nil {
			if hook := c.cluster.obs.OnControllerCall; hook != nil {
				hook(req.Kind, observerElapsed(start), err)
			}
		}
	}()

	if c == nil || c.cluster == nil {
		err = ErrNotStarted
		return controllerRPCResponse{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	body, err := encodeControllerRequest(req)
	if err != nil {
		return controllerRPCResponse{}, err
	}

	targets := c.targets()
	if len(targets) == 0 {
		err = ErrNoLeader
		return controllerRPCResponse{}, err
	}

	tried := make(map[multiraft.NodeID]struct{}, len(targets))
	var lastErr error
	var lastTarget multiraft.NodeID
	for len(targets) > 0 {
		target := targets[0]
		targets = targets[1:]
		if _, seen := tried[target]; seen {
			continue
		}
		tried[target] = struct{}{}
		lastTarget = target

		// Give each peer probe its own budget so a slow stale leader does not
		// consume the entire controller retry window before we reach the current
		// leader.
		rpcCtx, cancel := c.cluster.withControllerTimeout(ctx)
		var respBody []byte
		if target == c.cluster.cfg.NodeID {
			respBody, err = c.cluster.handleControllerRPC(rpcCtx, body)
		} else {
			respBody, err = c.cluster.controllerRPCService(rpcCtx, target, body)
		}
		cancel()
		if err != nil {
			if c.cachedLeader() == target {
				c.clearLeader()
			}
			c.logRetry(req, target, err, "controller rpc attempt failed, retrying")
			lastErr = err
			continue
		}

		resp, err := decodeControllerResponse(req.Kind, respBody)
		if err != nil {
			return controllerRPCResponse{}, err
		}
		if resp.NotLeader {
			if resp.LeaderID != 0 {
				leaderID := multiraft.NodeID(resp.LeaderID)
				c.upsertLeaderSeed(leaderID, resp.LeaderAddr)
				c.setLeader(leaderID)
				if _, seen := tried[leaderID]; !seen {
					targets = append([]multiraft.NodeID{leaderID}, targets...)
				}
			} else {
				c.clearLeader()
			}
			c.logRetry(req, target, ErrNotLeader, "controller rpc attempt failed, retrying")
			lastErr = ErrNotLeader
			continue
		}

		c.setLeader(target)
		return resp, nil
	}

	if lastErr == nil {
		lastErr = ErrNoLeader
	}
	err = lastErr
	c.logFailure(ctx, req, lastTarget, err)
	return controllerRPCResponse{}, err
}

func (c *controllerClient) targets() []multiraft.NodeID {
	if c == nil {
		return nil
	}

	leader := c.cachedLeader()
	if hintedLeader, ok := c.localLeaderHint(); ok {
		leader = hintedLeader
	}

	peers := c.peerSnapshot()
	targets := make([]multiraft.NodeID, 0, len(peers)+1)
	if leader != 0 {
		targets = append(targets, leader)
	}
	for _, peer := range peers {
		if peer == leader {
			continue
		}
		targets = append(targets, peer)
	}
	return targets
}

func (c *controllerClient) localLeaderHint() (multiraft.NodeID, bool) {
	if c == nil || c.cluster == nil || c.cluster.controller == nil {
		return 0, false
	}
	leader := multiraft.NodeID(c.cluster.controller.LeaderID())
	if leader == 0 {
		return 0, false
	}
	for _, peer := range c.peerSnapshot() {
		if peer == leader {
			return leader, true
		}
	}
	return 0, false
}

func (c *controllerClient) peerSnapshot() []multiraft.NodeID {
	if c == nil {
		return nil
	}
	c.peersMu.RLock()
	defer c.peersMu.RUnlock()
	return append([]multiraft.NodeID(nil), c.peers...)
}

func (c *controllerClient) upsertLeaderSeed(leader multiraft.NodeID, addr string) {
	if c == nil || c.cluster == nil || leader == 0 || addr == "" {
		return
	}
	discovery, ok := c.cluster.discovery.(interface{ UpsertSeed(SeedConfig) })
	if !ok || discovery == nil {
		return
	}
	discovery.UpsertSeed(SeedConfig{ID: leader, Addr: addr})
}

func (c *controllerClient) cachedLeader() multiraft.NodeID {
	if c == nil {
		return 0
	}
	return multiraft.NodeID(c.leader.Load())
}

func (c *controllerClient) setLeader(nodeID multiraft.NodeID) {
	if c == nil {
		return
	}
	prev := c.cachedLeader()
	c.leader.Store(uint64(nodeID))
	if nodeID != prev && c.onLeaderChange != nil {
		c.onLeaderChange(nodeID)
	}
}

func (c *controllerClient) clearLeader() {
	if c == nil {
		return
	}
	c.leader.Store(0)
}

func isControllerRedirect(err error) bool {
	return errors.Is(err, ErrNotLeader)
}

func (c *controllerClient) logRetry(req controllerRPCRequest, target multiraft.NodeID, err error, msg string) {
	logger := c.controllerLogger()
	if logger == nil {
		return
	}
	fields := c.controllerLogFields(req, target)
	fields = append(fields, wklog.Event("cluster.controller.rpc.retrying"), wklog.Error(err))
	if shouldDebugLogReadOnlyControllerFailure(req, err) {
		logger.Debug("controller rpc read attempt failed, retrying", fields...)
		return
	}
	logger.Warn(msg, fields...)
}

func (c *controllerClient) logFailure(ctx context.Context, req controllerRPCRequest, target multiraft.NodeID, err error) {
	logger := c.controllerLogger()
	if logger == nil {
		return
	}
	fields := c.controllerLogFields(req, target)
	fields = append(fields, wklog.Event("cluster.controller.rpc.failed"), wklog.Error(err))
	if shouldDebugLogReadOnlyControllerFailure(req, err) {
		logger.Debug("controller rpc read unavailable", fields...)
		return
	}
	if controllerRPCLogPolicyFromContext(ctx) == controllerRPCLogBestEffortRead && isReadOnlyControllerRPC(req.Kind) {
		logger.Warn("controller rpc read failed", fields...)
		return
	}
	logger.Error("controller rpc failed", fields...)
}

// shouldDebugLogReadOnlyControllerFailure keeps transient read unavailability out of operator error logs.
func shouldDebugLogReadOnlyControllerFailure(req controllerRPCRequest, err error) bool {
	return isReadOnlyControllerRPC(req.Kind) && controllerCommandRetryAllowed(err)
}

func isReadOnlyControllerRPC(kind string) bool {
	switch kind {
	case controllerRPCListNodes,
		controllerRPCListAssignments,
		controllerRPCListRuntimeViews,
		controllerRPCListTasks:
		return true
	default:
		return false
	}
}

func (c *controllerClient) controllerLogFields(req controllerRPCRequest, target multiraft.NodeID) []wklog.Field {
	fields := []wklog.Field{wklog.String("rpc", req.Kind)}
	if target != 0 {
		fields = append(fields, wklog.TargetNodeID(uint64(target)))
	}
	slotID := req.SlotID
	if slotID == 0 && req.Advance != nil {
		slotID = req.Advance.SlotID
	}
	if slotID != 0 {
		fields = append(fields, wklog.SlotID(uint64(slotID)))
	}
	return fields
}

func (c *controllerClient) controllerLogger() wklog.Logger {
	if c == nil || c.cluster == nil {
		return wklog.NewNop()
	}
	return c.cluster.controllerLogger()
}
