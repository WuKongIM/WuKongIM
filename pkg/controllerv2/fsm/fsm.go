package fsm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
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

// AppliedCommand binds a decoded ControllerV2 command to its committed Raft position.
type AppliedCommand struct {
	// Index is the committed Raft log index for Command.
	Index uint64
	// Term is the committed Raft term for Command.
	Term uint64
	// Command is the deterministic ControllerV2 mutation to apply.
	Command command.Command
}

// BatchApplyResult describes the deterministic outcome of applying a command batch.
type BatchApplyResult struct {
	// Results contains one result per input command in the same order.
	Results []ApplyResult
	// FinalState is the state published after durable save, or the current state when no state exists yet.
	FinalState state.ClusterState
}

// Store persists and loads ControllerV2 cluster state snapshots.
type Store interface {
	Load(context.Context) (state.ClusterState, error)
	Save(context.Context, state.ClusterState) error
}

// StateMachine applies committed ControllerV2 commands to a durable state file.
type StateMachine struct {
	mu       sync.Mutex
	store    Store
	state    state.ClusterState
	degraded bool
}

// New creates a state machine backed by store.
func New(store Store) (*StateMachine, error) {
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

// Reset clears warm in-memory state before Controller Raft startup recovery replays history.
//
// The caller must ensure no Service run loop, Apply, or Load call is active concurrently.
func (sm *StateMachine) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.state = state.ClusterState{}
	sm.degraded = false
}

// Restore saves and publishes a recovered ControllerV2 state snapshot.
func (sm *StateMachine) Restore(ctx context.Context, st state.ClusterState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	next := st.Clone()
	if next.Revision != 0 {
		checksum, err := state.Checksum(next)
		if err != nil {
			sm.degraded = true
			return err
		}
		next.Checksum = checksum
		if err := sm.store.Save(ctx, next); err != nil {
			sm.degraded = true
			return err
		}
	}
	sm.state = next.Clone()
	sm.degraded = false
	return nil
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
	result, err := sm.ApplyBatch(ctx, []AppliedCommand{{Index: raftIndex, Command: cmd}})
	if err != nil {
		if len(result.Results) > 0 {
			return result.Results[0], err
		}
		return ApplyResult{}, err
	}
	if len(result.Results) == 0 {
		return ApplyResult{}, nil
	}
	return result.Results[0], nil
}

// ApplyBatch applies committed Raft commands and saves the final state once before publishing it.
func (sm *StateMachine) ApplyBatch(ctx context.Context, entries []AppliedCommand) (BatchApplyResult, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	current := sm.state.Clone()
	next := current.Clone()
	out := BatchApplyResult{Results: make([]ApplyResult, 0, len(entries))}

	for _, entry := range entries {
		if current.Revision != 0 && entry.Index <= next.AppliedRaftIndex {
			out.Results = append(out.Results, ApplyResult{
				Noop:             true,
				Reason:           ReasonAlreadyApplied,
				Revision:         next.Revision,
				AppliedRaftIndex: next.AppliedRaftIndex,
			})
			continue
		}

		result := sm.applyMutation(&next, entry.Index, entry.Command)
		if next.Revision != 0 && entry.Index > next.AppliedRaftIndex {
			next.AppliedRaftIndex = entry.Index
		}
		result.Revision = next.Revision
		result.AppliedRaftIndex = next.AppliedRaftIndex
		if next.Revision == 0 && result.Rejected {
			// Before init there is no valid state file to persist; still report the
			// committed index for deterministic semantic rejects so callers can mark
			// the Raft entry handled without publishing an invalid cluster state.
			result.AppliedRaftIndex = entry.Index
		}
		out.Results = append(out.Results, result)
	}

	if next.Revision == 0 {
		out.FinalState = next.Clone()
		return out, nil
	}
	checksum, err := state.Checksum(next)
	if err != nil {
		sm.degraded = true
		return out, err
	}
	next.Checksum = checksum
	if err := sm.store.Save(ctx, next); err != nil {
		sm.degraded = true
		return out, err
	}
	sm.state = next.Clone()
	sm.degraded = false
	out.FinalState = sm.state.Clone()
	return out, nil
}
