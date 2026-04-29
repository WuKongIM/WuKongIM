package plane

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	"github.com/stretchr/testify/require"
)

func TestStateMachineStartAndAdvanceMigrationUpdatesHashSlotTable(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 2)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindStartMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
		},
	}))

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	migration := loaded.GetMigration(3)
	require.NotNil(t, migration)
	require.Equal(t, hashslot.PhaseSnapshot, migration.Phase)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAdvanceMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
			Phase:    uint8(hashslot.PhaseDelta),
		},
	}))

	loaded, err = store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	migration = loaded.GetMigration(3)
	require.NotNil(t, migration)
	require.Equal(t, hashslot.PhaseDelta, migration.Phase)
}

func TestStateMachineFinalizeAndAbortMigrationUpdateHashSlotTable(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.AdvanceMigration(3, hashslot.PhaseDelta)
	table.AdvanceMigration(3, hashslot.PhaseSwitching)
	table.StartMigration(4, 2, 1)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindFinalizeMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
		},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAbortMigration,
		Migration: &MigrationRequest{
			HashSlot: 4,
			Source:   2,
			Target:   1,
		},
	}))

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Nil(t, loaded.GetMigration(3))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(3)))
	require.Nil(t, loaded.GetMigration(4))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(4)))
}

func TestStateMachineAdvanceMigrationIgnoresStaleIdentityAndIllegalTransitions(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAdvanceMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   2,
			Target:   1,
			Phase:    uint8(hashslot.PhaseDelta),
		},
	}))
	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Equal(t, hashslot.PhaseSnapshot, loaded.GetMigration(3).Phase)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAdvanceMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
			Phase:    uint8(hashslot.PhaseSwitching),
		},
	}))
	loaded, err = store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Equal(t, hashslot.PhaseSnapshot, loaded.GetMigration(3).Phase)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAdvanceMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
			Phase:    uint8(hashslot.PhaseDelta),
		},
	}))
	loaded, err = store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Equal(t, hashslot.PhaseDelta, loaded.GetMigration(3).Phase)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAdvanceMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
			Phase:    uint8(hashslot.PhaseSnapshot),
		},
	}))
	loaded, err = store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Equal(t, hashslot.PhaseDelta, loaded.GetMigration(3).Phase)
}

func TestStateMachineFinalizeAndAbortMigrationIgnoreStaleIdentity(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.AdvanceMigration(3, hashslot.PhaseDelta)
	table.AdvanceMigration(3, hashslot.PhaseSwitching)
	table.StartMigration(4, 2, 1)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindFinalizeMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   9,
			Target:   2,
		},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAbortMigration,
		Migration: &MigrationRequest{
			HashSlot: 4,
			Source:   2,
			Target:   9,
		},
	}))

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.NotNil(t, loaded.GetMigration(3))
	require.Equal(t, uint64(1), uint64(loaded.Lookup(3)))
	require.NotNil(t, loaded.GetMigration(4))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(4)))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindFinalizeMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
		},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAbortMigration,
		Migration: &MigrationRequest{
			HashSlot: 4,
			Source:   2,
			Target:   1,
		},
	}))

	loaded, err = store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Nil(t, loaded.GetMigration(3))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(3)))
	require.Nil(t, loaded.GetMigration(4))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(4)))
}

func TestStateMachineMigrationCommandsRequireCompleteIdentity(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.StartMigration(4, 2, 1)
	table.StartMigration(2, 1, 2)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	for _, cmd := range []Command{
		{
			Kind: CommandKindAdvanceMigration,
			Migration: &MigrationRequest{
				HashSlot: 3,
				Target:   2,
				Phase:    uint8(hashslot.PhaseDelta),
			},
		},
		{
			Kind: CommandKindFinalizeMigration,
			Migration: &MigrationRequest{
				HashSlot: 4,
				Source:   2,
			},
		},
		{
			Kind: CommandKindAbortMigration,
			Migration: &MigrationRequest{
				HashSlot: 2,
			},
		},
	} {
		require.NoError(t, sm.Apply(ctx, cmd))
	}

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Equal(t, hashslot.PhaseSnapshot, loaded.GetMigration(3).Phase)
	require.NotNil(t, loaded.GetMigration(4))
	require.NotNil(t, loaded.GetMigration(2))
}

func TestStateMachineFinalizeMigrationRequiresSwitchingPhase(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.StartMigration(4, 2, 1)
	table.AdvanceMigration(4, hashslot.PhaseDelta)
	table.StartMigration(2, 1, 2)
	table.AdvanceMigration(2, hashslot.PhaseDelta)
	table.AdvanceMigration(2, hashslot.PhaseSwitching)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	for _, hashSlot := range []uint16{3, 4} {
		migration := table.GetMigration(hashSlot)
		require.NotNil(t, migration)
		require.NoError(t, sm.Apply(ctx, Command{
			Kind: CommandKindFinalizeMigration,
			Migration: &MigrationRequest{
				HashSlot: hashSlot,
				Source:   uint64(migration.Source),
				Target:   uint64(migration.Target),
			},
		}))
	}
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindFinalizeMigration,
		Migration: &MigrationRequest{
			HashSlot: 2,
			Source:   1,
			Target:   2,
		},
	}))

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.NotNil(t, loaded.GetMigration(3))
	require.Equal(t, uint64(1), uint64(loaded.Lookup(3)))
	require.NotNil(t, loaded.GetMigration(4))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(4)))
	require.Nil(t, loaded.GetMigration(2))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(2)))
}
