package cluster

import (
	"context"
	"fmt"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type slotExecutor struct {
	cluster                    *Cluster
	prepareSlot                func(multiraft.SlotID, []uint64)
	waitForLeader              func(context.Context, multiraft.SlotID) error
	changeSlotConfig           func(context.Context, multiraft.SlotID, multiraft.ConfigChange) error
	waitForCatchUp             func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	ensureBootstrapLeader      func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	ensureLeaderMovedOffSource func(context.Context, multiraft.SlotID, multiraft.NodeID, multiraft.NodeID) error
	managedSlotExecutionHook   func() ManagedSlotExecutionTestHook
}

type slotExecutorFuncs struct {
	prepareSlot                func(multiraft.SlotID, []uint64)
	waitForLeader              func(context.Context, multiraft.SlotID) error
	changeSlotConfig           func(context.Context, multiraft.SlotID, multiraft.ConfigChange) error
	waitForCatchUp             func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	ensureBootstrapLeader      func(context.Context, multiraft.SlotID, multiraft.NodeID) error
	ensureLeaderMovedOffSource func(context.Context, multiraft.SlotID, multiraft.NodeID, multiraft.NodeID) error
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
	e.prepareSlot(slotID, assignment.assignment.DesiredPeers)
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
		if err := e.waitForLeader(ctx, slotID); err != nil {
			return err
		}
		return e.ensureBootstrapLeader(ctx, slotID, multiraft.NodeID(assignment.task.TargetNode))
	case controllermeta.TaskKindRepair, controllermeta.TaskKindRebalance:
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
		return fmt.Errorf("%w: leader transfer task execution is not implemented", ErrInvalidConfig)
	default:
		return nil
	}
}
