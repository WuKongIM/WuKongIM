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
	table.StartMigration(4, 2, 1)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindFinalizeMigration,
		Migration: &MigrationRequest{
			HashSlot: 3,
		},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAbortMigration,
		Migration: &MigrationRequest{
			HashSlot: 4,
		},
	}))

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Nil(t, loaded.GetMigration(3))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(3)))
	require.Nil(t, loaded.GetMigration(4))
	require.Equal(t, uint64(2), uint64(loaded.Lookup(4)))
}
