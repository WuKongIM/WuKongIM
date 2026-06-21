package tasks

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultLeaderTransferPollMax      = 30
	defaultLeaderTransferPollInterval = 10 * time.Millisecond
	leaderTransferTimeoutError        = "leader transfer timed out"
)

// LeaderTransferRuntime is the Slot Raft surface needed to move leadership.
type LeaderTransferRuntime interface {
	Status(multiraft.SlotID) (multiraft.Status, error)
	ExpectLeaderTransfer(context.Context, multiraft.SlotID, multiraft.NodeID) error
	TransferLeadership(context.Context, multiraft.SlotID, multiraft.NodeID) error
}

// LeaderTransferExecutorConfig wires a Slot leader-transfer task executor.
type LeaderTransferExecutorConfig struct {
	// LocalNode is this node's stable cluster identity.
	LocalNode uint64
	// Runtime observes and controls local Slot Raft leadership.
	Runtime LeaderTransferRuntime
	// Writer persists task completion through the Controller facade.
	Writer Writer
	// PollMax bounds status polls after requesting leadership transfer.
	PollMax int
	// PollInterval waits between status polls after requesting transfer.
	PollInterval time.Duration
}

// LeaderTransferExecutor executes Slot leader-transfer tasks from the Slot leader.
type LeaderTransferExecutor struct {
	cfg LeaderTransferExecutorConfig
}

// NewLeaderTransferExecutor creates a LeaderTransferExecutor.
func NewLeaderTransferExecutor(cfg LeaderTransferExecutorConfig) *LeaderTransferExecutor {
	if cfg.PollMax == 0 {
		cfg.PollMax = defaultLeaderTransferPollMax
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultLeaderTransferPollInterval
	}
	return &LeaderTransferExecutor{cfg: cfg}
}

// Reconcile requests local Slot Raft leadership transfer and completes once an eligible leader is observed.
func (e *LeaderTransferExecutor) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if e == nil || e.cfg.LocalNode == 0 || e.cfg.Runtime == nil || e.cfg.Writer == nil {
		return nil
	}
	for _, task := range snapshot.Tasks {
		if task.Kind != control.TaskKindLeaderTransfer || task.Step != control.TaskStepTransferLeader || task.CompletionPolicy != control.TaskCompletionPolicySingleObserver {
			continue
		}
		assignment, ok := findSlot(snapshot.Slots, task.SlotID)
		if !ok {
			continue
		}
		if err := e.reconcileTask(ctx, task, assignment); err != nil {
			return err
		}
	}
	return nil
}

func (e *LeaderTransferExecutor) reconcileTask(ctx context.Context, task control.ReconcileTask, assignment control.SlotAssignment) error {
	slotID := multiraft.SlotID(task.SlotID)
	status, err := e.cfg.Runtime.Status(slotID)
	if err != nil {
		return err
	}
	if legalCompletedLeader(task, assignment, status) {
		return e.completeTask(ctx, task)
	}
	if err := e.cfg.Runtime.ExpectLeaderTransfer(ctx, slotID, multiraft.NodeID(task.TargetNode)); err != nil {
		return err
	}
	if uint64(status.LeaderID) != e.cfg.LocalNode {
		return nil
	}
	if err := e.cfg.Runtime.TransferLeadership(ctx, slotID, multiraft.NodeID(task.TargetNode)); err != nil {
		return err
	}
	for i := 0; i < e.cfg.PollMax; i++ {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		if e.cfg.PollInterval > 0 {
			timer := time.NewTimer(e.cfg.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		status, err = e.cfg.Runtime.Status(slotID)
		if err != nil {
			return err
		}
		if legalCompletedLeader(task, assignment, status) {
			return e.completeTask(ctx, task)
		}
	}
	return e.failTask(ctx, task, leaderTransferTimeoutError)
}

func (e *LeaderTransferExecutor) completeTask(ctx context.Context, task control.ReconcileTask) error {
	return e.cfg.Writer.CompleteTask(ctx, cv2.TaskResult{
		TaskID:      task.TaskID,
		SlotID:      task.SlotID,
		TaskKind:    cv2.TaskKind(task.Kind),
		ConfigEpoch: task.ConfigEpoch,
		Attempt:     task.Attempt,
	})
}

func (e *LeaderTransferExecutor) failTask(ctx context.Context, task control.ReconcileTask, err string) error {
	return e.cfg.Writer.FailTask(ctx, cv2.TaskResult{
		TaskID:      task.TaskID,
		SlotID:      task.SlotID,
		TaskKind:    cv2.TaskKind(task.Kind),
		ConfigEpoch: task.ConfigEpoch,
		Attempt:     task.Attempt,
		Err:         err,
	})
}

func legalCompletedLeader(task control.ReconcileTask, assignment control.SlotAssignment, status multiraft.Status) bool {
	leader := uint64(status.LeaderID)
	if leader == 0 || leader == task.SourceNode {
		return false
	}
	if !containsNode(assignment.DesiredPeers, leader) || !containsVoter(status.CurrentVoters, leader) {
		return false
	}
	return len(status.CurrentVoters) >= len(assignment.DesiredPeers)/2+1
}

func containsVoter(voters []multiraft.NodeID, nodeID uint64) bool {
	for _, voter := range voters {
		if uint64(voter) == nodeID {
			return true
		}
	}
	return false
}
