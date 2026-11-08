package fsm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

// ApplyResult describes the deterministic outcome of applying one committed command.
type ApplyResult struct {
	// Changed is true when the command advanced the logical state revision.
	Changed bool
	// Updated is true when the command changed durable state without advancing the logical cluster-state revision.
	Updated bool
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
	// TaskTransitions lists active task state edges produced by this command.
	TaskTransitions []TaskTransition
	// PresenceOwnerSet is the exact owner-set result of this applied command.
	PresenceOwnerSet *state.PresenceOwnerSet
	// PresenceGeneration is the exact claim result, never a later state reread.
	PresenceGeneration *state.PresenceGeneration
}

// AppliedCommand binds a decoded Controller command to its committed Raft position.
type AppliedCommand struct {
	// Index is the committed Raft log index for Command.
	Index uint64
	// Term is the committed Raft term for Command.
	Term uint64
	// Command is the deterministic Controller mutation to apply.
	Command command.Command
}

// BatchApplyResult describes the deterministic outcome of applying a command batch.
type BatchApplyResult struct {
	// Results contains one result per input command in the same order.
	Results []ApplyResult
	// FinalState is the state published after durable save, or the current state when no state exists yet.
	FinalState state.ClusterState
}

// Store persists and loads Controller cluster state snapshots.
type Store interface {
	Load(context.Context) (state.ClusterState, error)
	Save(context.Context, state.ClusterState) error
}

// StateMachine applies committed Controller commands to a durable state file.
type StateMachine struct {
	mu       sync.Mutex
	store    Store
	state    state.ClusterState
	degraded bool

	// checkpointEntries and checkpointInterval enable checkpoint mode when
	// non-zero: the state file is then a bounded-lag checkpoint of state the
	// Controller Raft WAL can replay, not a per-batch durability step (#1098).
	checkpointEntries  uint64
	checkpointInterval time.Duration
	now                func() time.Time
	// savedIndex and savedAt describe the last state file actually written or
	// loaded; they are the checkpoint baseline.
	savedIndex uint64
	savedAt    time.Time
}

// Option configures a StateMachine.
type Option func(*StateMachine)

// WithCheckpointPolicy makes the state file a checkpoint: it is written for the
// first materialized state, then once every entries applied Raft indexes or
// interval elapsed, whichever comes first, and on explicit Checkpoint calls.
// Callers must checkpoint before releasing any Raft WAL prefix.
func WithCheckpointPolicy(entries uint64, interval time.Duration) Option {
	return func(sm *StateMachine) {
		sm.checkpointEntries, sm.checkpointInterval = entries, interval
	}
}

// WithCheckpointClock overrides the clock used by the checkpoint interval.
func WithCheckpointClock(now func() time.Time) Option {
	return func(sm *StateMachine) {
		if now != nil {
			sm.now = now
		}
	}
}

// New creates a state machine backed by store. Without WithCheckpointPolicy
// every applied batch is saved before it is published.
func New(store Store, opts ...Option) (*StateMachine, error) {
	if store == nil {
		return nil, fmt.Errorf("controller/fsm: store is required")
	}
	sm := &StateMachine{store: store, now: time.Now}
	for _, opt := range opts {
		opt(sm)
	}
	return sm, nil
}

// checkpointDueLocked reports whether next must be written to the state file.
func (sm *StateMachine) checkpointDueLocked(next state.ClusterState) bool {
	if sm.checkpointEntries == 0 && sm.checkpointInterval == 0 {
		return true
	}
	if sm.savedIndex == 0 {
		return true
	}
	if sm.checkpointEntries != 0 && next.AppliedRaftIndex >= sm.savedIndex+sm.checkpointEntries {
		return true
	}
	return sm.checkpointInterval != 0 && sm.now().Sub(sm.savedAt) >= sm.checkpointInterval
}

// saveLocked writes st and records it as the checkpoint baseline.
func (sm *StateMachine) saveLocked(ctx context.Context, st state.ClusterState) error {
	if err := sm.store.Save(ctx, st); err != nil {
		return err
	}
	sm.savedIndex, sm.savedAt = st.AppliedRaftIndex, sm.now()
	return nil
}

// Checkpoint writes the current applied state when it is newer than the last
// state file. Controller Raft calls it before WAL compaction and on shutdown.
func (sm *StateMachine) Checkpoint(ctx context.Context) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.state.Revision == 0 || sm.state.AppliedRaftIndex <= sm.savedIndex {
		return nil
	}
	if err := sm.saveLocked(ctx, sm.state.Clone()); err != nil {
		sm.degraded = true
		return err
	}
	return nil
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
	sm.savedIndex, sm.savedAt = st.AppliedRaftIndex, sm.now()
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

// Restore saves and publishes a recovered Controller state snapshot.
func (sm *StateMachine) Restore(ctx context.Context, st state.ClusterState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	next := st.Clone()
	if err := preservePresenceHighWater(sm.state, next); err != nil {
		return err
	}
	if next.Revision != 0 {
		checksum, err := state.Checksum(next)
		if err != nil {
			sm.degraded = true
			return err
		}
		next.Checksum = checksum
		if err := sm.saveLocked(ctx, next); err != nil {
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
//
// Use ApplyBatch when the caller needs task transition audit facts.
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
	single := result.Results[0]
	single.TaskTransitions = nil
	return single, nil
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

		result := sm.applyMutation(&next, entry.Index, entry.Term, entry.Command)
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
	if sm.checkpointDueLocked(next) {
		if err := sm.saveLocked(ctx, next); err != nil {
			sm.degraded = true
			return out, err
		}
	}
	sm.state = next.Clone()
	sm.degraded = false
	out.FinalState = sm.state.Clone()
	return out, nil
}
