package cluster

import (
	"context"
	"errors"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type reconciler struct {
	agent *slotAgent
}

func newReconciler(agent *slotAgent) *reconciler {
	return &reconciler{agent: agent}
}

func (r *reconciler) Tick(ctx context.Context) error {
	if r == nil || r.agent == nil {
		return ErrNotStarted
	}
	a := r.agent
	if a.cluster == nil || a.client == nil || a.cache == nil {
		return ErrNotStarted
	}

	assignments := a.cache.Snapshot()
	scope, scoped := a.takePendingReconcileScope()
	if scoped {
		assignments = filterAssignmentsByScope(assignments, scope)
	}
	if len(assignments) == 0 && !scoped {
		return nil
	}
	now := time.Now()
	desiredLocalSlots := make(map[uint32]struct{}, len(assignments))
	nodeByID := make(map[uint64]controllermeta.ClusterNode)
	if nodes, err := a.listControllerNodes(ctx); err == nil {
		nodeByID = make(map[uint64]controllermeta.ClusterNode, len(nodes))
		for _, node := range nodes {
			nodeByID[node.NodeID] = node
		}
	}

	viewByGroup := make(map[uint32]controllermeta.SlotRuntimeView, len(assignments))
	liveRuntimeViews := false
	if views, live, err := a.listRuntimeViews(ctx); err == nil {
		liveRuntimeViews = live
		for _, view := range views {
			viewByGroup[view.SlotID] = view
		}
	}

	for _, assignment := range assignments {
		if !assignmentContainsPeer(assignment.DesiredPeers, uint64(a.cluster.cfg.NodeID)) {
			continue
		}
		desiredLocalSlots[assignment.SlotID] = struct{}{}
		_, hasView := viewByGroup[assignment.SlotID]
		if err := a.cluster.ensureManagedSlotLocal(
			ctx,
			multiraft.SlotID(assignment.SlotID),
			assignment.DesiredPeers,
			hasView,
			false,
		); err != nil {
			return err
		}
	}

	taskByGroup, err := r.loadTasks(ctx, assignments)
	if err != nil {
		return err
	}

	protectedSourceSlots := make(map[uint32]struct{}, len(taskByGroup))
	for _, assignment := range assignments {
		view, hasView := viewByGroup[assignment.SlotID]
		task, ok := taskByGroup[assignment.SlotID]
		hasLiveView := liveRuntimeViews && hasView
		if ok && r.sourceSlotShouldRemainOpen(task, view, hasLiveView) {
			if hasLiveView {
				a.cluster.setRuntimePeers(multiraft.SlotID(assignment.SlotID), nodeIDsFromUint64s(view.CurrentPeers))
			}
			protectedSourceSlots[assignment.SlotID] = struct{}{}
			if err := a.cluster.ensureManagedSlotLocal(
				ctx,
				multiraft.SlotID(assignment.SlotID),
				assignment.DesiredPeers,
				hasView,
				false,
			); err != nil {
				return err
			}
		}
	}
	for _, slotID := range a.cluster.runtime.Slots() {
		if scoped && !slotRequested(scope, uint32(slotID)) {
			continue
		}
		if _, ok := desiredLocalSlots[uint32(slotID)]; ok {
			continue
		}
		if _, ok := protectedSourceSlots[uint32(slotID)]; ok {
			continue
		}
		if err := a.cluster.runtime.CloseSlot(ctx, slotID); err != nil && !errors.Is(err, multiraft.ErrSlotNotFound) {
			if hook := a.cluster.obs.OnSlotEnsure; hook != nil {
				hook(uint32(slotID), "close", err)
			}
			return err
		}
		if hook := a.cluster.obs.OnSlotEnsure; hook != nil {
			hook(uint32(slotID), "close", nil)
		}
		a.cluster.deleteRuntimePeers(slotID)
	}

	for _, assignment := range assignments {
		task, ok := taskByGroup[assignment.SlotID]
		if !ok {
			a.clearPendingTaskReport(assignment.SlotID)
			continue
		}
		_, hasView := viewByGroup[assignment.SlotID]
		bootstrapAuthorized := task.Kind == controllermeta.TaskKindBootstrap &&
			reconcileTaskRunnable(now, task)
		if bootstrapAuthorized {
			if err := a.cluster.ensureManagedSlotLocal(
				ctx,
				multiraft.SlotID(assignment.SlotID),
				assignment.DesiredPeers,
				hasView,
				true,
			); err != nil {
				return err
			}
		}
		localAssigned := assignmentContainsPeer(assignment.DesiredPeers, uint64(a.cluster.cfg.NodeID))
		view, hasView := viewByGroup[assignment.SlotID]
		localSourceProtected := r.sourceSlotShouldRemainOpen(task, view, liveRuntimeViews && hasView)
		if !localAssigned && !localSourceProtected {
			a.clearPendingTaskReport(assignment.SlotID)
			continue
		}
		if pending, ok := a.pendingTaskReport(assignment.SlotID); ok {
			if !sameReconcileTaskIdentity(pending.task, task) {
				a.clearPendingTaskReport(assignment.SlotID)
			} else {
				reportErr := a.reportTaskResult(ctx, pending.task, pending.taskErr)
				if reportErr != nil {
					return reportErr
				}
				a.clearPendingTaskReport(assignment.SlotID)
				continue
			}
		}
		runnable := reconcileTaskRunnable(now, task)
		shouldExecute := r.shouldExecuteTask(assignment, task, nodeByID)
		if !runnable || !shouldExecute {
			continue
		}
		freshTask, err := a.getTask(ctx, assignment.SlotID)
		switch {
		case errors.Is(err, controllermeta.ErrNotFound):
			continue
		case err != nil:
			if !controllerReadFallbackAllowed(err) {
				return err
			}
			// Reuse the task snapshot we already loaded earlier in this tick when
			// the confirm read only failed transiently. Skipping here can stall
			// retry-driven repair tasks indefinitely because no execution means no
			// task result is ever reported back to the controller.
			freshTask = task
		case !sameReconcileTaskIdentity(freshTask, task):
			continue
		}
		task = freshTask
		execErr := a.cluster.executeReconcileTask(ctx, assignmentTaskState{
			assignment: assignment,
			task:       task,
		})
		reportErr := a.reportTaskResult(ctx, task, execErr)
		if reportErr != nil {
			a.storePendingTaskReport(assignment.SlotID, task, execErr)
			return reportErr
		}
		a.clearPendingTaskReport(assignment.SlotID)
	}
	return nil
}

func filterAssignmentsByScope(assignments []controllermeta.SlotAssignment, scope map[uint32]struct{}) []controllermeta.SlotAssignment {
	if len(assignments) == 0 || len(scope) == 0 {
		return assignments
	}
	filtered := make([]controllermeta.SlotAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		if !slotRequested(scope, assignment.SlotID) {
			continue
		}
		filtered = append(filtered, assignment)
	}
	return filtered
}

type reconcileTaskLoadResult struct {
	slotID uint32
	task   controllermeta.ReconcileTask
	err    error
}

func (r *reconciler) loadTasks(ctx context.Context, assignments []controllermeta.SlotAssignment) (map[uint32]controllermeta.ReconcileTask, error) {
	taskByGroup := make(map[uint32]controllermeta.ReconcileTask, len(assignments))
	if r == nil || r.agent == nil {
		return taskByGroup, ErrNotStarted
	}
	if len(assignments) == 0 {
		return taskByGroup, nil
	}

	assignedSlots := make(map[uint32]struct{}, len(assignments))
	for _, assignment := range assignments {
		assignedSlots[assignment.SlotID] = struct{}{}
		if pending, ok := r.agent.pendingTaskReport(assignment.SlotID); ok {
			taskByGroup[assignment.SlotID] = pending.task
		}
	}

	tasks, err := r.agent.listTasks(ctx)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if _, ok := assignedSlots[task.SlotID]; !ok {
			continue
		}
		if _, pending := taskByGroup[task.SlotID]; pending {
			continue
		}
		taskByGroup[task.SlotID] = task
	}
	return taskByGroup, nil
}

func (r *reconciler) shouldExecuteTask(assignment controllermeta.SlotAssignment, task controllermeta.ReconcileTask, nodes map[uint64]controllermeta.ClusterNode) bool {
	if len(assignment.DesiredPeers) == 0 || r == nil || r.agent == nil || r.agent.cluster == nil {
		return false
	}
	localNodeID := uint64(r.agent.cluster.cfg.NodeID)
	switch task.Kind {
	case controllermeta.TaskKindRepair, controllermeta.TaskKindRebalance:
		if task.SourceNode == localNodeID {
			return true
		}
		if r.shouldPreferSourceTaskExecutor(task, nodes) {
			return false
		}
		leaderID, err := r.agent.cluster.currentManagedSlotLeader(multiraft.SlotID(assignment.SlotID))
		if err == nil {
			return uint64(leaderID) == localNodeID
		}
	}

	minPeer := uint64(0)
	for _, peer := range assignment.DesiredPeers {
		node, ok := nodes[peer]
		if ok && node.Status != controllermeta.NodeStatusAlive {
			continue
		}
		if minPeer == 0 || peer < minPeer {
			minPeer = peer
		}
	}
	if minPeer == 0 {
		minPeer = assignment.DesiredPeers[0]
		for _, peer := range assignment.DesiredPeers[1:] {
			if peer < minPeer {
				minPeer = peer
			}
		}
	}
	return minPeer == localNodeID
}

func (r *reconciler) shouldPreferSourceTaskExecutor(task controllermeta.ReconcileTask, nodes map[uint64]controllermeta.ClusterNode) bool {
	if task.SourceNode == 0 || len(nodes) == 0 {
		return false
	}
	node, ok := nodes[task.SourceNode]
	if !ok {
		return false
	}
	switch node.Status {
	case controllermeta.NodeStatusAlive, controllermeta.NodeStatusDraining:
		return true
	default:
		return false
	}
}

func (r *reconciler) taskKeepsSourceGroupOpen(task controllermeta.ReconcileTask) bool {
	if r == nil || r.agent == nil || r.agent.cluster == nil {
		return false
	}
	localNodeID := uint64(r.agent.cluster.cfg.NodeID)
	if task.SourceNode == 0 || task.SourceNode != localNodeID {
		return false
	}
	switch task.Kind {
	case controllermeta.TaskKindRepair, controllermeta.TaskKindRebalance:
		return true
	default:
		return false
	}
}

func (r *reconciler) sourceSlotShouldRemainOpen(task controllermeta.ReconcileTask, view controllermeta.SlotRuntimeView, hasView bool) bool {
	if !r.taskKeepsSourceGroupOpen(task) {
		return false
	}
	localNodeID := uint64(r.agent.cluster.cfg.NodeID)
	if hasView {
		return assignmentContainsPeer(view.CurrentPeers, localNodeID)
	}
	if peers, ok := r.agent.cluster.getRuntimePeers(multiraft.SlotID(task.SlotID)); ok {
		return nodeIDsContain(peers, multiraft.NodeID(localNodeID))
	}
	return false
}
