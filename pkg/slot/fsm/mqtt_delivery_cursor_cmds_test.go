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

func mqttDeliveryCursorCommandFixture() metadb.MQTTDeliveryCursorMutation {
	return metadb.MQTTDeliveryCursorMutation{Key: metadb.MQTTDeliveryCursorKey{Namespace: "main", ClientID: "phone", SessionGeneration: 1,
		SubscriptionGeneration: 2, SourceKind: metadb.MQTTSourceChannel, SourceID: "group:g", SourceGeneration: "incarnation-1"},
		ExpectedRevision: 2, OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot-a", ConnectionID: 4,
		Op: metadb.MQTTCursorInit, Topic: mqttSubscriptionCommandFixture().Subscription.Topic, Through: 100, UpdatedAtMS: 1000}
}

func TestMQTTDeliveryCursorCommandAtomicAccountingAndSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	require.NoError(t, err)
	sessionRaw, err := EncodeMQTTSessionCASCommand(0, mqttSessionCommandFixture())
	require.NoError(t, err)
	subRaw, err := EncodeMQTTSubscriptionCommand(mqttSubscriptionCommandFixture())
	require.NoError(t, err)
	command := func(raw []byte, index uint64) multiraft.Command {
		return multiraft.Command{SlotID: 11, HashSlot: 7, Index: index, Term: 1, Data: raw}
	}
	encode := func(m metadb.MQTTDeliveryCursorMutation, index uint64) multiraft.Command {
		raw, err := EncodeMQTTDeliveryCursorCommand(m)
		require.NoError(t, err)
		require.Equal(t, []byte{1, 69}, raw[:2])
		return command(raw, index)
	}
	initial := mqttDeliveryCursorCommandFixture()
	account := initial
	account.ExpectedRevision, account.Op, account.Through, account.AddedMessages, account.AddedBytes = 3, metadb.MQTTCursorAccount, 104, 3, 120
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{command(sessionRaw, 1), command(subRaw, 2), encode(initial, 3), encode(account, 4), encode(initial, 5)})
	require.NoError(t, err)
	for i, status := range []metadb.MQTTSessionCASStatus{metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASConflict} {
		var result metadb.MQTTDeliveryCursorResult
		require.NoError(t, json.Unmarshal(results[i+2], &result))
		require.Equal(t, status, result.Status)
	}
	before, found, err := db.ForHashSlot(7).GetMQTTDeliveryCursor(ctx, account.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 104, before.AccountedThrough)
	require.EqualValues(t, 100, before.CompletedThrough)
	pages, _, done, err := db.ForHashSlot(7).ListMQTTDeliveryCursors(ctx, "main", "phone", 1, 0, metadb.MQTTDeliveryCursorKey{}, 1)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []metadb.MQTTDeliveryCursor{before}, pages)
	stale := account
	stale.ExpectedRevision, stale.OwnerGeneration, stale.Through = 4, 2, 105
	raw, err := sm.Apply(ctx, encode(stale, 6))
	require.NoError(t, err)
	var result metadb.MQTTDeliveryCursorResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, metadb.MQTTSessionCASConflict, result.Status)
	for _, c := range []multiraft.Command{
		{SlotID: 12, HashSlot: 7, Index: 7, Term: 1, Data: encode(account, 7).Data},
		{SlotID: 11, HashSlot: 8, Index: 7, Term: 1, Data: encode(account, 7).Data},
	} {
		_, err = sm.Apply(ctx, c)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 6, index)
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{7})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	got, found, err := target.ForHashSlot(7).GetMQTTDeliveryCursor(ctx, account.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, before, got)
	aggregate, _, err := target.ForHashSlot(7).GetMQTTSession(ctx, "main", "phone")
	require.NoError(t, err)
	require.EqualValues(t, 3, aggregate.PendingMessages)
	require.EqualValues(t, 120, aggregate.PendingBytes)
	raw, err = restored.Apply(ctx, encode(account, 7))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, result.Status)
	inspection, err := DecodeCommandInspection(encode(account, 4).Data)
	require.NoError(t, err)
	require.Equal(t, "mqtt_delivery_cursor", inspection.Type)
	require.Equal(t, "group:g", inspection.Payload["source_id"])
}

func TestMQTTDeliveryCursorCommandRejectsInvalidInput(t *testing.T) {
	m := mqttDeliveryCursorCommandFixture()
	valid, err := EncodeMQTTDeliveryCursorCommand(m)
	require.NoError(t, err)
	m.Op = 0
	_, err = EncodeMQTTDeliveryCursorCommand(m)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	for _, raw := range [][]byte{
		{1, 69}, append([]byte{1, 69}, bytes.Repeat([]byte(" "), 33<<10)...),
		append([]byte{1, 69}, []byte(`{"version":2}`)...),
		append([]byte{1, 69}, []byte(`{"version":1}`)...),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		bytes.Replace(valid, []byte(`"op":1`), []byte(`"op":1,"future":1`), 1),
	} {
		_, err = DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
