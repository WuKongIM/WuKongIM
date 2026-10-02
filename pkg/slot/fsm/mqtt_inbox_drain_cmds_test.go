package fsm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestMQTTInboxDrainCommandSurvivesSnapshotAndReplay(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{9})
	require.NoError(t, err)
	row := mqttSourceBindingCommandFixture()
	row.Key.Owner = metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingUID, ID: "alice"}
	old, err := EncodeMQTTSourceBindingCommand(0, row)
	require.NoError(t, err)
	require.NotContains(t, string(old), "drain_")
	encode := func(r metadb.MQTTSourceBinding, index uint64) multiraft.Command {
		raw, err := EncodeMQTTSourceBindingCommand(r.Revision-1, r)
		require.NoError(t, err)
		require.Equal(t, []byte{1, 71}, raw[:2])
		return multiraft.Command{SlotID: 11, HashSlot: 9, Index: index, Term: 1, Data: raw}
	}
	apply := func(machine multiraft.StateMachine, r metadb.MQTTSourceBinding, index uint64, status metadb.MQTTSessionCASStatus) {
		raw, err := machine.Apply(ctx, encode(r, index))
		require.NoError(t, err)
		var result metadb.MQTTSourceBindingResult
		require.NoError(t, json.Unmarshal(raw, &result))
		require.Equal(t, status, result.Status)
	}
	apply(sm, row, 1, metadb.MQTTSessionCASApplied)
	row.Revision, row.Stage, row.IntentRevision, row.DrainVersion = 2, metadb.MQTTBindingRemoving, 4, 1
	apply(sm, row, 2, metadb.MQTTSessionCASApplied)
	row.Revision, row.ProgressRevision, row.DrainAfterSourceID, row.DrainAfterSourceGeneration = 3, 5, strings.Repeat("s", 4096), strings.Repeat("g", 128)
	apply(sm, row, 3, metadb.MQTTSessionCASApplied)
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{9})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	got, found, err := target.ForHashSlot(9).GetMQTTSourceBinding(ctx, row.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, row, got)
	apply(restored, row, 4, metadb.MQTTSessionCASUnchanged)
	row.Revision++
	row.DrainDone = true
	apply(restored, row, 5, metadb.MQTTSessionCASApplied)
	row.Revision++
	row.Stage, row.ReleaseReason, row.RecoveryAtMS = metadb.MQTTBindingRemoved, metadb.MQTTBindingDrained, 0
	apply(restored, row, 6, metadb.MQTTSessionCASApplied)
	regressed := row
	regressed.Revision++
	regressed.Stage, regressed.ReleaseReason, regressed.RecoveryAtMS = metadb.MQTTBindingRemoving, 0, 1000
	apply(restored, regressed, 7, metadb.MQTTSessionCASConflict)
}
