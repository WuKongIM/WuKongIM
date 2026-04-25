package cluster

import (
	"context"
	"sort"
	"sync"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
)

type assignmentTaskState struct {
	assignment controllermeta.SlotAssignment
	task       controllermeta.ReconcileTask
}

type assignmentReconciler interface {
	Tick(context.Context) error
}

type slotAgent struct {
	cluster    *Cluster
	client     controllerAPI
	cache      *assignmentCache
	reports    pendingTaskReports
	reconciler assignmentReconciler

	observationMu         sync.RWMutex
	observationState      observationAppliedState
	pendingReconcileScope map[uint32]struct{}
}

type pendingTaskReport struct {
	task    controllermeta.ReconcileTask
	taskErr error
}

type pendingTaskReports struct {
	mu sync.Mutex
	m  map[uint32]pendingTaskReport
}

func (a *slotAgent) HeartbeatOnce(ctx context.Context) error {
	if a == nil || a.cluster == nil || a.client == nil {
		return ErrNotStarted
	}
	now := time.Now()
	return a.client.Report(ctx, slotcontrollerReport(a.cluster, now, nil))
}

func (a *slotAgent) SyncAssignments(ctx context.Context) error {
	if a == nil || a.client == nil {
		return ErrNotStarted
	}
	var assignments []controllermeta.SlotAssignment
	err := a.retryControllerCall(ctx, func(attemptCtx context.Context) error {
		var err error
		assignments, err = a.client.RefreshAssignments(attemptCtx)
		return err
	})
	if err == nil {
		if a.cache != nil {
			a.cache.SetAssignments(assignments)
		}
		a.setPendingReconcileScope(nil)
		return nil
	}
	if !controllerReadFallbackAllowed(err) || a.cluster == nil || a.cluster.controllerMeta == nil {
		return err
	}
	assignments, err = a.cluster.controllerMeta.ListAssignments(ctx)
	if err != nil {
		return err
	}
	if err := a.cluster.syncRouterHashSlotTableFromStore(ctx); err != nil {
		return err
	}
	if a.cache != nil {
		a.cache.SetAssignments(assignments)
	}
	a.setPendingReconcileScope(nil)
	return nil
}

func (a *slotAgent) SyncObservationDelta(ctx context.Context, hint observationHint) error {
	if a == nil || a.client == nil {
		return ErrNotStarted
	}

	req := observationDeltaRequest{
		LeaderID:         a.appliedObservationLeaderID(),
		LeaderGeneration: a.appliedObservationLeaderGeneration(),
		Revisions:        a.appliedObservationRevisions(),
		RequestedSlots:   append([]uint32(nil), hint.AffectedSlots...),
		ForceFullSync:    hint.NeedFullSync,
	}
	delta, err := a.client.FetchObservationDelta(ctx, req)
	if err != nil {
		return err
	}

	a.observationMu.Lock()
	beforeNodes := cloneObservationNodesByID(a.observationState.Nodes)
	applyObservationDelta(&a.observationState, delta)
	nodeStatusChanges := diffObservationNodeStatuses(beforeNodes, a.observationState.Nodes)
	a.pendingReconcileScope = requestedSlotSet(reconcileScopeFromObservationDelta(delta))
	assignments := sortedObservationAssignments(a.observationState.Assignments)
	a.observationMu.Unlock()

	if a.cache != nil {
		a.cache.SetAssignments(assignments)
	}
	if a.cluster != nil && !a.cluster.isLocalControllerLeader() {
		if hook := a.cluster.obs.OnNodeStatusChange; hook != nil {
			for _, change := range nodeStatusChanges {
				hook(change.nodeID, change.from, change.to)
			}
		}
	}
	return nil
}

func (a *slotAgent) ApplyAssignments(ctx context.Context) error {
	if a == nil || a.cluster == nil || a.client == nil || a.cache == nil {
		return ErrNotStarted
	}
	reconciler := a.assignmentReconciler()
	if reconciler == nil {
		return ErrNotStarted
	}
	return reconciler.Tick(ctx)
}

func (a *slotAgent) assignmentReconciler() assignmentReconciler {
	if a == nil {
		return nil
	}
	if a.reconciler == nil {
		a.reconciler = newReconciler(a)
	}
	return a.reconciler
}

func (a *slotAgent) reportTaskResult(ctx context.Context, task controllermeta.ReconcileTask, taskErr error) error {
	if a == nil || a.cluster == nil || a.client == nil {
		return ErrNotStarted
	}
	err := a.cluster.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
		if a.cluster.isLocalControllerLeader() {
			return a.cluster.proposeTaskResultOnLeader(attemptCtx, task, taskErr)
		}
		return a.client.ReportTaskResult(attemptCtx, task, taskErr)
	})
	if err == nil {
		if hook := a.cluster.obs.OnTaskResult; hook != nil {
			hook(task.SlotID, controllerTaskKindName(task.Kind), controllerTaskResult(taskErr))
		}
	}
	return err
}

func (a *slotAgent) listControllerNodes(ctx context.Context) ([]controllermeta.ClusterNode, error) {
	if a == nil || a.client == nil {
		return nil, ErrNotStarted
	}
	if a.cluster != nil && a.cluster.controllerHost != nil && a.cluster.controllerHost.IsLeader(a.cluster.cfg.NodeID) {
		if snapshot, ok := a.cluster.controllerHost.metadataSnapshot(); ok {
			return snapshot.Nodes, nil
		}
	}
	if nodes, ok := a.appliedObservationNodes(); ok {
		return nodes, nil
	}
	var nodes []controllermeta.ClusterNode
	err := a.retryControllerCall(ctx, func(attemptCtx context.Context) error {
		var err error
		nodes, err = a.client.ListNodes(attemptCtx)
		return err
	})
	if err == nil || !controllerReadFallbackAllowed(err) || a.cluster == nil || a.cluster.controllerMeta == nil {
		return nodes, err
	}
	return a.cluster.controllerMeta.ListNodes(ctx)
}

func (a *slotAgent) listRuntimeViews(ctx context.Context) ([]controllermeta.SlotRuntimeView, bool, error) {
	if a == nil || a.client == nil {
		return nil, false, ErrNotStarted
	}
	if views, ok := a.appliedObservationRuntimeViews(); ok {
		return views, true, nil
	}
	var views []controllermeta.SlotRuntimeView
	err := a.retryControllerCall(ctx, func(attemptCtx context.Context) error {
		var err error
		views, err = a.client.ListRuntimeViews(attemptCtx)
		return err
	})
	if err == nil || !controllerReadFallbackAllowed(err) || a.cluster == nil || a.cluster.controllerMeta == nil {
		return views, err == nil, err
	}
	views, err = a.cluster.controllerMeta.ListRuntimeViews(ctx)
	return views, false, err
}

func (a *slotAgent) listTasks(ctx context.Context) ([]controllermeta.ReconcileTask, error) {
	if a == nil || a.client == nil {
		return nil, ErrNotStarted
	}
	if a.cluster != nil && a.cluster.controllerHost != nil && a.cluster.controllerHost.IsLeader(a.cluster.cfg.NodeID) {
		if snapshot, ok := a.cluster.controllerHost.metadataSnapshot(); ok {
			return snapshot.Tasks, nil
		}
	}
	if a.cluster != nil && a.cluster.controllerMeta != nil && a.cluster.isLocalControllerLeader() {
		return a.cluster.controllerMeta.ListTasks(ctx)
	}
	if tasks, ok := a.appliedObservationTasks(); ok {
		return tasks, nil
	}
	var tasks []controllermeta.ReconcileTask
	err := a.retryControllerCall(ctx, func(attemptCtx context.Context) error {
		var err error
		tasks, err = a.client.ListTasks(attemptCtx)
		return err
	})
	if err == nil || !controllerReadFallbackAllowed(err) || a.cluster == nil || a.cluster.controllerMeta == nil {
		return tasks, err
	}
	return a.cluster.controllerMeta.ListTasks(ctx)
}

func (a *slotAgent) getTask(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
	if a == nil || a.client == nil {
		return controllermeta.ReconcileTask{}, ErrNotStarted
	}
	if a.cluster != nil && a.cluster.controllerHost != nil && a.cluster.controllerHost.IsLeader(a.cluster.cfg.NodeID) {
		if snapshot, ok := a.cluster.controllerHost.metadataSnapshot(); ok {
			task, exists := snapshot.TasksBySlot[slotID]
			if !exists {
				return controllermeta.ReconcileTask{}, controllermeta.ErrNotFound
			}
			return task, nil
		}
	}
	if a.cluster != nil && a.cluster.controllerMeta != nil && a.cluster.isLocalControllerLeader() {
		return a.cluster.controllerMeta.GetTask(ctx, slotID)
	}
	var task controllermeta.ReconcileTask
	err := a.retryControllerCall(ctx, func(attemptCtx context.Context) error {
		var err error
		task, err = a.client.GetTask(attemptCtx, slotID)
		return err
	})
	if err == nil || !controllerReadFallbackAllowed(err) || a.cluster == nil || a.cluster.controllerMeta == nil || !a.cluster.isLocalControllerLeader() {
		return task, err
	}
	return a.cluster.controllerMeta.GetTask(ctx, slotID)
}

func (a *slotAgent) retryControllerCall(ctx context.Context, fn func(context.Context) error) error {
	if a == nil || a.client == nil {
		return ErrNotStarted
	}
	if a.cluster != nil {
		return a.cluster.retryControllerCommand(ctx, fn)
	}
	return fn(ctx)
}

func (a *slotAgent) appliedObservationRevisions() observationRevisions {
	if a == nil {
		return observationRevisions{}
	}
	a.observationMu.RLock()
	defer a.observationMu.RUnlock()
	return a.observationState.Revisions
}

func (a *slotAgent) appliedObservationLeaderID() uint64 {
	if a == nil {
		return 0
	}
	a.observationMu.RLock()
	defer a.observationMu.RUnlock()
	return a.observationState.LeaderID
}

func (a *slotAgent) appliedObservationLeaderGeneration() uint64 {
	if a == nil {
		return 0
	}
	a.observationMu.RLock()
	defer a.observationMu.RUnlock()
	return a.observationState.LeaderGeneration
}

func (a *slotAgent) appliedObservationNodes() ([]controllermeta.ClusterNode, bool) {
	if a == nil {
		return nil, false
	}
	a.observationMu.RLock()
	defer a.observationMu.RUnlock()
	if a.observationState.LeaderGeneration == 0 {
		return nil, false
	}
	return sortedObservationNodes(a.observationState.Nodes), true
}

func (a *slotAgent) appliedObservationRuntimeViews() ([]controllermeta.SlotRuntimeView, bool) {
	if a == nil {
		return nil, false
	}
	a.observationMu.RLock()
	defer a.observationMu.RUnlock()
	if a.observationState.LeaderGeneration == 0 {
		return nil, false
	}
	return sortedObservationRuntimeViews(a.observationState.RuntimeViews), true
}

func (a *slotAgent) appliedObservationTasks() ([]controllermeta.ReconcileTask, bool) {
	if a == nil {
		return nil, false
	}
	a.observationMu.RLock()
	defer a.observationMu.RUnlock()
	if a.observationState.LeaderGeneration == 0 {
		return nil, false
	}
	return sortedObservationTasks(a.observationState.Tasks), true
}

func (a *slotAgent) setPendingReconcileScope(slotIDs []uint32) {
	if a == nil {
		return
	}
	a.observationMu.Lock()
	defer a.observationMu.Unlock()
	a.pendingReconcileScope = requestedSlotSet(slotIDs)
}

func (a *slotAgent) takePendingReconcileScope() (map[uint32]struct{}, bool) {
	if a == nil {
		return nil, false
	}
	a.observationMu.Lock()
	defer a.observationMu.Unlock()
	if len(a.pendingReconcileScope) == 0 {
		return nil, false
	}
	scope := make(map[uint32]struct{}, len(a.pendingReconcileScope))
	for slotID := range a.pendingReconcileScope {
		scope[slotID] = struct{}{}
	}
	a.pendingReconcileScope = nil
	return scope, true
}

func (a *slotAgent) pendingTaskReport(slotID uint32) (pendingTaskReport, bool) {
	if a == nil {
		return pendingTaskReport{}, false
	}
	a.reports.mu.Lock()
	defer a.reports.mu.Unlock()
	if a.reports.m == nil {
		return pendingTaskReport{}, false
	}
	report, ok := a.reports.m[slotID]
	return report, ok
}

func (a *slotAgent) storePendingTaskReport(slotID uint32, task controllermeta.ReconcileTask, taskErr error) {
	if a == nil {
		return
	}
	a.reports.mu.Lock()
	defer a.reports.mu.Unlock()
	if a.reports.m == nil {
		a.reports.m = make(map[uint32]pendingTaskReport)
	}
	a.reports.m[slotID] = pendingTaskReport{task: task, taskErr: taskErr}
}

func (a *slotAgent) clearPendingTaskReport(slotID uint32) {
	if a == nil {
		return
	}
	a.reports.mu.Lock()
	defer a.reports.mu.Unlock()
	if a.reports.m == nil {
		return
	}
	delete(a.reports.m, slotID)
}

func sameReconcileTaskIdentity(left, right controllermeta.ReconcileTask) bool {
	return left.SlotID == right.SlotID &&
		left.Kind == right.Kind &&
		left.Step == right.Step &&
		left.SourceNode == right.SourceNode &&
		left.TargetNode == right.TargetNode &&
		left.Attempt == right.Attempt
}

func sortedObservationAssignments(assignments map[uint32]controllermeta.SlotAssignment) []controllermeta.SlotAssignment {
	if len(assignments) == 0 {
		return nil
	}
	out := make([]controllermeta.SlotAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		out = append(out, cloneSlotAssignment(assignment))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SlotID < out[j].SlotID })
	return out
}

func sortedObservationNodes(nodes map[uint64]controllermeta.ClusterNode) []controllermeta.ClusterNode {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]controllermeta.ClusterNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

func sortedObservationRuntimeViews(views map[uint32]controllermeta.SlotRuntimeView) []controllermeta.SlotRuntimeView {
	if len(views) == 0 {
		return nil
	}
	out := make([]controllermeta.SlotRuntimeView, 0, len(views))
	for _, view := range views {
		out = append(out, cloneRuntimeView(view))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SlotID < out[j].SlotID })
	return out
}

func sortedObservationTasks(tasks map[uint32]controllermeta.ReconcileTask) []controllermeta.ReconcileTask {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]controllermeta.ReconcileTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SlotID < out[j].SlotID })
	return out
}

func slotcontrollerReport(c *Cluster, now time.Time, view *controllermeta.SlotRuntimeView) slotcontroller.AgentReport {
	return slotcontroller.AgentReport{
		NodeID:               uint64(c.cfg.NodeID),
		Addr:                 c.controllerReportAddr(),
		ObservedAt:           now,
		CapacityWeight:       1,
		HashSlotTableVersion: c.HashSlotTableVersion(),
		Runtime:              view,
	}
}
