package fsm

import (
	"reflect"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// handleBootstrapRevisionMismatch makes stale bootstrap proposals idempotent when newer state already covers the slot.
func handleBootstrapRevisionMismatch(current *state.ClusterState, cmd command.Command) (ApplyResult, bool) {
	if cmd.ExpectedRevision == nil || *cmd.ExpectedRevision == current.Revision {
		return ApplyResult{}, false
	}
	// Bootstrap planning can race with an already-committed slot assignment. A
	// mismatched ExpectedRevision is idempotent only when current state proves
	// this stale bootstrap command has already been made obsolete.
	if cmd.Assignment == nil || cmd.Task == nil || cmd.Task.Kind != state.TaskKindBootstrap {
		return reject(ReasonExpectedRevisionMismatch), true
	}
	slotID := cmd.Assignment.SlotID
	if existing, ok := findAssignment(current.Slots, slotID); ok {
		if equivalentAssignment(existing, *cmd.Assignment) {
			return noop(ReasonStaleBootstrapObsolete), true
		}
		return noop(ReasonStaleBootstrapObsolete), true
	}
	if existing, ok := findTaskBySlot(current.Tasks, slotID); ok {
		if equivalentTask(existing, *cmd.Task) {
			return noop(ReasonStaleBootstrapObsolete), true
		}
		return noop(ReasonStaleBootstrapObsolete), true
	}
	return reject(ReasonStaleBootstrapMissingSlot), true
}

func handleFailTaskRevisionMismatch(current *state.ClusterState, cmd command.Command) (ApplyResult, bool) {
	if cmd.ExpectedRevision == nil || *cmd.ExpectedRevision == current.Revision {
		return ApplyResult{}, false
	}
	if cmd.TaskResult == nil || cmd.TaskResult.TaskID == "" {
		return reject(ReasonInvalidTaskResult), true
	}
	if findTaskByID(current.Tasks, cmd.TaskResult.TaskID) < 0 {
		return noop(ReasonTaskMissing), true
	}
	return reject(ReasonExpectedRevisionMismatch), true
}

func isNonBootstrapIdempotent(current state.ClusterState, cmd command.Command) bool {
	switch cmd.Kind {
	case command.KindUpsertNode:
		if cmd.Node == nil {
			return false
		}
		idx := findNode(current.Nodes, cmd.Node.NodeID)
		return idx >= 0 && equivalentNode(current.Nodes[idx], *cmd.Node)
	case command.KindUpdateControllerVoters:
		candidate := current.Clone()
		candidate.Controllers = append([]state.ControllerVoter(nil), cmd.Controllers...)
		candidate.Normalize()
		return reflect.DeepEqual(current.Controllers, candidate.Controllers)
	case command.KindReplaceHashSlotTable:
		return cmd.HashSlots != nil && reflect.DeepEqual(current.HashSlots, cloneHashSlotTable(*cmd.HashSlots))
	case command.KindCompleteTask:
		return cmd.TaskResult != nil && cmd.TaskResult.TaskID != "" && findTaskByID(current.Tasks, cmd.TaskResult.TaskID) < 0
	default:
		return false
	}
}

func validateChanged(next *state.ClusterState, before state.ClusterState, cmd command.Command) ApplyResult {
	next.Revision++
	next.UpdatedAt = nextUpdatedAt(before.UpdatedAt, cmd.IssuedAt)
	if err := next.Validate(); err != nil {
		*next = before
		return reject(ReasonInvalidState)
	}
	return changed()
}

// nextUpdatedAt is deterministic for replay: non-zero command timestamps are
// normalized to UTC, while zero timestamps preserve the previous durable value.
func nextUpdatedAt(previous time.Time, issuedAt time.Time) time.Time {
	if issuedAt.IsZero() {
		return previous.UTC()
	}
	return issuedAt.UTC()
}

// commandIssuedAt uses zero time for zero-timestamp init commands because no
// previous durable state exists yet.
func commandIssuedAt(issuedAt time.Time) time.Time {
	if issuedAt.IsZero() {
		return time.Time{}
	}
	return issuedAt.UTC()
}

func changed() ApplyResult {
	return ApplyResult{Changed: true}
}

func noop(reason string) ApplyResult {
	return ApplyResult{Noop: true, Reason: reason}
}

func reject(reason string) ApplyResult {
	return ApplyResult{Rejected: true, Reason: reason}
}
