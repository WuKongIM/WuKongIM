package fsm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
)

const (
	// ReasonNoChange marks an idempotent command that did not change logical state.
	ReasonNoChange = "no_change"
	// ReasonAlreadyApplied marks an entry at or before the durable applied index.
	ReasonAlreadyApplied = "already_applied"
	// ReasonStaleBootstrapObsolete marks a bootstrap command superseded by newer slot state.
	ReasonStaleBootstrapObsolete = "stale_bootstrap_obsolete"
	// ReasonStaleBootstrapMissingSlot marks a stale bootstrap command for a still-missing slot.
	ReasonStaleBootstrapMissingSlot = "stale_bootstrap_missing_slot"
	// ReasonExpectedRevisionMismatch marks a failed compare-and-set guard.
	ReasonExpectedRevisionMismatch = "expected_revision_mismatch"
	// ReasonInvalidState marks a command that would violate cluster-state invariants.
	ReasonInvalidState = "invalid_state"
	// ReasonInvalidCommand marks a command missing required payload fields.
	ReasonInvalidCommand = "invalid_command"
	// ReasonInvalidTaskResult marks a task result missing required identifiers.
	ReasonInvalidTaskResult = "invalid_task_result"
	// ReasonTaskMissing marks an obsolete task result for an absent active task.
	ReasonTaskMissing = "task_missing"
	// ReasonTaskSlotMismatch marks a task result that targets the wrong slot.
	ReasonTaskSlotMismatch = "task_slot_mismatch"
	// ReasonInitConflict marks an init command that does not match existing state.
	ReasonInitConflict = "init_conflict"
)

// ApplyResult describes the deterministic outcome of applying one committed command.
type ApplyResult struct {
	// Changed is true when the command advanced the logical state revision.
	Changed bool
	// Noop is true when the command was idempotent or obsolete.
	Noop bool
	// Rejected is true when the command is semantically invalid for the current state.
	Rejected bool
	// Reason describes a no-op or semantic reject outcome.
	Reason string
	// Revision is the resulting logical cluster-state revision.
	Revision uint64
	// AppliedRaftIndex is the resulting durable Raft applied index.
	AppliedRaftIndex uint64
}

// StateMachine applies committed ControllerV2 commands to a durable state file.
type StateMachine struct {
	mu       sync.Mutex
	store    *statefile.Store
	state    state.ClusterState
	degraded bool
}

// New creates a state machine backed by store.
func New(store *statefile.Store) (*StateMachine, error) {
	if store == nil {
		return nil, fmt.Errorf("controllerv2/fsm: store is required")
	}
	return &StateMachine{store: store}, nil
}

// Load loads the current durable cluster state into memory.
func (sm *StateMachine) Load(ctx context.Context) error {
	st, err := sm.store.Load(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			sm.mu.Lock()
			sm.state = state.ClusterState{}
			sm.degraded = false
			sm.mu.Unlock()
			return nil
		}
		sm.mu.Lock()
		sm.degraded = true
		sm.mu.Unlock()
		return err
	}
	sm.mu.Lock()
	sm.state = st.Clone()
	sm.degraded = false
	sm.mu.Unlock()
	return nil
}

// Reset clears the in-memory state so a caller can replay durable Raft history from an empty state.
func (sm *StateMachine) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.state = state.ClusterState{}
	sm.degraded = false
}

// Snapshot returns a deep copy of the latest published state.
func (sm *StateMachine) Snapshot(ctx context.Context) state.ClusterState {
	_ = ctx
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state.Clone()
}

// IsDegraded reports whether the last local durable operation failed.
func (sm *StateMachine) IsDegraded() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.degraded
}

// Apply applies one committed Raft command and saves the resulting state before publishing it.
func (sm *StateMachine) Apply(ctx context.Context, raftIndex uint64, cmd command.Command) (ApplyResult, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	current := sm.state.Clone()
	if current.Revision != 0 && raftIndex <= current.AppliedRaftIndex {
		return ApplyResult{Noop: true, Reason: ReasonAlreadyApplied, Revision: current.Revision, AppliedRaftIndex: current.AppliedRaftIndex}, nil
	}

	next := current.Clone()
	result := sm.applyMutation(&next, raftIndex, cmd)
	if next.Revision != 0 && raftIndex > current.AppliedRaftIndex {
		next.AppliedRaftIndex = raftIndex
	}
	result.Revision = next.Revision
	result.AppliedRaftIndex = next.AppliedRaftIndex

	if next.Revision == 0 {
		// Before init there is no valid state file to persist; still report the
		// committed index for deterministic semantic rejects so callers can mark
		// the Raft entry handled without publishing an invalid cluster state.
		if result.Rejected {
			result.AppliedRaftIndex = raftIndex
		}
		return result, nil
	}
	checksum, err := state.Checksum(next)
	if err != nil {
		sm.degraded = true
		return result, err
	}
	next.Checksum = checksum
	if err := sm.store.Save(ctx, next); err != nil {
		sm.degraded = true
		return result, err
	}
	sm.state = next.Clone()
	sm.degraded = false
	return result, nil
}
