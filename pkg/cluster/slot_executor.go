package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

var ErrLeaderTransferSafetyCheck = errors.New("raftcluster: leader transfer safety check failed")

type slotExecutor struct {
	cluster                    *Cluster
	prepareSlot                func(multiraft.SlotID, []uint64)
	waitForLeader              func(context.Context, multiraft.SlotID) error
	changeSlotConfig           func(context.Context, multiraft.SlotID, multiraft.ConfigChange) error
	waitForCatchUp             func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	ensureBootstrapLeader      func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	ensureLeaderMovedOffSource func(context.Context, multiraft.SlotID, multiraft.NodeID, multiraft.NodeID) error
	transferLeadershipFrom     func(context.Context, multiraft.SlotID, multiraft.NodeID, multiraft.NodeID) error
	waitForSpecificLeader      func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	managedSlotExecutionHook   func() ManagedSlotExecutionTestHook
}

type slotExecutorFuncs struct {
	prepareSlot                func(multiraft.SlotID, []uint64)
	waitForLeader              func(context.Context, multiraft.SlotID) error
	changeSlotConfig           func(context.Context, multiraft.SlotID, multiraft.ConfigChange) error
	waitForCatchUp             func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	ensureBootstrapLeader      func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	ensureLeaderMovedOffSource func(context.Context, multiraft.SlotID, multiraft.NodeID, multiraft.NodeID) error
	transferLeadership         func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	transferLeadershipFrom     func(context.Context, multiraft.SlotID, multiraft.NodeID, multiraft.NodeID) error
	waitForSpecificLeader      func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	managedSlotExecutionHook   func() ManagedSlotExecutionTestHook
}

func newSlotExecutor(cluster *Cluster) *slotExecutor {
	return newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{})
}

func newSlotExecutorWithFuncs(cluster *Cluster, funcs slotExecutorFuncs) *slotExecutor {
	executor := &slotExecutor{
		cluster: cluster,
	}
	executor.prepareSlot = funcs.prepareSlot
	if executor.prepareSlot == nil {
		executor.prepareSlot = func(slotID multiraft.SlotID, desiredPeers []uint64) {
			cluster.setRuntimePeers(slotID, cluster.runtimePeersForLocalSlot(slotID, desiredPeers))
		}
	}
	executor.waitForLeader = funcs.waitForLeader
	if executor.waitForLeader == nil {
		executor.waitForLeader = cluster.managedSlots().waitForLeader
	}
	executor.changeSlotConfig = funcs.changeSlotConfig
	if executor.changeSlotConfig == nil {
		executor.changeSlotConfig = cluster.managedSlots().changeConfig
	}
	executor.waitForCatchUp = funcs.waitForCatchUp
	if executor.waitForCatchUp == nil {
		executor.waitForCatchUp = cluster.managedSlots().waitForCatchUp
	}
	executor.ensureBootstrapLeader = funcs.ensureBootstrapLeader
	if executor.ensureBootstrapLeader == nil {
		executor.ensureBootstrapLeader = cluster.managedSlots().ensureLeaderOnTarget
	}
	executor.ensureLeaderMovedOffSource = funcs.ensureLeaderMovedOffSource
	if executor.ensureLeaderMovedOffSource == nil {
		executor.ensureLeaderMovedOffSource = cluster.managedSlots().ensureLeaderMovedOffSource
	}
	executor.transferLeadershipFrom = funcs.transferLeadershipFrom
	if executor.transferLeadershipFrom == nil {
		executor.transferLeadershipFrom = cluster.managedSlots().transferLeadershipFrom
	}
	if funcs.transferLeadershipFrom == nil && funcs.transferLeadership != nil {
		executor.transferLeadershipFrom = func(ctx context.Context, slotID multiraft.SlotID, _ multiraft.NodeID, target multiraft.NodeID) error {
			return funcs.transferLeadership(ctx, slotID, target)
		}
	}
	executor.waitForSpecificLeader = funcs.waitForSpecificLeader
	if executor.waitForSpecificLeader == nil {
		executor.waitForSpecificLeader = cluster.managedSlots().waitForSpecificLeader
	}
	executor.managedSlotExecutionHook = funcs.managedSlotExecutionHook
	if executor.managedSlotExecutionHook == nil {
		executor.managedSlotExecutionHook = func() ManagedSlotExecutionTestHook {
			cluster.managedSlotHooks.mu.RLock()
			hook := cluster.managedSlotHooks.execution
			cluster.managedSlotHooks.mu.RUnlock()
			return hook
		}
	}
	return executor
}

func (e *slotExecutor) Execute(ctx context.Context, assignment assignmentTaskState) (err error) {
	if e == nil || e.cluster == nil {
		return ErrNotStarted
	}

	slotID := multiraft.SlotID(assignment.assignment.SlotID)
	start := time.Now()
	defer func() {
		if hook := e.cluster.obs.OnReconcileStep; hook != nil {
			hook(uint32(slotID), reconcileStepName(assignment.task), observerElapsed(start), err)
		}
	}()

	hook := e.managedSlotExecutionHook()
	if hook != nil {
		if err = hook(uint32(slotID), assignment.task); err != nil {
			return err
		}
	}

	switch assignment.task.Kind {
	case controllermeta.TaskKindBootstrap:
		e.prepareSlot(slotID, assignment.assignment.DesiredPeers)
		if err := e.waitForLeader(ctx, slotID); err != nil {
			return err
		}
		return e.ensureBootstrapLeader(ctx, slotID, multiraft.NodeID(assignment.task.TargetNode))
	case controllermeta.TaskKindRepair, controllermeta.TaskKindRebalance:
		e.prepareSlot(slotID, assignment.assignment.DesiredPeers)
		if err := e.changeSlotConfig(ctx, slotID, multiraft.ConfigChange{
			Type:   multiraft.AddLearner,
			NodeID: multiraft.NodeID(assignment.task.TargetNode),
		}); err != nil {
			return err
		}
		if err := e.waitForCatchUp(ctx, slotID, multiraft.NodeID(assignment.task.TargetNode)); err != nil {
			return err
		}
		if err := e.changeSlotConfig(ctx, slotID, multiraft.ConfigChange{
			Type:   multiraft.PromoteLearner,
			NodeID: multiraft.NodeID(assignment.task.TargetNode),
		}); err != nil {
			return err
		}
		if err := e.waitForCatchUp(ctx, slotID, multiraft.NodeID(assignment.task.TargetNode)); err != nil {
			return err
		}
		if err := e.ensureLeaderMovedOffSource(
			ctx,
			slotID,
			multiraft.NodeID(assignment.task.SourceNode),
			multiraft.NodeID(assignment.task.TargetNode),
		); err != nil {
			return err
		}
		if assignment.task.SourceNode != 0 {
			if err := e.changeSlotConfig(ctx, slotID, multiraft.ConfigChange{
				Type:   multiraft.RemoveVoter,
				NodeID: multiraft.NodeID(assignment.task.SourceNode),
			}); err != nil {
				return err
			}
		}
		return nil
	case controllermeta.TaskKindLeaderTransfer:
		return e.executeLeaderTransfer(ctx, assignment, slotID)
	default:
		return nil
	}
}

func (e *slotExecutor) executeLeaderTransfer(ctx context.Context, assignment assignmentTaskState, slotID multiraft.SlotID) error {
	if !assignment.hasRuntimeView {
		return fmt.Errorf("%w: runtime view missing", ErrLeaderTransferSafetyCheck)
	}
	view := assignment.runtimeView
	if view.SlotID != 0 && view.SlotID != uint32(slotID) {
		return fmt.Errorf("%w: runtime view slot mismatch", ErrLeaderTransferSafetyCheck)
	}
	if view.LeaderID == 0 {
		return fmt.Errorf("%w: leader unknown", ErrLeaderTransferSafetyCheck)
	}
	if len(view.CurrentVoters) == 0 {
		return fmt.Errorf("%w: current voters unknown", ErrLeaderTransferSafetyCheck)
	}
	if !assignmentContainsPeer(view.CurrentVoters, view.LeaderID) {
		return fmt.Errorf("%w: observed leader is not a current voter", ErrLeaderTransferSafetyCheck)
	}
	if assignment.assignment.ConfigEpoch != 0 && view.ObservedConfigEpoch < assignment.assignment.ConfigEpoch {
		return fmt.Errorf("%w: runtime view config epoch is stale", ErrLeaderTransferSafetyCheck)
	}
	leaderNode, ok := assignment.nodes[view.LeaderID]
	if !ok || !controllerNodeEligibleForLeaderTransfer(leaderNode) {
		return fmt.Errorf("%w: observed leader node not eligible", ErrLeaderTransferSafetyCheck)
	}
	if localNodeID := uint64(e.cluster.cfg.NodeID); localNodeID != 0 && localNodeID != view.LeaderID {
		return fmt.Errorf("%w: executor is not observed leader", ErrLeaderTransferSafetyCheck)
	}
	targetNode := assignment.task.TargetNode
	if !assignmentContainsPeer(assignment.assignment.DesiredPeers, targetNode) {
		return fmt.Errorf("%w: target not in desired peers", ErrLeaderTransferSafetyCheck)
	}
	if !assignmentContainsPeer(view.CurrentVoters, targetNode) {
		return fmt.Errorf("%w: target not in current voters", ErrLeaderTransferSafetyCheck)
	}
	node, ok := assignment.nodes[targetNode]
	if !ok || !controllerNodeEligibleForLeaderTransfer(node) {
		return fmt.Errorf("%w: target node not eligible", ErrLeaderTransferSafetyCheck)
	}
	if view.LeaderID == targetNode {
		return nil
	}
	if err := e.transferLeadershipFrom(ctx, slotID, multiraft.NodeID(view.LeaderID), multiraft.NodeID(targetNode)); err != nil {
		return err
	}
	return e.waitForSpecificLeader(ctx, slotID, multiraft.NodeID(targetNode))
}

func controllerNodeEligibleForLeaderTransfer(node controllermeta.ClusterNode) bool {
	return node.Status == controllermeta.NodeStatusAlive &&
		node.Role == controllermeta.NodeRoleData &&
		node.JoinState == controllermeta.NodeJoinStateActive
}
