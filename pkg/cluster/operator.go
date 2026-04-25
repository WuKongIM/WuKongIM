package cluster

import (
	"context"
	"errors"
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type TaskStatus = controllermeta.TaskStatus

const (
	TaskStatusPending  = controllermeta.TaskStatusPending
	TaskStatusRetrying = controllermeta.TaskStatusRetrying
	TaskStatusFailed   = controllermeta.TaskStatusFailed
)

type RecoverStrategy uint8

const (
	RecoverStrategyLatestLiveReplica RecoverStrategy = iota + 1
)

func (c *Cluster) controllerLeaderWaitDuration() time.Duration {
	if c != nil && c.controllerLeaderWaitTimeout > 0 {
		return c.controllerLeaderWaitTimeout
	}
	return c.timeoutConfig().ControllerLeaderWait
}

func (c *Cluster) isLocalControllerLeader() bool {
	return c != nil && c.controller != nil && c.controller.LeaderID() == uint64(c.cfg.NodeID)
}

func (c *Cluster) MarkNodeDraining(ctx context.Context, nodeID uint64) error {
	if c.controllerClient == nil {
		return ErrNotStarted
	}
	return c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
		return c.controllerClient.Operator(attemptCtx, slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorMarkNodeDraining,
			NodeID: nodeID,
		})
	})
}

func (c *Cluster) ResumeNode(ctx context.Context, nodeID uint64) error {
	if c.controllerClient == nil {
		return ErrNotStarted
	}
	return c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
		return c.controllerClient.Operator(attemptCtx, slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorResumeNode,
			NodeID: nodeID,
		})
	})
}

func (c *Cluster) GetReconcileTask(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
	if c.controllerMeta != nil && c.isLocalControllerLeader() {
		return c.controllerMeta.GetTask(ctx, slotID)
	}
	if c.controllerClient != nil {
		var task controllermeta.ReconcileTask
		err := c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			var err error
			task, err = c.controllerClient.GetTask(attemptCtx, slotID)
			return err
		})
		if err == nil {
			return task, nil
		}
		if !controllerReadFallbackAllowed(err) || c.controllerMeta == nil || !c.isLocalControllerLeader() {
			return controllermeta.ReconcileTask{}, err
		}
		return c.controllerMeta.GetTask(ctx, slotID)
	}
	if c.controllerMeta != nil {
		return c.controllerMeta.GetTask(ctx, slotID)
	}
	return controllermeta.ReconcileTask{}, ErrNotStarted
}

// GetReconcileTaskStrict returns the controller leader's task detail without local fallback.
func (c *Cluster) GetReconcileTaskStrict(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
	if c != nil && c.controllerMeta != nil && c.isLocalControllerLeader() {
		return c.controllerMeta.GetTask(ctx, slotID)
	}
	if c != nil && c.controllerClient != nil {
		var task controllermeta.ReconcileTask
		err := c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			var err error
			task, err = c.controllerClient.GetTask(attemptCtx, slotID)
			return err
		})
		if err != nil {
			return controllermeta.ReconcileTask{}, err
		}
		return task, nil
	}
	return controllermeta.ReconcileTask{}, ErrNotStarted
}

func (c *Cluster) ForceReconcile(ctx context.Context, slotID uint32) error {
	if c.controllerClient != nil || c.controller != nil {
		return c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			if c.isLocalControllerLeader() {
				return c.forceReconcileOnLeader(attemptCtx, slotID)
			}
			if c.controllerClient == nil {
				return ErrNotLeader
			}
			return c.controllerClient.ForceReconcile(attemptCtx, slotID)
		})
	}
	if c.isLocalControllerLeader() {
		return c.forceReconcileOnLeader(ctx, slotID)
	}
	return ErrNotStarted
}

func (c *Cluster) TransferSlotLeader(ctx context.Context, slotID uint32, nodeID multiraft.NodeID) error {
	return c.transferSlotLeadership(ctx, multiraft.SlotID(slotID), nodeID)
}

func (c *Cluster) RecoverSlot(ctx context.Context, slotID uint32, strategy RecoverStrategy) error {
	assignments, err := c.ListSlotAssignments(ctx)
	if err != nil {
		return err
	}
	return c.recoverSlotWithAssignments(ctx, slotID, strategy, assignments)
}

// RecoverSlotStrict runs slot recovery against controller-leader assignments only.
func (c *Cluster) RecoverSlotStrict(ctx context.Context, slotID uint32, strategy RecoverStrategy) error {
	assignments, err := c.ListSlotAssignmentsStrict(ctx)
	if err != nil {
		return err
	}
	return c.recoverSlotWithAssignments(ctx, slotID, strategy, assignments)
}

func (c *Cluster) recoverSlotWithAssignments(ctx context.Context, slotID uint32, strategy RecoverStrategy, assignments []controllermeta.SlotAssignment) error {
	if strategy != RecoverStrategyLatestLiveReplica {
		return ErrInvalidConfig
	}

	var peers []uint64
	for _, assignment := range assignments {
		if assignment.SlotID == slotID {
			peers = assignment.DesiredPeers
			break
		}
	}

	if len(peers) == 0 {
		return ErrSlotNotFound
	}

	reachable := 0
	for _, peer := range peers {
		if multiraft.NodeID(peer) == c.cfg.NodeID {
			if _, err := c.runtime.Status(multiraft.SlotID(slotID)); err == nil {
				reachable++
			}
			continue
		}

		body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
			Kind:   managedSlotRPCStatus,
			SlotID: slotID,
		})
		if err != nil {
			return err
		}
		respBody, err := c.RPCService(ctx, multiraft.NodeID(peer), multiraft.SlotID(slotID), rpcServiceManagedSlot, body)
		if err != nil {
			continue
		}
		if _, err := decodeManagedSlotResponse(respBody); err == nil {
			reachable++
		}
	}
	if reachable < len(peers)/2+1 {
		return ErrManualRecoveryRequired
	}
	return nil
}

func (c *Cluster) AddSlot(ctx context.Context) (multiraft.SlotID, error) {
	assignments, err := c.listSlotAssignmentsForOperator(ctx)
	if err != nil {
		return 0, err
	}

	newSlotID, peers, err := nextSlotDefinition(c, assignments)
	if err != nil {
		return 0, err
	}

	req := slotcontroller.AddSlotRequest{
		NewSlotID: uint64(newSlotID),
		Peers:     peers,
	}
	if err := c.submitAddSlot(ctx, req); err != nil {
		return 0, err
	}
	return newSlotID, nil
}

func (c *Cluster) RemoveSlot(ctx context.Context, slotID multiraft.SlotID) error {
	if slotID == 0 {
		return ErrInvalidConfig
	}
	table := c.GetHashSlotTable()
	if table == nil {
		return ErrNotStarted
	}
	if len(table.HashSlotsOf(slotID)) == 0 {
		return ErrSlotNotFound
	}
	for _, migration := range table.ActiveMigrations() {
		if migration.Source == slotID || migration.Target == slotID {
			return ErrInvalidConfig
		}
	}

	return c.submitRemoveSlot(ctx, slotcontroller.RemoveSlotRequest{
		SlotID: uint64(slotID),
	})
}

func (c *Cluster) Rebalance(ctx context.Context) ([]MigrationPlan, error) {
	table := c.GetHashSlotTable()
	if table == nil {
		return nil, ErrNotStarted
	}
	plan := ComputeRebalancePlan(table)
	for _, migration := range plan {
		if err := c.StartHashSlotMigration(ctx, migration.HashSlot, migration.To); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (c *Cluster) GetHashSlotTable() *HashSlotTable {
	if c == nil || c.router == nil {
		return nil
	}
	table := c.router.hashSlotTable.Load()
	if table == nil {
		return nil
	}
	return table.Clone()
}

func (c *Cluster) GetMigrationStatus() []HashSlotMigration {
	table := c.GetHashSlotTable()
	if table == nil {
		return nil
	}
	return table.ActiveMigrations()
}

func (c *Cluster) TransportPoolStats() []transport.PoolPeerStats {
	if c == nil {
		return nil
	}

	merged := make(map[transport.NodeID]transport.PoolPeerStats)
	mergePoolStats := func(pool *transport.Pool) {
		if pool == nil {
			return
		}
		for _, stat := range pool.Stats() {
			current := merged[stat.NodeID]
			current.NodeID = stat.NodeID
			current.Active += stat.Active
			current.Idle += stat.Idle
			merged[stat.NodeID] = current
		}
	}

	mergePoolStats(c.raftPool)
	mergePoolStats(c.rpcPool)

	stats := make([]transport.PoolPeerStats, 0, len(merged))
	for _, stat := range merged {
		stats = append(stats, stat)
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].NodeID < stats[j].NodeID
	})
	return stats
}

func nextSlotDefinition(c *Cluster, assignments []controllermeta.SlotAssignment) (multiraft.SlotID, []uint64, error) {
	var maxSlotID uint32
	var minSlotID uint32
	var peers []uint64
	for _, assignment := range assignments {
		if assignment.SlotID > maxSlotID {
			maxSlotID = assignment.SlotID
		}
		if len(peers) == 0 || assignment.SlotID < minSlotID {
			minSlotID = assignment.SlotID
			peers = append([]uint64(nil), assignment.DesiredPeers...)
		}
	}
	if len(peers) == 0 && c != nil {
		if len(c.cfg.Slots) > 0 {
			for _, peer := range c.cfg.Slots[0].Peers {
				peers = append(peers, uint64(peer))
			}
		}
		if maxSlotID == 0 {
			for _, slotID := range c.SlotIDs() {
				if uint32(slotID) > maxSlotID {
					maxSlotID = uint32(slotID)
				}
			}
		}
	}
	if len(peers) == 0 || maxSlotID == 0 {
		return 0, nil, ErrSlotNotFound
	}
	return multiraft.SlotID(maxSlotID + 1), peers, nil
}

func (c *Cluster) listSlotAssignmentsForOperator(ctx context.Context) ([]controllermeta.SlotAssignment, error) {
	if c != nil && c.controllerMeta != nil && c.localControllerLeaderActive() {
		return c.controllerMeta.ListAssignments(ctx)
	}
	return c.ListSlotAssignments(ctx)
}

func (c *Cluster) localControllerLeaderActive() bool {
	return c != nil &&
		c.controller != nil &&
		c.controller.LeaderID() == uint64(c.cfg.NodeID)
}

func (c *Cluster) submitAddSlot(ctx context.Context, req slotcontroller.AddSlotRequest) error {
	if c == nil {
		return ErrNotStarted
	}
	if c.localControllerLeaderActive() {
		proposeCtx, cancel := c.withControllerTimeout(ctx)
		defer cancel()
		if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
			Kind:    slotcontroller.CommandKindAddSlot,
			AddSlot: &req,
		}); err != nil {
			if errors.Is(err, controllerraft.ErrNotLeader) {
				return ErrNotLeader
			}
			return err
		}
		return nil
	}
	if c.controllerClient != nil {
		return c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			return c.controllerClient.AddSlot(attemptCtx, req)
		})
	}
	if c.controller == nil {
		return ErrNotStarted
	}
	if !c.localControllerLeaderActive() {
		return ErrNotLeader
	}
	proposeCtx, cancel := c.withControllerTimeout(ctx)
	defer cancel()
	if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
		Kind:    slotcontroller.CommandKindAddSlot,
		AddSlot: &req,
	}); err != nil {
		if errors.Is(err, controllerraft.ErrNotLeader) {
			return ErrNotLeader
		}
		return err
	}
	return nil
}

func (c *Cluster) submitRemoveSlot(ctx context.Context, req slotcontroller.RemoveSlotRequest) error {
	if c == nil {
		return ErrNotStarted
	}
	if c.localControllerLeaderActive() {
		proposeCtx, cancel := c.withControllerTimeout(ctx)
		defer cancel()
		if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
			Kind:       slotcontroller.CommandKindRemoveSlot,
			RemoveSlot: &req,
		}); err != nil {
			if errors.Is(err, controllerraft.ErrNotLeader) {
				return ErrNotLeader
			}
			return err
		}
		return nil
	}
	if c.controllerClient != nil {
		return c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			return c.controllerClient.RemoveSlot(attemptCtx, req)
		})
	}
	if c.controller == nil {
		return ErrNotStarted
	}
	if !c.localControllerLeaderActive() {
		return ErrNotLeader
	}
	proposeCtx, cancel := c.withControllerTimeout(ctx)
	defer cancel()
	if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
		Kind:       slotcontroller.CommandKindRemoveSlot,
		RemoveSlot: &req,
	}); err != nil {
		if errors.Is(err, controllerraft.ErrNotLeader) {
			return ErrNotLeader
		}
		return err
	}
	return nil
}

func (c *Cluster) forceReconcileOnLeader(ctx context.Context, slotID uint32) error {
	if c.controller == nil || c.controllerMeta == nil {
		return ErrNotStarted
	}

	now := time.Now()
	assignment, assignmentErr := c.controllerMeta.GetAssignment(ctx, slotID)
	task, taskErr := c.controllerMeta.GetTask(ctx, slotID)

	switch {
	case taskErr == nil:
		var assignmentPtr *controllermeta.SlotAssignment
		switch {
		case assignmentErr == nil:
			assignmentPtr = &assignment
		case !errors.Is(assignmentErr, controllermeta.ErrNotFound):
			return assignmentErr
		}
		task.Status = controllermeta.TaskStatusPending
		task.NextRunAt = now
		proposeCtx, cancel := c.withControllerTimeout(ctx)
		defer cancel()
		if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
			Kind:       slotcontroller.CommandKindAssignmentTaskUpdate,
			Assignment: assignmentPtr,
			Task:       &task,
		}); err != nil {
			if errors.Is(err, controllerraft.ErrNotLeader) {
				return ErrNotLeader
			}
			return err
		}
		return nil
	case !errors.Is(taskErr, controllermeta.ErrNotFound):
		return taskErr
	}

	state, err := c.snapshotPlannerState(ctx)
	if err != nil {
		return err
	}
	planner := slotcontroller.NewPlanner(slotcontroller.PlannerConfig{
		SlotCount: c.cfg.effectiveInitialSlotCount(),
		ReplicaN:  c.cfg.SlotReplicaN,
	})
	decision, err := planner.ReconcileSlot(ctx, state, slotID)
	if err != nil {
		return err
	}
	if decision.Task == nil {
		if errors.Is(assignmentErr, controllermeta.ErrNotFound) {
			return controllermeta.ErrNotFound
		}
		return nil
	}
	if decision.Task.Kind == controllermeta.TaskKindRebalance {
		return nil
	}

	proposeCtx, cancel := c.withControllerTimeout(ctx)
	defer cancel()
	if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
		Kind:       slotcontroller.CommandKindAssignmentTaskUpdate,
		Assignment: &decision.Assignment,
		Task:       decision.Task,
	}); err != nil {
		if errors.Is(err, controllerraft.ErrNotLeader) {
			return ErrNotLeader
		}
		return err
	}
	return nil
}

func (c *Cluster) proposeTaskResultOnLeader(ctx context.Context, task controllermeta.ReconcileTask, taskErr error) error {
	if c == nil || c.controller == nil {
		return ErrNotStarted
	}

	advance := &slotcontroller.TaskAdvance{
		SlotID:  task.SlotID,
		Attempt: task.Attempt,
		Now:     time.Now(),
	}
	if taskErr != nil {
		advance.Err = taskErr
	}

	proposeCtx, cancel := c.withControllerTimeout(ctx)
	defer cancel()
	if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
		Kind:    slotcontroller.CommandKindTaskResult,
		Advance: advance,
	}); err != nil {
		if errors.Is(err, controllerraft.ErrNotLeader) {
			return ErrNotLeader
		}
		return err
	}
	return nil
}

func (c *Cluster) retryControllerCommand(ctx context.Context, fn func(context.Context) error) error {
	return Retry{
		Interval:    c.controllerRetryInterval(),
		MaxWait:     c.controllerLeaderWaitDuration(),
		IsRetryable: controllerCommandRetryAllowed,
	}.Do(ctx, fn)
}
