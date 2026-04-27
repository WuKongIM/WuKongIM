package management

import (
	"context"
	"errors"
	"fmt"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	scaleInOnboardingJobScanLimit = 100
	scaleInFutureReportTolerance  = time.Second
	scaleInDefaultLeaderTransfers = 1
	scaleInMaxLeaderTransfers     = 3
)

var (
	// ErrNodeScaleInBlocked reports that preflight safety checks currently block the action.
	ErrNodeScaleInBlocked = errors.New("management: node scale-in blocked")
	// ErrInvalidNodeScaleInState reports that the requested action does not fit the current scale-in state.
	ErrInvalidNodeScaleInState = errors.New("management: invalid node scale-in state")
)

// NodeScaleInStatus describes the current manager-driven scale-in state.
type NodeScaleInStatus string

const (
	// NodeScaleInStatusBlocked means one or more safety checks currently block scale-in.
	NodeScaleInStatusBlocked NodeScaleInStatus = "blocked"
	// NodeScaleInStatusNotStarted means the target is safe to start but is not Draining yet.
	NodeScaleInStatusNotStarted NodeScaleInStatus = "not_started"
	// NodeScaleInStatusFailed means scale-in progress is blocked by failed reconcile work.
	NodeScaleInStatusFailed NodeScaleInStatus = "failed"
	// NodeScaleInStatusMigratingReplicas means Slot replicas or reconcile tasks still reference the target.
	NodeScaleInStatusMigratingReplicas NodeScaleInStatus = "migrating_replicas"
	// NodeScaleInStatusTransferringLeaders means replicas are gone but Slot leaders still run on the target.
	NodeScaleInStatusTransferringLeaders NodeScaleInStatus = "transferring_leaders"
	// NodeScaleInStatusWaitingConnections means cluster state is clear but runtime sessions are still present or unknown.
	NodeScaleInStatusWaitingConnections NodeScaleInStatus = "waiting_connections"
	// NodeScaleInStatusReadyToRemove means the manager sees no remaining blockers for external node removal.
	NodeScaleInStatusReadyToRemove NodeScaleInStatus = "ready_to_remove"
)

// NodeScaleInPlanRequest carries operator confirmation needed for scale-in safety checks.
type NodeScaleInPlanRequest struct {
	// ConfirmStatefulSetTail confirms the operator has verified the target is the Kubernetes StatefulSet tail.
	ConfirmStatefulSetTail bool
	// ExpectedTailNodeID is the node ID the operator expects Kubernetes scale-down to remove.
	ExpectedTailNodeID uint64
}

// AdvanceNodeScaleInRequest controls one bounded scale-in advancement attempt.
type AdvanceNodeScaleInRequest struct {
	// MaxLeaderTransfers caps leader transfers performed by one request; zero defaults to one.
	MaxLeaderTransfers int
	// ForceCloseConnections requests connection closing but is not a safety override.
	ForceCloseConnections bool
}

// NodeScaleInReportError carries the latest report alongside a typed action error.
type NodeScaleInReportError struct {
	// Err is the typed cause returned by errors.Is/As.
	Err error
	// Report is the latest fail-closed report for the target node.
	Report NodeScaleInReport
}

func (e NodeScaleInReportError) Error() string {
	if e.Err == nil {
		return "management: node scale-in report error"
	}
	return e.Err.Error()
}

// Unwrap returns the typed action error.
func (e NodeScaleInReportError) Unwrap() error {
	return e.Err
}

// NodeScaleInReport contains safety checks, progress counters, and the current scale-in status.
type NodeScaleInReport struct {
	// NodeID is the target node being evaluated for scale-in.
	NodeID uint64
	// Status is the current manager-computed scale-in status.
	Status NodeScaleInStatus
	// SafeToRemove is true only when the node is ready for external removal.
	SafeToRemove bool
	// ConnectionSafetyVerified reports whether runtime connection counters were known.
	ConnectionSafetyVerified bool
	// Checks contains boolean safety check results.
	Checks NodeScaleInChecks
	// Progress contains scale-in progress counters.
	Progress NodeScaleInProgress
	// Runtime contains the latest target-node runtime counters used for connection safety.
	Runtime NodeRuntimeSummary
	// BlockedReasons lists blocking safety reasons.
	BlockedReasons []NodeScaleInBlockedReason
}

// NodeScaleInChecks records individual scale-in safety checks.
type NodeScaleInChecks struct {
	// SlotReplicaCountKnown reports whether configured SlotReplicaN is available to the usecase.
	SlotReplicaCountKnown bool
	// ControllerReadsAvailable reports whether all strict controller-backed reads succeeded.
	ControllerReadsAvailable bool
	// TargetNodeFound reports whether the target exists in controller metadata.
	TargetNodeFound bool
	// TargetIsDataNode reports whether the target has the data-plane node role.
	TargetIsDataNode bool
	// TargetActiveOrDraining reports whether the target can participate in scale-in.
	TargetActiveOrDraining bool
	// TailNodeMappingVerified reports operator confirmation for StatefulSet tail removal.
	TailNodeMappingVerified bool
	// RuntimeViewsFresh reports whether every assigned Slot has a complete fresh runtime view.
	RuntimeViewsFresh bool
	// ConnectionSafetyKnown reports whether target runtime counters were available.
	ConnectionSafetyKnown bool
}

// NodeScaleInProgress records counters used to explain scale-in progress.
type NodeScaleInProgress struct {
	// AssignedSlotReplicas counts desired Slot replicas that still include the target.
	AssignedSlotReplicas int
	// ObservedSlotReplicas counts runtime Slot peers that still include the target.
	ObservedSlotReplicas int
	// SlotLeaders counts observed Slot leaders still running on the target.
	SlotLeaders int
	// ActiveTasksInvolvingNode counts active reconcile tasks that reference the target.
	ActiveTasksInvolvingNode int
	// ActiveMigrationsInvolvingNode counts active hash-slot migrations that must finish before scale-in.
	ActiveMigrationsInvolvingNode int
	// ActiveConnections counts active online connections on the target.
	ActiveConnections int
	// ClosingConnections counts target connections that are closing but still registered.
	ClosingConnections int
	// GatewaySessions counts gateway sessions on the target, including unauthenticated sessions.
	GatewaySessions int
	// ActiveConnectionsUnknown reports that runtime connection counters could not be read.
	ActiveConnectionsUnknown bool
}

// NodeScaleInBlockedReason describes one safety condition blocking scale-in.
type NodeScaleInBlockedReason struct {
	// Code is the stable machine-readable reason code.
	Code string
	// Message is a human-readable explanation.
	Message string
	// Count is an optional number related to the blocked condition.
	Count int
	// SlotID is the optional slot related to the blocked condition.
	SlotID uint32
	// NodeID is the optional node related to the blocked condition.
	NodeID uint64
}

type nodeScaleInSnapshot struct {
	nodes              []controllermeta.ClusterNode
	assignments        []controllermeta.SlotAssignment
	views              []controllermeta.SlotRuntimeView
	tasks              []controllermeta.ReconcileTask
	migrations         []raftcluster.HashSlotMigration
	onboardingJobs     []controllermeta.NodeOnboardingJob
	onboardingHasMore  bool
	runtime            NodeRuntimeSummary
	runtimeReadFailure bool
}

// PlanNodeScaleIn returns a fail-closed scale-in report for the target node.
func (a *App) PlanNodeScaleIn(ctx context.Context, nodeID uint64, req NodeScaleInPlanRequest) (NodeScaleInReport, error) {
	report := NodeScaleInReport{NodeID: nodeID, Status: NodeScaleInStatusBlocked}
	if a == nil || a.slotReplicaN <= 0 {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("slot_replica_count_unknown", "slot replica count is not configured", 0, 0, 0))
		return report, nil
	}
	report.Checks.SlotReplicaCountKnown = true

	snapshot, err := a.loadNodeScaleInSnapshot(ctx, nodeID)
	if err != nil {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("controller_leader_unavailable", fmt.Sprintf("strict controller reads are unavailable: %v", err), 0, 0, 0))
		return report, nil
	}
	report.Checks.ControllerReadsAvailable = true
	report.Runtime = snapshot.runtime

	target, targetFound := findScaleInNode(snapshot.nodes, nodeID)
	report.Checks.TargetNodeFound = targetFound
	if !targetFound {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("target_not_found", "target node was not found in controller metadata", 0, 0, nodeID))
	} else {
		report.Checks.TargetIsDataNode = target.Role == controllermeta.NodeRoleData
		report.Checks.TargetActiveOrDraining = scaleInNodeActiveOrDraining(target)
		if !report.Checks.TargetIsDataNode {
			report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("target_not_data_node", "target node is not a data node", 0, 0, nodeID))
		}
		if scaleInNodeIsControllerVoter(a, target) {
			report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("target_is_controller_voter", "target node is a controller voter and cannot be scaled in by the data-node flow", 0, 0, nodeID))
		}
		if !report.Checks.TargetActiveOrDraining {
			report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("target_not_active_or_draining", "target node is not alive or draining", 0, 0, nodeID))
		}
	}

	report.Checks.TailNodeMappingVerified = req.ConfirmStatefulSetTail && req.ExpectedTailNodeID == nodeID
	if !report.Checks.TailNodeMappingVerified {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("tail_node_mapping_unverified", "operator must confirm the target is the StatefulSet tail node", 0, 0, nodeID))
	}

	remainingDataNodes := scaleInRemainingAliveDataNodes(snapshot.nodes, nodeID)
	if len(remainingDataNodes) < a.slotReplicaN {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("remaining_data_nodes_insufficient", "remaining alive data nodes are fewer than the configured Slot replica count", len(remainingDataNodes), 0, nodeID))
	}

	if other, ok := scaleInOtherDrainingDataNode(snapshot.nodes, nodeID); ok {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("other_draining_node_exists", "another data node is already draining", 1, 0, other.NodeID))
	}

	if len(snapshot.migrations) > 0 {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("active_hashslot_migrations_exist", "active hash-slot migrations must finish before scale-in", len(snapshot.migrations), 0, 0))
	}
	if running := scaleInRunningOnboardingJobs(snapshot.onboardingJobs); running > 0 || snapshot.onboardingHasMore {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("running_onboarding_exists", "running node onboarding jobs must finish before scale-in", running, 0, 0))
	}

	activeTargetTasks, failedTasks := scaleInTaskCounts(snapshot.tasks, nodeID)
	report.Progress.ActiveTasksInvolvingNode = activeTargetTasks
	if targetFound && target.Status != controllermeta.NodeStatusDraining && activeTargetTasks > 0 {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("active_reconcile_tasks_involving_target", "active reconcile tasks involving the target must finish before scale-in starts", activeTargetTasks, 0, nodeID))
	}
	if failedTasks > 0 {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("failed_reconcile_tasks_exist", "failed reconcile tasks must be resolved before scale-in", failedTasks, 0, 0))
	}

	viewBySlot := scaleInRuntimeViewBySlot(snapshot.views)
	report.Checks.RuntimeViewsFresh = scaleInRuntimeViewsCompleteAndFresh(a.now(), a.scaleInRuntimeViewMaxAge, snapshot.assignments, snapshot.views)
	if !report.Checks.RuntimeViewsFresh {
		report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("runtime_views_incomplete_or_stale", "Slot runtime views are incomplete, stale, or from the future", 0, 0, 0))
	}
	for _, assignment := range snapshot.assignments {
		view, ok := viewBySlot[assignment.SlotID]
		if !ok {
			continue
		}
		if !view.HasQuorum {
			report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("slot_quorum_lost", "a Slot runtime view reports quorum loss", 0, assignment.SlotID, 0))
		}
		if scaleInAssignmentContains(assignment, nodeID) && scaleInViewContains(view, nodeID) && view.HealthyVoters <= 1 {
			report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("target_unique_healthy_replica", "target appears to be the only healthy replica for a Slot", int(view.HealthyVoters), assignment.SlotID, nodeID))
		}
	}

	report.Progress.AssignedSlotReplicas = scaleInAssignedReplicaCount(snapshot.assignments, nodeID)
	report.Progress.ObservedSlotReplicas = scaleInObservedReplicaCount(snapshot.views, nodeID)
	report.Progress.SlotLeaders = scaleInLeaderCount(snapshot.views, nodeID)
	report.Progress.ActiveMigrationsInvolvingNode = len(snapshot.migrations)
	report.Progress.ActiveConnections = snapshot.runtime.ActiveOnline
	report.Progress.ClosingConnections = snapshot.runtime.ClosingOnline
	report.Progress.GatewaySessions = snapshot.runtime.GatewaySessions
	report.Progress.ActiveConnectionsUnknown = snapshot.runtime.Unknown || snapshot.runtimeReadFailure
	report.ConnectionSafetyVerified = !report.Progress.ActiveConnectionsUnknown
	report.Checks.ConnectionSafetyKnown = report.ConnectionSafetyVerified

	report.Status = scaleInStatus(target, targetFound, report.BlockedReasons, report.Progress, report.ConnectionSafetyVerified)
	report.SafeToRemove = report.Status == NodeScaleInStatusReadyToRemove
	return report, nil
}

// StartNodeScaleIn marks a preflight-safe node as Draining and returns the refreshed report.
func (a *App) StartNodeScaleIn(ctx context.Context, nodeID uint64, req NodeScaleInPlanRequest) (NodeScaleInReport, error) {
	report, err := a.PlanNodeScaleIn(ctx, nodeID, req)
	if err != nil {
		return report, err
	}
	if len(report.BlockedReasons) > 0 {
		return report, &NodeScaleInReportError{Err: ErrNodeScaleInBlocked, Report: report}
	}
	if report.Status != NodeScaleInStatusNotStarted {
		return report, nil
	}
	if a == nil || a.cluster == nil {
		return report, &NodeScaleInReportError{Err: ErrNodeScaleInBlocked, Report: report}
	}
	if err := a.cluster.MarkNodeDraining(ctx, nodeID); err != nil {
		return report, err
	}
	return a.PlanNodeScaleIn(ctx, nodeID, req)
}

// GetNodeScaleInStatus returns the current scale-in report without requiring a mutating action request.
func (a *App) GetNodeScaleInStatus(ctx context.Context, nodeID uint64) (NodeScaleInReport, error) {
	return a.PlanNodeScaleIn(ctx, nodeID, NodeScaleInPlanRequest{
		ConfirmStatefulSetTail: true,
		ExpectedTailNodeID:     nodeID,
	})
}

// CancelNodeScaleIn resumes a Draining node and returns the refreshed report.
func (a *App) CancelNodeScaleIn(ctx context.Context, nodeID uint64) (NodeScaleInReport, error) {
	if a == nil || a.cluster == nil {
		return NodeScaleInReport{NodeID: nodeID, Status: NodeScaleInStatusBlocked}, ErrNodeScaleInBlocked
	}
	nodes, err := a.cluster.ListNodesStrict(ctx)
	if err != nil {
		return NodeScaleInReport{NodeID: nodeID, Status: NodeScaleInStatusBlocked}, err
	}
	target, ok := findScaleInNode(nodes, nodeID)
	if !ok {
		return NodeScaleInReport{NodeID: nodeID, Status: NodeScaleInStatusBlocked}, controllermeta.ErrNotFound
	}
	if target.Status != controllermeta.NodeStatusDraining {
		return a.GetNodeScaleInStatus(ctx, nodeID)
	}
	if err := a.cluster.ResumeNode(ctx, nodeID); err != nil {
		return NodeScaleInReport{NodeID: nodeID, Status: NodeScaleInStatusBlocked}, err
	}
	return a.GetNodeScaleInStatus(ctx, nodeID)
}

// AdvanceNodeScaleIn performs a bounded leader-transfer step and returns the refreshed report.
func (a *App) AdvanceNodeScaleIn(ctx context.Context, nodeID uint64, req AdvanceNodeScaleInRequest) (NodeScaleInReport, error) {
	report, err := a.GetNodeScaleInStatus(ctx, nodeID)
	if err != nil {
		return report, err
	}
	if req.ForceCloseConnections {
		return report, &NodeScaleInReportError{Err: ErrInvalidNodeScaleInState, Report: report}
	}
	if a == nil || a.cluster == nil {
		return report, &NodeScaleInReportError{Err: ErrNodeScaleInBlocked, Report: report}
	}

	snapshot, err := a.loadNodeScaleInSnapshot(ctx, nodeID)
	if err != nil {
		return report, err
	}
	target, ok := findScaleInNode(snapshot.nodes, nodeID)
	if !ok {
		return report, controllermeta.ErrNotFound
	}
	if target.Status != controllermeta.NodeStatusDraining {
		return report, &NodeScaleInReportError{Err: ErrInvalidNodeScaleInState, Report: report}
	}

	limit := clampScaleInLeaderTransfers(req.MaxLeaderTransfers)
	transferred := 0
	for _, view := range snapshot.views {
		if view.LeaderID != nodeID {
			continue
		}
		candidate, ok := selectScaleInLeaderCandidate(snapshot.nodes, snapshot.assignments, view, nodeID)
		if !ok {
			return report, &NodeScaleInReportError{Err: ErrInvalidNodeScaleInState, Report: report}
		}
		if err := a.cluster.TransferSlotLeader(ctx, view.SlotID, multiraft.NodeID(candidate)); err != nil {
			return report, err
		}
		transferred++
		if transferred >= limit {
			break
		}
	}
	return a.GetNodeScaleInStatus(ctx, nodeID)
}

func (a *App) loadNodeScaleInSnapshot(ctx context.Context, nodeID uint64) (nodeScaleInSnapshot, error) {
	if a == nil || a.cluster == nil {
		return nodeScaleInSnapshot{}, fmt.Errorf("cluster reader is not configured")
	}
	nodes, err := a.cluster.ListNodesStrict(ctx)
	if err != nil {
		return nodeScaleInSnapshot{}, err
	}
	assignments, err := a.cluster.ListSlotAssignmentsStrict(ctx)
	if err != nil {
		return nodeScaleInSnapshot{}, err
	}
	views, err := a.cluster.ListObservedRuntimeViewsStrict(ctx)
	if err != nil {
		return nodeScaleInSnapshot{}, err
	}
	tasks, err := a.cluster.ListTasksStrict(ctx)
	if err != nil {
		return nodeScaleInSnapshot{}, err
	}
	migrations, err := a.cluster.ListActiveMigrationsStrict(ctx)
	if err != nil {
		return nodeScaleInSnapshot{}, err
	}
	onboardingJobs, _, onboardingHasMore, err := a.cluster.ListNodeOnboardingJobs(ctx, scaleInOnboardingJobScanLimit, "")
	if err != nil {
		return nodeScaleInSnapshot{}, err
	}

	runtime := NodeRuntimeSummary{NodeID: nodeID, Unknown: true}
	runtimeReadFailure := true
	if a.runtimeSummary != nil {
		runtime, err = a.runtimeSummary.NodeRuntimeSummary(ctx, nodeID)
		if err == nil && (runtime.NodeID == 0 || runtime.NodeID == nodeID) && !runtime.Unknown {
			runtime.NodeID = nodeID
			runtimeReadFailure = false
		} else {
			runtime = NodeRuntimeSummary{NodeID: nodeID, Unknown: true}
		}
	}

	return nodeScaleInSnapshot{
		nodes:              nodes,
		assignments:        assignments,
		views:              views,
		tasks:              tasks,
		migrations:         migrations,
		onboardingJobs:     onboardingJobs,
		onboardingHasMore:  onboardingHasMore,
		runtime:            runtime,
		runtimeReadFailure: runtimeReadFailure,
	}, nil
}

func scaleInBlockedReason(code, message string, count int, slotID uint32, nodeID uint64) NodeScaleInBlockedReason {
	return NodeScaleInBlockedReason{Code: code, Message: message, Count: count, SlotID: slotID, NodeID: nodeID}
}

func findScaleInNode(nodes []controllermeta.ClusterNode, nodeID uint64) (controllermeta.ClusterNode, bool) {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return controllermeta.ClusterNode{}, false
}

func scaleInNodeIsControllerVoter(a *App, node controllermeta.ClusterNode) bool {
	if node.Role == controllermeta.NodeRoleControllerVoter {
		return true
	}
	if a == nil {
		return false
	}
	_, ok := a.controllerPeerIDs[node.NodeID]
	return ok
}

func activeAliveDataNodes(nodes []controllermeta.ClusterNode) []controllermeta.ClusterNode {
	out := make([]controllermeta.ClusterNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Role == controllermeta.NodeRoleData && node.JoinState == controllermeta.NodeJoinStateActive && node.Status == controllermeta.NodeStatusAlive {
			out = append(out, node)
		}
	}
	return out
}

func scaleInRemainingAliveDataNodes(nodes []controllermeta.ClusterNode, nodeID uint64) []controllermeta.ClusterNode {
	alive := activeAliveDataNodes(nodes)
	out := alive[:0]
	for _, node := range alive {
		if node.NodeID != nodeID {
			out = append(out, node)
		}
	}
	return out
}

func scaleInOtherDrainingDataNode(nodes []controllermeta.ClusterNode, target uint64) (controllermeta.ClusterNode, bool) {
	for _, node := range nodes {
		if node.NodeID != target && node.Role == controllermeta.NodeRoleData && node.Status == controllermeta.NodeStatusDraining {
			return node, true
		}
	}
	return controllermeta.ClusterNode{}, false
}

func scaleInNodeActiveOrDraining(node controllermeta.ClusterNode) bool {
	if node.JoinState != controllermeta.NodeJoinStateActive {
		return false
	}
	return node.Status == controllermeta.NodeStatusAlive || node.Status == controllermeta.NodeStatusDraining
}

func scaleInAssignmentContains(assignment controllermeta.SlotAssignment, nodeID uint64) bool {
	for _, peer := range assignment.DesiredPeers {
		if peer == nodeID {
			return true
		}
	}
	return false
}

func scaleInViewContains(view controllermeta.SlotRuntimeView, nodeID uint64) bool {
	for _, peer := range view.CurrentPeers {
		if peer == nodeID {
			return true
		}
	}
	return false
}

func scaleInTaskInvolves(task controllermeta.ReconcileTask, nodeID uint64) bool {
	return task.SourceNode == nodeID || task.TargetNode == nodeID
}

func scaleInTaskActive(task controllermeta.ReconcileTask) bool {
	return task.Status == controllermeta.TaskStatusPending || task.Status == controllermeta.TaskStatusRetrying
}

func scaleInTaskFailed(task controllermeta.ReconcileTask) bool {
	return task.Status == controllermeta.TaskStatusFailed
}

func scaleInOnboardingJobRunning(job controllermeta.NodeOnboardingJob) bool {
	return job.Status == controllermeta.OnboardingJobStatusRunning
}

func scaleInRunningOnboardingJobs(jobs []controllermeta.NodeOnboardingJob) int {
	count := 0
	for _, job := range jobs {
		if scaleInOnboardingJobRunning(job) {
			count++
		}
	}
	return count
}

func scaleInTaskCounts(tasks []controllermeta.ReconcileTask, nodeID uint64) (activeTargetTasks int, failedTasks int) {
	for _, task := range tasks {
		if scaleInTaskFailed(task) {
			failedTasks++
		}
		if scaleInTaskActive(task) && scaleInTaskInvolves(task, nodeID) {
			activeTargetTasks++
		}
	}
	return activeTargetTasks, failedTasks
}

func scaleInRuntimeViewsCompleteAndFresh(now time.Time, maxAge time.Duration, assignments []controllermeta.SlotAssignment, views []controllermeta.SlotRuntimeView) bool {
	viewBySlot := scaleInRuntimeViewBySlot(views)
	for _, assignment := range assignments {
		view, ok := viewBySlot[assignment.SlotID]
		if !ok || view.LastReportAt.IsZero() {
			return false
		}
		if now.Sub(view.LastReportAt) > maxAge {
			return false
		}
		if view.LastReportAt.Sub(now) > scaleInFutureReportTolerance {
			return false
		}
	}
	return true
}

func scaleInRuntimeViewBySlot(views []controllermeta.SlotRuntimeView) map[uint32]controllermeta.SlotRuntimeView {
	bySlot := make(map[uint32]controllermeta.SlotRuntimeView, len(views))
	for _, view := range views {
		bySlot[view.SlotID] = view
	}
	return bySlot
}

func scaleInAssignmentBySlot(assignments []controllermeta.SlotAssignment) map[uint32]controllermeta.SlotAssignment {
	bySlot := make(map[uint32]controllermeta.SlotAssignment, len(assignments))
	for _, assignment := range assignments {
		bySlot[assignment.SlotID] = assignment
	}
	return bySlot
}

func clampScaleInLeaderTransfers(limit int) int {
	if limit <= 0 {
		return scaleInDefaultLeaderTransfers
	}
	if limit > scaleInMaxLeaderTransfers {
		return scaleInMaxLeaderTransfers
	}
	return limit
}

func selectScaleInLeaderCandidate(nodes []controllermeta.ClusterNode, assignments []controllermeta.SlotAssignment, view controllermeta.SlotRuntimeView, target uint64) (uint64, bool) {
	eligible := scaleInEligibleLeaderCandidates(nodes, target)
	assignment, _ := scaleInAssignmentBySlot(assignments)[view.SlotID]
	for _, peer := range assignment.DesiredPeers {
		if _, ok := eligible[peer]; ok {
			return peer, true
		}
	}
	for _, peer := range view.CurrentPeers {
		if _, ok := eligible[peer]; ok {
			return peer, true
		}
	}
	return 0, false
}

func scaleInEligibleLeaderCandidates(nodes []controllermeta.ClusterNode, target uint64) map[uint64]struct{} {
	eligible := make(map[uint64]struct{})
	for _, node := range nodes {
		if node.NodeID == target {
			continue
		}
		if node.Role != controllermeta.NodeRoleData || node.JoinState != controllermeta.NodeJoinStateActive || node.Status != controllermeta.NodeStatusAlive {
			continue
		}
		eligible[node.NodeID] = struct{}{}
	}
	return eligible
}

func scaleInAssignedReplicaCount(assignments []controllermeta.SlotAssignment, nodeID uint64) int {
	count := 0
	for _, assignment := range assignments {
		if scaleInAssignmentContains(assignment, nodeID) {
			count++
		}
	}
	return count
}

func scaleInObservedReplicaCount(views []controllermeta.SlotRuntimeView, nodeID uint64) int {
	count := 0
	for _, view := range views {
		if scaleInViewContains(view, nodeID) {
			count++
		}
	}
	return count
}

func scaleInLeaderCount(views []controllermeta.SlotRuntimeView, nodeID uint64) int {
	count := 0
	for _, view := range views {
		if view.LeaderID == nodeID {
			count++
		}
	}
	return count
}

func scaleInStatus(target controllermeta.ClusterNode, targetFound bool, reasons []NodeScaleInBlockedReason, progress NodeScaleInProgress, connectionSafetyVerified bool) NodeScaleInStatus {
	if len(reasons) > 0 {
		return NodeScaleInStatusBlocked
	}
	if !targetFound || target.Status != controllermeta.NodeStatusDraining {
		return NodeScaleInStatusNotStarted
	}
	if progress.AssignedSlotReplicas > 0 || progress.ObservedSlotReplicas > 0 || progress.ActiveTasksInvolvingNode > 0 {
		return NodeScaleInStatusMigratingReplicas
	}
	if progress.SlotLeaders > 0 {
		return NodeScaleInStatusTransferringLeaders
	}
	if !connectionSafetyVerified || progress.ActiveConnections > 0 || progress.ClosingConnections > 0 || progress.GatewaySessions > 0 {
		return NodeScaleInStatusWaitingConnections
	}
	return NodeScaleInStatusReadyToRemove
}
