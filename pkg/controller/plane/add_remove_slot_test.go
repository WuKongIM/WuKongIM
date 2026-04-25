package plane

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func countsAfterMigrations(table *hashslot.HashSlotTable) map[uint64]int {
	counts := make(map[uint64]int)
	for hashSlot := uint16(0); hashSlot < table.HashSlotCount(); hashSlot++ {
		slotID := uint64(table.Lookup(hashSlot))
		if slotID != 0 {
			counts[slotID]++
		}
	}
	for _, migration := range table.ActiveMigrations() {
		counts[uint64(migration.Source)]--
		counts[uint64(migration.Target)]++
	}
	return counts
}

func TestStateMachineAddSlotCreatesAssignmentAndMigrations(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, store.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 2)))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindAddSlot,
		AddSlot: &AddSlotRequest{
			NewSlotID: 3,
			Peers:     []uint64{1, 2, 3},
		},
	}))

	assignment, err := store.GetAssignment(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint32(3), assignment.SlotID)
	require.Equal(t, []uint64{1, 2, 3}, assignment.DesiredPeers)

	table, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)

	migrations := table.ActiveMigrations()
	require.Len(t, migrations, 2)
	for _, migration := range migrations {
		require.Equal(t, uint64(3), uint64(migration.Target))
		require.Equal(t, hashslot.PhaseSnapshot, migration.Phase)
		require.Equal(t, uint64(migration.Source), uint64(table.Lookup(migration.HashSlot)))
	}

	counts := countsAfterMigrations(table)
	require.Equal(t, 3, counts[1])
	require.Equal(t, 3, counts[2])
	require.Equal(t, 2, counts[3])

	task, err := store.GetTask(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint32(3), task.SlotID)
	require.Equal(t, controllermeta.TaskKindBootstrap, task.Kind)
	require.Equal(t, controllermeta.TaskStepAddLearner, task.Step)
	require.Equal(t, uint64(1), task.TargetNode)
	require.Equal(t, controllermeta.TaskStatusPending, task.Status)
}

func TestStateMachineRemoveSlotCreatesMigrationsWithoutDeletingAssignmentYet(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 3)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))
	require.NoError(t, store.UpsertAssignment(ctx, assignmentWithPeers(1, 1, 2, 3)))
	require.NoError(t, store.UpsertAssignment(ctx, assignmentWithPeers(2, 1, 2, 3)))
	require.NoError(t, store.UpsertAssignment(ctx, assignmentWithPeers(3, 1, 2, 3)))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindRemoveSlot,
		RemoveSlot: &RemoveSlotRequest{
			SlotID: 2,
		},
	}))

	assignment, err := store.GetAssignment(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint32(2), assignment.SlotID)

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)

	migrations := loaded.ActiveMigrations()
	require.Len(t, migrations, 3)
	for _, migration := range migrations {
		require.Equal(t, uint64(2), uint64(migration.Source))
		require.NotEqual(t, uint64(2), uint64(migration.Target))
		require.Equal(t, hashslot.PhaseSnapshot, migration.Phase)
	}

	counts := countsAfterMigrations(loaded)
	require.Equal(t, 0, counts[2])
	require.Equal(t, 4, counts[1])
	require.Equal(t, 4, counts[3])
}

func TestStateMachineFinalizeMigrationDeletesDrainedSlotAssignment(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 3)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))
	require.NoError(t, store.UpsertAssignment(ctx, assignmentWithPeers(1, 1, 2, 3)))
	require.NoError(t, store.UpsertAssignment(ctx, assignmentWithPeers(2, 1, 2, 3)))
	require.NoError(t, store.UpsertAssignment(ctx, assignmentWithPeers(3, 1, 2, 3)))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindRemoveSlot,
		RemoveSlot: &RemoveSlotRequest{
			SlotID: 2,
		},
	}))

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	for _, migration := range loaded.ActiveMigrations() {
		require.NoError(t, sm.Apply(ctx, Command{
			Kind: CommandKindFinalizeMigration,
			Migration: &MigrationRequest{
				HashSlot: migration.HashSlot,
				Source:   uint64(migration.Source),
				Target:   uint64(migration.Target),
			},
		}))
	}

	_, err = store.GetAssignment(ctx, 2)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)

	assignments, err := store.ListAssignments(ctx)
	require.NoError(t, err)
	require.Len(t, assignments, 2)

	finalTable, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.Empty(t, finalTable.ActiveMigrations())
	require.Empty(t, finalTable.HashSlotsOf(2))
	require.Len(t, finalTable.HashSlotsOf(1), 4)
	require.Len(t, finalTable.HashSlotsOf(3), 4)
}

func assignmentWithPeers(slotID uint32, peers ...uint64) controllermeta.SlotAssignment {
	return controllermeta.SlotAssignment{
		SlotID:       slotID,
		DesiredPeers: append([]uint64(nil), peers...),
		ConfigEpoch:  1,
	}
}
