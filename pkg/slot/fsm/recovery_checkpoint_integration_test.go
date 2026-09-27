//go:build integration

package fsm

import (
	"bytes"
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure matrix: legacy migration commands may touch a non-owned partition;
// incoming ownership may overlap; a changed ownership proof is not evidence of
// disjoint writes. All must retain the conservative global invalidation fence.
func TestRecoveryCheckpointUncertainOwnershipInvalidatesGlobally(t *testing.T) {
	for _, mode := range []string{"migration_batch", "incoming", "changed_proof"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db, err := metadb.Open(t.TempDir())
			require.NoError(t, err)
			defer db.Close()
			require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
			sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
			require.NoError(t, err)
			m := sm.(*stateMachine)
			b := db.NewWriteBatch()
			require.NoError(t, b.SetSlotRecoveryCheckpoint(22, 8, []byte("neighbor")))
			require.NoError(t, b.Commit())
			require.NoError(t, b.Close())
			epoch := db.MetaDB().RecoveryEpoch()
			cmd := multiraft.Command{SlotID: 11, HashSlot: 5, Index: 1, Term: 1, Data: EncodeNoopCommand()}
			switch mode {
			case "migration_batch":
				m.UpdateOutgoingDeltaTargets(map[uint16]multiraft.SlotID{5: 22})
				fence := cmd
				fence.Data = EncodeEnterFenceCommandForTarget(5, 22)
				cmd.Index = 2
				_, err = m.ApplyBatch(ctx, []multiraft.Command{fence, cmd})
			case "incoming":
				m.UpdateIncomingDeltaHashSlots([]uint16{6})
				_, err = m.Apply(ctx, cmd)
			case "changed_proof":
				cmd.Checkpoint = &multiraft.RecoveryCheckpoint{Version: 1, SlotID: 11, AppliedIndex: 1, AppliedTerm: 1, LiveEpoch: epoch}
				_, err = m.Apply(ctx, cmd)
			}
			require.NoError(t, err)
			require.Greater(t, db.MetaDB().RecoveryEpoch(), epoch)
			if mode == "incoming" {
				state, err := m.RecoveryState(ctx)
				require.NoError(t, err)
				require.False(t, state.Enabled, "overlapping migration state must not establish a new checkpoint anchor")
			}
		})
	}
}

func TestRecoveryCheckpointStartupRejectsOverlappingMigration(t *testing.T) {
	ctx := context.Background()
	db, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	require.NoError(t, err)
	m := sm.(*stateMachine)
	m.UpdateIncomingDeltaHashSlots([]uint16{6})
	snap, err := m.Snapshot(ctx)
	require.NoError(t, err)
	snap.Index = 1
	err = m.RestoreStartupSnapshot(ctx, snap, bytes.NewReader(snap.Data), int64(len(snap.Data)), nil)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}
