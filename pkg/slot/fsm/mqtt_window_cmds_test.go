package fsm

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func mqttWindowCommandFixture() metadb.MQTTWindowMutation {
	return metadb.MQTTWindowMutation{Key: mqttDeliveryCursorCommandFixture().Key, ExpectedRevision: 5,
		OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot-a", ConnectionID: 4, Op: metadb.MQTTWindowAdmit,
		Publication: metadb.MQTTInflightPublication{Position: 101, MessageID: 1001, MessageSeq: 101, ContentVersion: 1, ContentHash: strings.Repeat("a", 64), Bytes: 100}, UpdatedAtMS: 2000}
}

func TestMQTTWindowCommandOrderedApplySnapshotAndDeletedACKRetry(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	require.NoError(t, err)
	session := mqttSessionCommandFixture()
	session.NextPacketID = 65535
	sub := mqttSubscriptionCommandFixture()
	active := sub
	active.ExpectedRevision = 2
	active.Subscription.Revision = 3
	active.Subscription.Stage, active.Subscription.RecoveryAtMS = metadb.MQTTSubscriptionActive, 0
	init := mqttDeliveryCursorCommandFixture()
	init.ExpectedRevision = 3
	account := init
	account.ExpectedRevision, account.Op, account.Through, account.AddedMessages, account.AddedBytes = 4, metadb.MQTTCursorAccount, 104, 3, 300
	checked := func(data []byte, err error) []byte { t.Helper(); require.NoError(t, err); return data }
	raws := [][]byte{checked(EncodeMQTTSessionCASCommand(0, session)), checked(EncodeMQTTSubscriptionCommand(sub)),
		checked(EncodeMQTTSubscriptionCommand(active)), checked(EncodeMQTTDeliveryCursorCommand(init)), checked(EncodeMQTTDeliveryCursorCommand(account))}
	first := mqttWindowCommandFixture()
	second := first
	second.ExpectedRevision = 6
	second.Publication.Position, second.Publication.MessageID, second.Publication.MessageSeq = 103, 1003, 103
	ack := first
	ack.ExpectedRevision, ack.Op, ack.Publication, ack.PacketID, ack.DeliveryOrder = 7, metadb.MQTTWindowAck, metadb.MQTTInflightPublication{}, 1, 2
	raws = append(raws, checked(EncodeMQTTWindowCommand(first)), checked(EncodeMQTTWindowCommand(second)), checked(EncodeMQTTWindowCommand(ack)))
	var commands []multiraft.Command
	for i, raw := range raws {
		commands = append(commands, multiraft.Command{SlotID: 11, HashSlot: 7, Index: uint64(i + 1), Term: 1, Data: raw})
	}
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, commands)
	require.NoError(t, err)
	for i, pid := range []uint16{65535, 1, 1} {
		var result metadb.MQTTWindowResult
		require.NoError(t, json.Unmarshal(results[5+i], &result))
		require.Equal(t, metadb.MQTTWindowApplied, result.Status)
		require.Equal(t, pid, result.PacketID)
	}
	require.Equal(t, []byte{1, 70}, raws[5][:2])
	cursor, _, err := db.ForHashSlot(7).GetMQTTDeliveryCursor(ctx, first.Key)
	require.NoError(t, err)
	require.EqualValues(t, 100, cursor.CompletedThrough)
	require.EqualValues(t, 1, cursor.InflightCount)
	stale := ack
	stale.ExpectedRevision, stale.OwnerGeneration = 8, 2
	staleRaw := checked(EncodeMQTTWindowCommand(stale))
	raw, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 7, Index: 9, Term: 1, Data: staleRaw})
	require.NoError(t, err)
	var result metadb.MQTTWindowResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, metadb.MQTTWindowConflict, result.Status)
	for _, command := range []multiraft.Command{
		{SlotID: 12, HashSlot: 7, Index: 10, Term: 1, Data: staleRaw}, {SlotID: 11, HashSlot: 8, Index: 10, Term: 1, Data: staleRaw},
	} {
		_, err = sm.Apply(ctx, command)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 9, index)
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{7})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	records, _, done, err := target.ForHashSlot(7).ListMQTTInflight(ctx, "main", "phone", 1, metadb.MQTTOutbound, metadb.MQTTInflightCursor{}, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Len(t, records, 1)
	require.EqualValues(t, 65535, records[0].PacketID)
	require.Equal(t, first.Publication, records[0].Publication)
	exact, found, err := target.ForHashSlot(7).GetMQTTInflight(ctx, "main", "phone", 1, metadb.MQTTOutbound, 65535)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, records[0], exact)
	retry := commands[7]
	retry.Index = 10
	raw, err = restored.Apply(ctx, retry)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, metadb.MQTTWindowUnchanged, result.Status)
	require.EqualValues(t, 1, result.PacketID)
	inspection, err := DecodeCommandInspection(raws[5])
	require.NoError(t, err)
	require.Equal(t, "mqtt_window", inspection.Type)
	require.EqualValues(t, 1001, inspection.Payload["message_id"])
}

func TestMQTTWindowCommandRejectsInvalidInput(t *testing.T) {
	m := mqttWindowCommandFixture()
	valid, err := EncodeMQTTWindowCommand(m)
	require.NoError(t, err)
	m.Op = 0
	_, err = EncodeMQTTWindowCommand(m)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	for _, raw := range [][]byte{
		{1, 70}, append([]byte{1, 70}, bytes.Repeat([]byte(" "), 33<<10)...),
		append([]byte{1, 70}, []byte(`{"version":2}`)...), append([]byte{1, 70}, []byte(`{"version":1}`)...),
		append(append([]byte(nil), valid...), []byte(` {}`)...), bytes.Replace(valid, []byte(`"op":1`), []byte(`"op":1,"future":1`), 1),
	} {
		_, err = DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
