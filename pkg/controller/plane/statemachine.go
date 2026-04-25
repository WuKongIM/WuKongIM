package plane

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type StateMachineConfig struct {
	SuspectTimeout   time.Duration
	DeadTimeout      time.Duration
	MaxTaskAttempts  int
	RetryBackoffBase time.Duration
}

type StateMachine struct {
	store *controllermeta.Store
	cfg   StateMachineConfig
}

const (
	defaultSuspectTimeout   = 3 * time.Second
	defaultDeadTimeout      = 10 * time.Second
	defaultRetryBackoffBase = time.Second
)

func NewStateMachine(store *controllermeta.Store, cfg StateMachineConfig) *StateMachine {
	if cfg.SuspectTimeout <= 0 {
		cfg.SuspectTimeout = defaultSuspectTimeout
	}
	if cfg.DeadTimeout <= 0 {
		cfg.DeadTimeout = defaultDeadTimeout
	}
	if cfg.MaxTaskAttempts <= 0 {
		cfg.MaxTaskAttempts = 3
	}
	if cfg.RetryBackoffBase <= 0 {
		cfg.RetryBackoffBase = defaultRetryBackoffBase
	}
	return &StateMachine{store: store, cfg: cfg}
}

func (sm *StateMachine) Apply(ctx context.Context, cmd Command) error {
	if sm == nil || sm.store == nil {
		return controllermeta.ErrClosed
	}
	switch cmd.Kind {
	case CommandKindNodeHeartbeat:
		if cmd.Report == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyNodeHeartbeat(ctx, *cmd.Report)
	case CommandKindOperatorRequest:
		if cmd.Op == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyOperatorRequest(ctx, *cmd.Op)
	case CommandKindEvaluateTimeouts:
		if cmd.Advance == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyTimeoutEvaluation(ctx, cmd.Advance.Now)
	case CommandKindTaskResult:
		if cmd.Advance == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyTaskResult(ctx, *cmd.Advance)
	case CommandKindAssignmentTaskUpdate:
		return sm.applyAssignmentTaskUpdate(ctx, cmd.Assignment, cmd.Task)
	case CommandKindNodeStatusUpdate:
		if cmd.NodeStatusUpdate == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyNodeStatusUpdate(ctx, *cmd.NodeStatusUpdate)
	case CommandKindStartMigration:
		if cmd.Migration == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyStartMigration(ctx, *cmd.Migration)
	case CommandKindAdvanceMigration:
		if cmd.Migration == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyAdvanceMigration(ctx, *cmd.Migration)
	case CommandKindFinalizeMigration:
		if cmd.Migration == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyFinalizeMigration(ctx, *cmd.Migration)
	case CommandKindAbortMigration:
		if cmd.Migration == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyAbortMigration(ctx, *cmd.Migration)
	case CommandKindAddSlot:
		if cmd.AddSlot == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyAddSlot(ctx, *cmd.AddSlot)
	case CommandKindRemoveSlot:
		if cmd.RemoveSlot == nil {
			return controllermeta.ErrInvalidArgument
		}
		return sm.applyRemoveSlot(ctx, *cmd.RemoveSlot)
	default:
		return controllermeta.ErrInvalidArgument
	}
}

func (sm *StateMachine) applyNodeHeartbeat(ctx context.Context, report AgentReport) error {
	if report.NodeID == 0 || report.Addr == "" {
		return controllermeta.ErrInvalidArgument
	}

	node, err := sm.store.GetNode(ctx, report.NodeID)
	switch {
	case errors.Is(err, controllermeta.ErrNotFound):
		node = controllermeta.ClusterNode{NodeID: report.NodeID, CapacityWeight: 1}
	case err != nil:
		return err
	}

	node.Addr = report.Addr
	node.LastHeartbeatAt = report.ObservedAt
	if report.CapacityWeight > 0 {
		node.CapacityWeight = report.CapacityWeight
	}
	if node.CapacityWeight <= 0 {
		node.CapacityWeight = 1
	}
	if node.Status != controllermeta.NodeStatusDraining {
		node.Status = controllermeta.NodeStatusAlive
	}
	if err := sm.store.UpsertNode(ctx, node); err != nil {
		return err
	}

	if report.Runtime != nil {
		return sm.store.UpsertRuntimeView(ctx, *report.Runtime)
	}
	return nil
}

func (sm *StateMachine) applyOperatorRequest(ctx context.Context, op OperatorRequest) error {
	if op.NodeID == 0 {
		return controllermeta.ErrInvalidArgument
	}

	node, err := sm.store.GetNode(ctx, op.NodeID)
	switch {
	case errors.Is(err, controllermeta.ErrNotFound):
		node = controllermeta.ClusterNode{
			NodeID:         op.NodeID,
			Addr:           placeholderNodeAddr(op.NodeID),
			CapacityWeight: 1,
		}
	case err != nil:
		return err
	}

	switch op.Kind {
	case OperatorMarkNodeDraining:
		node.Status = controllermeta.NodeStatusDraining
	case OperatorResumeNode:
		node.Status = controllermeta.NodeStatusAlive
		return sm.store.UpsertNodeAndDeleteRepairTasks(ctx, node)
	default:
		return controllermeta.ErrInvalidArgument
	}
	return sm.store.UpsertNode(ctx, node)
}

func (sm *StateMachine) applyTimeoutEvaluation(ctx context.Context, now time.Time) error {
	nodes, err := sm.store.ListNodes(ctx)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if node.Status == controllermeta.NodeStatusDraining {
			continue
		}
		elapsed := now.Sub(node.LastHeartbeatAt)
		switch {
		case sm.cfg.DeadTimeout > 0 && elapsed > sm.cfg.DeadTimeout:
			node.Status = controllermeta.NodeStatusDead
		case sm.cfg.SuspectTimeout > 0 && elapsed >= sm.cfg.SuspectTimeout:
			node.Status = controllermeta.NodeStatusSuspect
		default:
			node.Status = controllermeta.NodeStatusAlive
		}
		if err := sm.store.UpsertNode(ctx, node); err != nil {
			return err
		}
	}
	return nil
}

func (sm *StateMachine) applyTaskResult(ctx context.Context, advance TaskAdvance) error {
	if advance.SlotID == 0 {
		return controllermeta.ErrInvalidArgument
	}

	task, err := sm.store.GetTask(ctx, advance.SlotID)
	if errors.Is(err, controllermeta.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if task.Attempt != advance.Attempt {
		return nil
	}
	if advance.Err == nil {
		return sm.store.DeleteTask(ctx, advance.SlotID)
	}

	task.Attempt++
	task.LastError = advance.Err.Error()
	if int(task.Attempt) >= sm.cfg.MaxTaskAttempts {
		task.Status = controllermeta.TaskStatusFailed
		task.NextRunAt = advance.Now
		return sm.store.UpsertTask(ctx, task)
	}

	task.Status = controllermeta.TaskStatusRetrying
	task.NextRunAt = advance.Now.Add(sm.retryDelay(task.Attempt))
	return sm.store.UpsertTask(ctx, task)
}

func (sm *StateMachine) applyAssignmentTaskUpdate(ctx context.Context, assignment *controllermeta.SlotAssignment, task *controllermeta.ReconcileTask) error {
	if task != nil && task.Kind == controllermeta.TaskKindRepair {
		obsolete, err := sm.repairTaskObsolete(ctx, *task)
		if err != nil {
			return err
		}
		if obsolete {
			return nil
		}
	}
	switch {
	case assignment != nil && task != nil:
		return sm.store.UpsertAssignmentTask(ctx, *assignment, *task)
	case assignment != nil:
		return sm.store.UpsertAssignment(ctx, *assignment)
	case task != nil:
		return sm.store.UpsertTask(ctx, *task)
	default:
		return controllermeta.ErrInvalidArgument
	}
}

func (sm *StateMachine) applyNodeStatusUpdate(ctx context.Context, update NodeStatusUpdate) error {
	if len(update.Transitions) == 0 {
		return controllermeta.ErrInvalidArgument
	}

	for _, transition := range update.Transitions {
		if transition.NodeID == 0 || !validNodeStatusTarget(transition.NewStatus) {
			return controllermeta.ErrInvalidArgument
		}

		node, err := sm.store.GetNode(ctx, transition.NodeID)
		if errors.Is(err, controllermeta.ErrNotFound) {
			if transition.Addr == "" {
				continue
			}
			node = controllermeta.ClusterNode{
				NodeID:         transition.NodeID,
				Addr:           transition.Addr,
				CapacityWeight: normalizeNodeCapacity(transition.CapacityWeight),
			}
			err = nil
		}
		if err != nil {
			return err
		}
		if transition.ExpectedStatus != nil && node.Status != *transition.ExpectedStatus {
			continue
		}

		if transition.Addr != "" {
			node.Addr = transition.Addr
		}
		if transition.CapacityWeight > 0 {
			node.CapacityWeight = transition.CapacityWeight
		}
		if node.CapacityWeight <= 0 {
			node.CapacityWeight = 1
		}
		node.Status = transition.NewStatus
		if transition.EvaluatedAt.After(node.LastHeartbeatAt) {
			node.LastHeartbeatAt = transition.EvaluatedAt
		}
		if err := sm.store.UpsertNode(ctx, node); err != nil {
			return err
		}
	}
	return nil
}

func validNodeStatusTarget(status controllermeta.NodeStatus) bool {
	switch status {
	case controllermeta.NodeStatusAlive, controllermeta.NodeStatusSuspect, controllermeta.NodeStatusDead, controllermeta.NodeStatusDraining:
		return true
	default:
		return false
	}
}

func normalizeNodeCapacity(weight int) int {
	if weight <= 0 {
		return 1
	}
	return weight
}

func (sm *StateMachine) repairTaskObsolete(ctx context.Context, task controllermeta.ReconcileTask) (bool, error) {
	if task.SourceNode == 0 {
		return false, nil
	}

	node, err := sm.store.GetNode(ctx, task.SourceNode)
	switch {
	case errors.Is(err, controllermeta.ErrNotFound):
		return false, nil
	case err != nil:
		return false, err
	}

	switch node.Status {
	case controllermeta.NodeStatusDead, controllermeta.NodeStatusDraining:
		return false, nil
	default:
		return true, nil
	}
}

func (sm *StateMachine) retryDelay(attempt uint32) time.Duration {
	if attempt == 0 {
		return sm.cfg.RetryBackoffBase
	}
	shift := attempt - 1
	if shift > 30 {
		shift = 30
	}
	factor := time.Duration(1 << shift)
	if sm.cfg.RetryBackoffBase > 0 && factor > 0 && sm.cfg.RetryBackoffBase > time.Duration(math.MaxInt64/int64(factor)) {
		return time.Duration(math.MaxInt64)
	}
	return sm.cfg.RetryBackoffBase * factor
}

func placeholderNodeAddr(nodeID uint64) string {
	return fmt.Sprintf("operator://node-%d", nodeID)
}

func (sm *StateMachine) applyStartMigration(ctx context.Context, req MigrationRequest) error {
	table, err := sm.store.LoadHashSlotTable(ctx)
	if err != nil {
		return err
	}
	table.StartMigration(req.HashSlot, multiraft.SlotID(req.Source), multiraft.SlotID(req.Target))
	return sm.store.SaveHashSlotTable(ctx, table)
}

func (sm *StateMachine) applyAdvanceMigration(ctx context.Context, req MigrationRequest) error {
	table, err := sm.store.LoadHashSlotTable(ctx)
	if err != nil {
		return err
	}
	table.AdvanceMigration(req.HashSlot, hashslot.MigrationPhase(req.Phase))
	return sm.store.SaveHashSlotTable(ctx, table)
}

func (sm *StateMachine) applyFinalizeMigration(ctx context.Context, req MigrationRequest) error {
	table, err := sm.store.LoadHashSlotTable(ctx)
	if err != nil {
		return err
	}
	table.FinalizeMigration(req.HashSlot)
	sourceSlot := multiraft.SlotID(req.Source)
	if drainedSlotAssignmentRemovable(table, sourceSlot) {
		return sm.store.DeleteAssignmentTaskAndSaveHashSlotTable(ctx, uint32(sourceSlot), table)
	}
	return sm.store.SaveHashSlotTable(ctx, table)
}

func (sm *StateMachine) applyAbortMigration(ctx context.Context, req MigrationRequest) error {
	table, err := sm.store.LoadHashSlotTable(ctx)
	if err != nil {
		return err
	}
	table.AbortMigration(req.HashSlot)
	return sm.store.SaveHashSlotTable(ctx, table)
}

func (sm *StateMachine) applyAddSlot(ctx context.Context, req AddSlotRequest) error {
	if req.NewSlotID == 0 {
		return controllermeta.ErrInvalidArgument
	}

	table, err := sm.store.LoadHashSlotTable(ctx)
	if err != nil {
		return err
	}

	plan := hashslot.ComputeAddSlotPlan(table, multiraft.SlotID(req.NewSlotID))
	for _, migration := range plan {
		table.StartMigration(migration.HashSlot, migration.From, migration.To)
	}

	assignment := controllermeta.SlotAssignment{
		SlotID:       uint32(req.NewSlotID),
		DesiredPeers: append([]uint64(nil), req.Peers...),
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:     uint32(req.NewSlotID),
		Kind:       controllermeta.TaskKindBootstrap,
		Step:       controllermeta.TaskStepAddLearner,
		TargetNode: firstBootstrapTarget(req.Peers),
		Status:     controllermeta.TaskStatusPending,
	}
	return sm.store.UpsertAssignmentTaskAndSaveHashSlotTable(ctx, assignment, task, table)
}

func (sm *StateMachine) applyRemoveSlot(ctx context.Context, req RemoveSlotRequest) error {
	if req.SlotID == 0 {
		return controllermeta.ErrInvalidArgument
	}

	table, err := sm.store.LoadHashSlotTable(ctx)
	if err != nil {
		return err
	}

	plan := hashslot.ComputeRemoveSlotPlan(table, multiraft.SlotID(req.SlotID))
	for _, migration := range plan {
		table.StartMigration(migration.HashSlot, migration.From, migration.To)
	}

	return sm.store.SaveHashSlotTable(ctx, table)
}

func firstBootstrapTarget(peers []uint64) uint64 {
	var target uint64
	for _, peer := range peers {
		if peer == 0 {
			continue
		}
		if target == 0 || peer < target {
			target = peer
		}
	}
	return target
}

func drainedSlotAssignmentRemovable(table *hashslot.HashSlotTable, slotID multiraft.SlotID) bool {
	if table == nil || slotID == 0 {
		return false
	}
	if len(table.HashSlotsOf(slotID)) != 0 {
		return false
	}
	for _, migration := range table.ActiveMigrations() {
		if migration.Source == slotID || migration.Target == slotID {
			return false
		}
	}
	return true
}
