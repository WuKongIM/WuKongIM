package tasks

import (
	"context"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/slots"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultSlotReplicaMovePollMax      = 30
	defaultSlotReplicaMovePollInterval = 10 * time.Millisecond
	slotReplicaMoveCatchUpTimeoutError = "slot replica move learner catch-up timed out"
	maxSlotReplicaMoveLastErrorBytes   = 1024
)

// SlotReplicaMoveRuntime is the Slot Raft surface needed to move one replica.
type SlotReplicaMoveRuntime interface {
	Status(multiraft.SlotID) (multiraft.Status, error)
	ChangeConfig(context.Context, multiraft.SlotID, multiraft.ConfigChange) (multiraft.Future, error)
	TransferLeadership(context.Context, multiraft.SlotID, multiraft.NodeID) error
}

// SlotLearnerOpener opens local Slot runtime state for a staged learner.
type SlotLearnerOpener interface {
	OpenLearner(context.Context, slots.Assignment) error
}

// SlotReplicaMoveWriter persists staged replica-move task commands.
type SlotReplicaMoveWriter interface {
	AdvanceSlotReplicaMovePhase(context.Context, control.SlotReplicaMovePhaseAdvance) error
	CommitSlotReplicaMove(context.Context, control.SlotReplicaMoveCommit) error
	FailTask(context.Context, cv2.TaskResult) error
}

// SlotReplicaMoveExecutorConfig wires a staged Slot replica move executor.
type SlotReplicaMoveExecutorConfig struct {
	// LocalNode is this node's stable cluster identity.
	LocalNode uint64
	// Runtime observes and changes Slot Raft config from the Slot leader.
	Runtime SlotReplicaMoveRuntime
	// Learners opens target learner runtime state on the target node.
	Learners SlotLearnerOpener
	// MoveWriter persists task phase and commit commands.
	MoveWriter SlotReplicaMoveWriter
	// PollMax bounds status polls while waiting for learner catch-up.
	PollMax int
	// PollInterval waits between learner catch-up polls.
	PollInterval time.Duration
}

// SlotReplicaMoveExecutor executes staged Slot replica move tasks.
type SlotReplicaMoveExecutor struct {
	cfg SlotReplicaMoveExecutorConfig
}

// NewSlotReplicaMoveExecutor creates a SlotReplicaMoveExecutor.
func NewSlotReplicaMoveExecutor(cfg SlotReplicaMoveExecutorConfig) *SlotReplicaMoveExecutor {
	if cfg.PollMax == 0 {
		cfg.PollMax = defaultSlotReplicaMovePollMax
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultSlotReplicaMovePollInterval
	}
	return &SlotReplicaMoveExecutor{cfg: cfg}
}

// Reconcile advances staged Slot replica move tasks using observed Slot Raft state.
func (e *SlotReplicaMoveExecutor) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if e == nil || e.cfg.LocalNode == 0 || e.cfg.MoveWriter == nil {
		return nil
	}
	for _, task := range snapshot.Tasks {
		if task.Kind != control.TaskKindSlotReplicaMove || task.CompletionPolicy != control.TaskCompletionPolicySingleObserver {
			continue
		}
		_, ok := findSlot(snapshot.Slots, task.SlotID)
		if !ok {
			continue
		}
		if err := e.reconcileTask(ctx, snapshot.HashSlots, task); err != nil {
			return err
		}
	}
	return nil
}

func (e *SlotReplicaMoveExecutor) reconcileTask(ctx context.Context, table control.HashSlotTable, task control.ReconcileTask) error {
	if task.Step == control.TaskStepOpenLearner {
		if e.cfg.LocalNode != task.TargetNode || e.cfg.Learners == nil {
			return nil
		}
		if err := e.openLearner(ctx, table, task); err != nil {
			return e.failTask(ctx, task, err.Error())
		}
		return e.advancePhase(ctx, task, control.TaskStepAddLearner, multiraft.Status{SlotID: multiraft.SlotID(task.SlotID)})
	}
	if e.cfg.LocalNode == task.TargetNode && e.cfg.Learners != nil {
		if err := e.openLearner(ctx, table, task); err != nil {
			return e.failTask(ctx, task, err.Error())
		}
	}
	if e.cfg.Runtime == nil {
		return nil
	}
	status, err := e.cfg.Runtime.Status(multiraft.SlotID(task.SlotID))
	if err != nil {
		return err
	}
	if uint64(status.LeaderID) != e.cfg.LocalNode {
		return nil
	}
	switch task.Step {
	case control.TaskStepAddLearner:
		return e.addLearner(ctx, task, status)
	case control.TaskStepPromoteLearner:
		return e.promoteLearner(ctx, task, status)
	case control.TaskStepRemoveVoter:
		return e.removeVoter(ctx, task, status)
	case control.TaskStepCommitAssignment:
		return e.commitAssignment(ctx, task, status)
	default:
		return nil
	}
}

func (e *SlotReplicaMoveExecutor) openLearner(ctx context.Context, table control.HashSlotTable, task control.ReconcileTask) error {
	return e.cfg.Learners.OpenLearner(ctx, slots.Assignment{
		SlotID:       task.SlotID,
		DesiredPeers: append([]uint64(nil), task.TargetPeers...),
		HashSlots:    hashSlotsForSlot(table, task.SlotID),
	})
}

func (e *SlotReplicaMoveExecutor) addLearner(ctx context.Context, task control.ReconcileTask, status multiraft.Status) error {
	if containsNodeID(status.CurrentVoters, task.TargetNode) {
		return e.advancePhase(ctx, task, control.TaskStepRemoveVoter, status)
	}
	if containsNodeID(status.CurrentLearners, task.TargetNode) {
		return e.advancePhase(ctx, task, control.TaskStepPromoteLearner, status)
	}
	if err := e.changeConfig(ctx, task, multiraft.ConfigChange{
		Type:   multiraft.AddLearner,
		NodeID: multiraft.NodeID(task.TargetNode),
	}); err != nil {
		return e.failTask(ctx, task, err.Error())
	}
	observed, ok, err := e.waitForStatus(ctx, task, status, func(st multiraft.Status) bool {
		return containsNodeID(st.CurrentLearners, task.TargetNode) || containsNodeID(st.CurrentVoters, task.TargetNode)
	})
	if err != nil {
		return err
	}
	if !ok {
		return e.failTask(ctx, task, "slot replica move add learner observation timed out")
	}
	if containsNodeID(observed.CurrentVoters, task.TargetNode) {
		return e.advancePhase(ctx, task, control.TaskStepRemoveVoter, observed)
	}
	return e.advancePhase(ctx, task, control.TaskStepPromoteLearner, observed)
}

func (e *SlotReplicaMoveExecutor) promoteLearner(ctx context.Context, task control.ReconcileTask, status multiraft.Status) error {
	if containsNodeID(status.CurrentVoters, task.TargetNode) {
		return e.advancePhase(ctx, task, control.TaskStepRemoveVoter, status)
	}
	caughtUp, latest, err := e.waitLearnerCaughtUp(ctx, task, status)
	if err != nil {
		return err
	}
	if !caughtUp {
		return e.failTask(ctx, task, slotReplicaMoveCatchUpTimeoutError)
	}
	if err := e.changeConfig(ctx, task, multiraft.ConfigChange{
		Type:   multiraft.PromoteLearner,
		NodeID: multiraft.NodeID(task.TargetNode),
	}); err != nil {
		return e.failTask(ctx, task, err.Error())
	}
	observed, ok, err := e.waitForStatus(ctx, task, latest, func(st multiraft.Status) bool {
		return containsNodeID(st.CurrentVoters, task.TargetNode)
	})
	if err != nil {
		return err
	}
	if !ok {
		return e.failTask(ctx, task, "slot replica move promote learner observation timed out")
	}
	return e.advancePhase(ctx, task, control.TaskStepRemoveVoter, observed)
}

func (e *SlotReplicaMoveExecutor) removeVoter(ctx context.Context, task control.ReconcileTask, status multiraft.Status) error {
	if !containsNodeID(status.CurrentVoters, task.SourceNode) {
		observed, ok, err := e.waitForStatus(ctx, task, status, func(st multiraft.Status) bool {
			return validMoveCommitObservation(task, st)
		})
		if err != nil {
			return err
		}
		if !ok {
			return e.failTask(ctx, task, "slot replica move remove voter did not observe target voters")
		}
		return e.advancePhase(ctx, task, control.TaskStepCommitAssignment, observed)
	}
	if uint64(status.LeaderID) == task.SourceNode {
		target, ok := firstNonSourceVoter(status.CurrentVoters, task.SourceNode)
		if !ok {
			return e.failTask(ctx, task, "slot replica move has no non-source voter for leadership transfer")
		}
		if err := e.cfg.Runtime.TransferLeadership(ctx, multiraft.SlotID(task.SlotID), multiraft.NodeID(target)); err != nil {
			return e.failTask(ctx, task, err.Error())
		}
		return e.advancePhase(ctx, task, control.TaskStepRemoveVoter, status)
	}
	if err := e.changeConfig(ctx, task, multiraft.ConfigChange{
		Type:   multiraft.RemoveVoter,
		NodeID: multiraft.NodeID(task.SourceNode),
	}); err != nil {
		return e.failTask(ctx, task, err.Error())
	}
	observed, ok, err := e.waitForStatus(ctx, task, status, func(st multiraft.Status) bool {
		return validMoveCommitObservation(task, st)
	})
	if err != nil {
		return err
	}
	if !ok {
		return e.failTask(ctx, task, "slot replica move remove voter did not observe target voters")
	}
	return e.advancePhase(ctx, task, control.TaskStepCommitAssignment, observed)
}

func (e *SlotReplicaMoveExecutor) commitAssignment(ctx context.Context, task control.ReconcileTask, status multiraft.Status) error {
	if status.ConfigAppliedIndex == 0 || !sameUint64Set(nodeIDsToUint64(status.CurrentVoters), task.TargetPeers) {
		return nil
	}
	return e.cfg.MoveWriter.CommitSlotReplicaMove(ctx, control.SlotReplicaMoveCommit{
		TaskID:      task.TaskID,
		SlotID:      task.SlotID,
		ConfigEpoch: task.ConfigEpoch,
		Attempt:     task.Attempt,
	})
}

func (e *SlotReplicaMoveExecutor) waitLearnerCaughtUp(ctx context.Context, task control.ReconcileTask, current multiraft.Status) (bool, multiraft.Status, error) {
	status, ok, err := e.waitForStatus(ctx, task, current, func(status multiraft.Status) bool {
		return learnerCaughtUp(status, task.TargetNode)
	})
	return ok, status, err
}

func (e *SlotReplicaMoveExecutor) changeConfig(ctx context.Context, task control.ReconcileTask, change multiraft.ConfigChange) error {
	fut, err := e.cfg.Runtime.ChangeConfig(ctx, multiraft.SlotID(task.SlotID), change)
	if err != nil {
		return err
	}
	if fut == nil {
		return nil
	}
	_, err = fut.Wait(ctx)
	return err
}

func (e *SlotReplicaMoveExecutor) waitForStatus(ctx context.Context, task control.ReconcileTask, current multiraft.Status, predicate func(multiraft.Status) bool) (multiraft.Status, bool, error) {
	status := current
	for i := 0; i <= e.cfg.PollMax; i++ {
		if predicate(status) {
			return status, true, nil
		}
		if i == e.cfg.PollMax {
			break
		}
		if err := ctxErr(ctx); err != nil {
			return status, false, err
		}
		if e.cfg.PollInterval > 0 {
			timer := time.NewTimer(e.cfg.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return status, false, ctx.Err()
			case <-timer.C:
			}
		}
		latest, err := e.cfg.Runtime.Status(multiraft.SlotID(task.SlotID))
		if err != nil {
			return status, false, err
		}
		status = latest
	}
	return status, false, nil
}

func (e *SlotReplicaMoveExecutor) advancePhase(ctx context.Context, task control.ReconcileTask, next control.TaskStep, status multiraft.Status) error {
	return e.cfg.MoveWriter.AdvanceSlotReplicaMovePhase(ctx, control.SlotReplicaMovePhaseAdvance{
		TaskID:              task.TaskID,
		SlotID:              task.SlotID,
		ConfigEpoch:         task.ConfigEpoch,
		Attempt:             task.Attempt,
		ExpectedPhaseIndex:  task.PhaseIndex,
		NextStep:            next,
		ObservedConfigIndex: status.ConfigAppliedIndex,
		ObservedVoters:      nodeIDsToUint64(status.CurrentVoters),
		ObservedLearners:    nodeIDsToUint64(status.CurrentLearners),
	})
}

func (e *SlotReplicaMoveExecutor) failTask(ctx context.Context, task control.ReconcileTask, err string) error {
	return e.cfg.MoveWriter.FailTask(ctx, cv2.TaskResult{
		TaskID:      task.TaskID,
		SlotID:      task.SlotID,
		TaskKind:    cv2.TaskKind(task.Kind),
		ConfigEpoch: task.ConfigEpoch,
		Attempt:     task.Attempt,
		Err:         boundedTaskError(err),
	})
}

func learnerCaughtUp(status multiraft.Status, target uint64) bool {
	if !containsNodeID(status.CurrentLearners, target) {
		return false
	}
	progress, ok := status.Progress[multiraft.NodeID(target)]
	return ok && progress.Match >= status.CommitIndex
}

func validMoveCommitObservation(task control.ReconcileTask, status multiraft.Status) bool {
	return status.ConfigAppliedIndex != 0 &&
		!containsNodeID(status.CurrentVoters, task.SourceNode) &&
		sameUint64Set(nodeIDsToUint64(status.CurrentVoters), task.TargetPeers)
}

func containsNodeID(nodes []multiraft.NodeID, want uint64) bool {
	for _, node := range nodes {
		if uint64(node) == want {
			return true
		}
	}
	return false
}

func nodeIDsToUint64(nodes []multiraft.NodeID) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, uint64(node))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func firstNonSourceVoter(voters []multiraft.NodeID, source uint64) (uint64, bool) {
	candidates := nodeIDsToUint64(voters)
	for _, candidate := range candidates {
		if candidate != source {
			return candidate, true
		}
	}
	return 0, false
}

func sameUint64Set(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]uint64(nil), a...)
	right := append([]uint64(nil), b...)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func boundedTaskError(err string) string {
	return truncateTaskErrorUTF8(err, maxSlotReplicaMoveLastErrorBytes)
}

func truncateTaskErrorUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(s)) <= maxBytes {
		return s
	}
	trimmed := string([]byte(s)[:maxBytes])
	for !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}
