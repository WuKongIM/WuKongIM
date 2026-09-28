package fsm

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestMQTTSourceBindingRetireCommandFencesAcrossSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{9})
	require.NoError(t, err)
	cas := func(expected uint64, row metadb.MQTTSourceBinding) []byte {
		raw, err := EncodeMQTTSourceBindingCommand(expected, row)
		require.NoError(t, err)
		return raw
	}
	r := mqttSourceBindingCommandFixture()
	removing := r
	removing.Revision, removing.Stage, removing.ReleaseReason, removing.ProgressRevision = 2, metadb.MQTTBindingRemoving, metadb.MQTTBindingSessionEnded, 2
	removed := removing
	removed.Revision, removed.Stage, removed.RecoveryAtMS, removed.ProtectionRevision = 3, metadb.MQTTBindingRemoved, 0, 2
	retire, err := EncodeMQTTSourceBindingRetireCommand(r.Key, 3, 1)
	require.NoError(t, err)
	require.Equal(t, []byte{1, 77}, retire[:2])
	var commands []multiraft.Command
	for i, raw := range [][]byte{cas(0, r), cas(1, removing), cas(2, removed), retire, retire} {
		commands = append(commands, multiraft.Command{SlotID: 11, HashSlot: 9, Index: uint64(i + 1), Term: 1, Data: raw})
	}
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, commands)
	require.NoError(t, err)
	for i, want := range []metadb.MQTTSessionCASStatus{metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASConflict} {
		var got metadb.MQTTSourceBindingResult
		require.NoError(t, json.Unmarshal(results[i+3], &got))
		require.Equal(t, want, got.Status)
	}
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{9})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	res, err := restored.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 9, Index: 6, Term: 1, Data: cas(0, r)})
	require.NoError(t, err)
	var got metadb.MQTTSourceBindingResult
	require.NoError(t, json.Unmarshal(res, &got))
	require.Equal(t, metadb.MQTTSessionCASConflict, got.Status, "restored fence must reject resurrection")

	view, err := DecodeCommandInspection(retire)
	require.NoError(t, err)
	require.Equal(t, "mqtt_source_binding_retire", view.Type)
	_, err = EncodeMQTTSourceBindingRetireCommand(r.Key, 3, 0)
	require.Error(t, err)
	for _, raw := range [][]byte{{1, 77}, append(bytes.Clone(retire), []byte(`{}`)...), append([]byte{1, 77}, []byte(`{"version":2}`)...)} {
		_, err := DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
