package fsm

import (
	"bytes"
	"context"
	"encoding/json"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTReclamationIndexCommandSnapshotAndOwnership(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, e := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	require.NoError(t, e)
	s := mqttSessionCommandFixture()
	create, e := EncodeMQTTSessionCASCommand(0, s)
	require.NoError(t, e)
	s.Revision++
	s.Generation++
	s.OwnerGeneration++
	change, e := EncodeMQTTSessionCASCommand(1, s)
	require.NoError(t, e)
	build := EncodeMQTTReclamationIndexCommand()
	require.Equal(t, []byte{1, 76}, build[:2])
	var commands []multiraft.Command
	for i, raw := range [][]byte{create, change, build, build} {
		commands = append(commands, multiraft.Command{SlotID: 11, HashSlot: 7, Index: uint64(i + 1), Term: 1, Data: raw})
	}
	results, e := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, commands)
	require.NoError(t, e)
	for i, want := range []metadb.MQTTReclamationIndexResult{{Scanned: 1, Done: true}, {Done: true}} {
		var got metadb.MQTTReclamationIndexResult
		require.NoError(t, json.Unmarshal(results[i+2], &got))
		require.Equal(t, want, got)
	}
	for _, ids := range [][2]uint16{{12, 7}, {11, 8}} {
		_, e = sm.Apply(ctx, multiraft.Command{SlotID: multiraft.SlotID(ids[0]), HashSlot: ids[1], Index: 5, Term: 1, Data: build})
		require.ErrorIs(t, e, metadb.ErrInvalidArgument)
	}
	snapshot, e := sm.Snapshot(ctx)
	require.NoError(t, e)
	target := openTestDB(t)
	restored, e := NewStateMachineWithHashSlots(target, 11, []uint16{7})
	require.NoError(t, e)
	require.NoError(t, restored.Restore(ctx, snapshot))
	page, e := target.ReadMQTTState(ctx, 7, metadb.MQTTRead{Kind: metadb.MQTTReadSessionReclamation, Limit: 1})
	require.NoError(t, e)
	require.Equal(t, []metadb.MQTTSession{s}, page.Sessions)
	view, e := DecodeCommandInspection(build)
	require.NoError(t, e)
	require.Equal(t, "mqtt_reclamation_index", view.Type)
	for _, raw := range [][]byte{{1, 76}, append([]byte{1, 76}, bytes.Repeat([]byte(" "), 129)...), append(bytes.Clone(build), []byte(`{}`)...), append([]byte{1, 76}, []byte(`{"version":2}`)...), append([]byte{1, 76}, []byte(`{"version":1,"client_id":"x"}`)...)} {
		_, e := DecodeCommandInspection(raw)
		require.Error(t, e)
	}
}
