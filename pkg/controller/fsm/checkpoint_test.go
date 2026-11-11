package fsm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	"github.com/stretchr/testify/require"
)

// upsertNodeCommand renames node 1 so each committed index changes state.
func upsertNodeCommand(expected uint64, name string) command.Command {
	node := baseNodes()[0]
	node.Name = name
	rev := expected
	return command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &rev, Node: &node}
}

func newCheckpointingStateMachine(t *testing.T, opts ...Option) (*StateMachine, *countingStore) {
	t.Helper()
	store := &countingStore{Store: statefile.New(filepath.Join(t.TempDir(), "cluster-state.json"))}
	sm, err := New(store, opts...)
	require.NoError(t, err)
	return sm, store
}

// Ways checkpointing could break durability or the hot path:
//   - the first materialized state is never written, so tools see no file;
//   - every batch still saves, keeping the fsync amplification (issue #1098);
//   - a checkpoint never happens, so replay grows without bound;
//   - Checkpoint writes a stale index or rewrites an unchanged state;
//   - a loaded file is ignored as the checkpoint baseline.
func TestCheckpointPolicySavesFirstStateThenOnlyWhenDue(t *testing.T) {
	ctx := context.Background()
	sm, store := newCheckpointingStateMachine(t, WithCheckpointPolicy(3, time.Hour))

	_, err := sm.Apply(ctx, 10, initCommand())
	require.NoError(t, err)
	require.Equal(t, int32(1), store.saves.Load(), "the first materialized state is written immediately")

	_, err = sm.Apply(ctx, 11, upsertNodeCommand(1, "a"))
	require.NoError(t, err)
	_, err = sm.Apply(ctx, 12, upsertNodeCommand(2, "b"))
	require.NoError(t, err)
	require.Equal(t, int32(1), store.saves.Load(), "batches before the entry threshold stay in memory")

	_, err = sm.Apply(ctx, 13, upsertNodeCommand(3, "c"))
	require.NoError(t, err)
	require.Equal(t, int32(2), store.saves.Load(), "reaching the entry threshold checkpoints")
	persisted, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(13), persisted.AppliedRaftIndex)
}

func TestCheckpointPolicyIntervalTriggersSave(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_000, 0)
	sm, store := newCheckpointingStateMachine(t, WithCheckpointPolicy(1_000_000, 30*time.Second), WithCheckpointClock(func() time.Time { return now }))

	_, err := sm.Apply(ctx, 10, initCommand())
	require.NoError(t, err)
	_, err = sm.Apply(ctx, 11, upsertNodeCommand(1, "a"))
	require.NoError(t, err)
	require.Equal(t, int32(1), store.saves.Load())

	now = now.Add(31 * time.Second)
	_, err = sm.Apply(ctx, 12, upsertNodeCommand(2, "b"))
	require.NoError(t, err)
	require.Equal(t, int32(2), store.saves.Load())
}

func TestCheckpointPersistsLatestAppliedStateOnce(t *testing.T) {
	ctx := context.Background()
	sm, store := newCheckpointingStateMachine(t, WithCheckpointPolicy(1_000_000, time.Hour))
	_, err := sm.Apply(ctx, 10, initCommand())
	require.NoError(t, err)
	_, err = sm.Apply(ctx, 11, upsertNodeCommand(1, "latest"))
	require.NoError(t, err)

	require.NoError(t, sm.Checkpoint(ctx))
	persisted, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(11), persisted.AppliedRaftIndex)
	require.Equal(t, sm.Snapshot(ctx).Checksum, persisted.Checksum)
	saves := store.saves.Load()

	require.NoError(t, sm.Checkpoint(ctx))
	require.Equal(t, saves, store.saves.Load(), "an unchanged state is not rewritten")
}

func TestCheckpointBaselineFollowsLoadedFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "cluster-state.json")
	seed, err := New(statefile.New(path))
	require.NoError(t, err)
	_, err = seed.Apply(ctx, 10, initCommand())
	require.NoError(t, err)

	store := &countingStore{Store: statefile.New(path)}
	sm, err := New(store, WithCheckpointPolicy(2, time.Hour))
	require.NoError(t, err)
	require.NoError(t, sm.Load(ctx))
	_, err = sm.Apply(ctx, 11, upsertNodeCommand(1, "a"))
	require.NoError(t, err)
	require.Equal(t, int32(0), store.saves.Load(), "the loaded file is the checkpoint baseline")
	_, err = sm.Apply(ctx, 12, upsertNodeCommand(2, "b"))
	require.NoError(t, err)
	require.Equal(t, int32(1), store.saves.Load())
}
