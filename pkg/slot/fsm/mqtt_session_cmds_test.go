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

func mqttSessionCommandFixture() metadb.MQTTSession {
	return metadb.MQTTSession{Namespace: "main", ClientID: "phone", UID: "alice", Generation: 1, Revision: 1,
		OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot-a", ConnectionID: 4, LeaseUntilMS: 5000,
		State: metadb.MQTTSessionActive, SessionExpirySec: 86400, DeviceFlag: 0, ReceiveMaximum: 64,
		MaxPacketBytes: 1 << 20, NextPacketID: 1, NextDeliveryOrder: 1, QuotaMessages: 10000,
		QuotaBytes: 64 << 20, UpdatedAtMS: 1000}
}

func TestMQTTSessionCommandCASOwnershipSnapshotAndReplay(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	require.NoError(t, err)
	row := mqttSessionCommandFixture()
	encode := func(r metadb.MQTTSession, expected, index uint64) multiraft.Command {
		raw, err := EncodeMQTTSessionCASCommand(expected, r)
		require.NoError(t, err)
		require.Equal(t, []byte{1, 67}, raw[:2])
		return multiraft.Command{SlotID: 11, HashSlot: 7, Index: index, Term: 1, Data: raw}
	}
	first := encode(row, 0, 1)
	row.Revision, row.OwnerGeneration, row.OwnerNodeID, row.ConnectionID = 2, 2, 2, 8
	second := encode(row, 1, 2)
	stale := encode(mqttSessionCommandFixture(), 0, 3)
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{first, second, stale})
	require.NoError(t, err)
	for i, status := range []metadb.MQTTSessionCASStatus{metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASConflict} {
		var result metadb.MQTTSessionCASResult
		require.NoError(t, json.Unmarshal(results[i], &result))
		require.Equal(t, status, result.Status)
	}
	got, found, err := db.ForHashSlot(7).GetMQTTSession(ctx, row.Namespace, row.ClientID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, row, got)
	for _, command := range []multiraft.Command{
		{SlotID: 11, HashSlot: 8, Index: 4, Term: 1, Data: second.Data},
		{SlotID: 12, HashSlot: 7, Index: 4, Term: 1, Data: second.Data},
	} {
		_, err := sm.Apply(ctx, command)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 3, index)
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{7})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	got, found, err = target.ForHashSlot(7).GetMQTTSession(ctx, row.Namespace, row.ClientID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, row, got)
	second.Index = 4
	raw, err := restored.Apply(ctx, second)
	require.NoError(t, err)
	var result metadb.MQTTSessionCASResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, result.Status)
	inspection, err := DecodeCommandInspection(first.Data)
	require.NoError(t, err)
	require.Equal(t, "mqtt_session_cas", inspection.Type)
	require.Equal(t, "phone", inspection.Payload["client_id"])
	_, token := inspection.Payload["token"]
	require.False(t, token)
}

func TestMQTTSessionCommandRejectsInvalidOrUnboundedInput(t *testing.T) {
	row := mqttSessionCommandFixture()
	_, err := EncodeMQTTSessionCASCommand(3, row)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	valid, err := EncodeMQTTSessionCASCommand(0, row)
	require.NoError(t, err)
	body := valid[2:]
	for _, data := range [][]byte{
		{1, 67}, append([]byte{1, 67}, bytes.Repeat([]byte(" "), 33<<10)...),
		append([]byte{1, 67}, []byte(`{"version":2}`)...),
		append([]byte{1, 67}, []byte(`{"version":1}`)...),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		append([]byte{1, 67}, append([]byte(`{"unexpected":1,`), body[1:]...)...),
	} {
		_, err := DecodeCommandInspection(data)
		require.Error(t, err)
	}
}
