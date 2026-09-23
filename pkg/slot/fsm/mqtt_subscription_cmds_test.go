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

func mqttSubscriptionCommandFixture() metadb.MQTTSubscriptionMutation {
	return metadb.MQTTSubscriptionMutation{ExpectedRevision: 1, OwnerGeneration: 1,
		OwnerNodeID: 1, OwnerBootID: "boot-a", ConnectionID: 4,
		Subscription: metadb.MQTTSubscription{Namespace: "main", ClientID: "phone", SessionGeneration: 1,
			Topic: "wk/v1/groups/Zw/messages", Generation: 2, Revision: 2, TargetKind: metadb.MQTTSubscriptionGroup,
			TargetID: "g", GrantedQoS: 1, Stage: metadb.MQTTSubscriptionPreparing, OperationID: "subscribe-1",
			RecoveryAtMS: 1000, UpdatedAtMS: 1000}}
}

func TestMQTTSubscriptionCommandAtomicFencingSnapshotAndReplay(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	require.NoError(t, err)
	session := mqttSessionCommandFixture()
	raw, err := EncodeMQTTSessionCASCommand(0, session)
	require.NoError(t, err)
	first := multiraft.Command{SlotID: 11, HashSlot: 7, Index: 1, Term: 1, Data: raw}
	prepare := mqttSubscriptionCommandFixture()
	encode := func(m metadb.MQTTSubscriptionMutation, index uint64) multiraft.Command {
		m.Subscription.Revision = m.ExpectedRevision + 1
		raw, err := EncodeMQTTSubscriptionCommand(m)
		require.NoError(t, err)
		require.Equal(t, []byte{1, 68}, raw[:2])
		return multiraft.Command{SlotID: 11, HashSlot: 7, Index: index, Term: 1, Data: raw}
	}
	active := prepare
	active.ExpectedRevision = 2
	active.Subscription.Revision = 3
	active.Subscription.Stage, active.Subscription.RecoveryAtMS = metadb.MQTTSubscriptionActive, 0
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{first, encode(prepare, 2), encode(active, 3), encode(prepare, 4)})
	require.NoError(t, err)
	for i, status := range []metadb.MQTTSessionCASStatus{metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASConflict} {
		var result metadb.MQTTSessionCASResult
		require.NoError(t, json.Unmarshal(results[i], &result))
		require.Equal(t, status, result.Status)
	}
	stale := active
	stale.ExpectedRevision, stale.OwnerGeneration = 3, 2
	_, err = sm.Apply(ctx, encode(stale, 5))
	require.NoError(t, err)
	got, found, err := db.ForHashSlot(7).GetMQTTSubscription(ctx, "main", "phone", 1, active.Subscription.Topic)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, active.Subscription, got)
	gotSession, _, err := db.ForHashSlot(7).GetMQTTSession(ctx, "main", "phone")
	require.NoError(t, err)
	require.EqualValues(t, 3, gotSession.Revision)
	page, _, done, err := db.ForHashSlot(7).ListMQTTSubscriptions(ctx, "main", "phone", 1, "", 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []metadb.MQTTSubscription{got}, page)
	recovery, _, _, err := db.ForHashSlot(7).ListMQTTSubscriptionRecovery(ctx, metadb.MQTTSubscriptionRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Empty(t, recovery)
	for _, c := range []multiraft.Command{
		{SlotID: 12, HashSlot: 7, Index: 6, Term: 1, Data: encode(active, 6).Data},
		{SlotID: 11, HashSlot: 8, Index: 6, Term: 1, Data: encode(active, 6).Data},
	} {
		_, err = sm.Apply(ctx, c)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 5, index)
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{7})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	got, found, err = target.ForHashSlot(7).GetMQTTSubscription(ctx, "main", "phone", 1, active.Subscription.Topic)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, active.Subscription, got)
	raw, err = restored.Apply(ctx, encode(active, 6))
	require.NoError(t, err)
	var retry metadb.MQTTSessionCASResult
	require.NoError(t, json.Unmarshal(raw, &retry))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, retry.Status)
	inspection, err := DecodeCommandInspection(encode(prepare, 2).Data)
	require.NoError(t, err)
	require.Equal(t, "mqtt_subscription_mutation", inspection.Type)
	require.Equal(t, "phone", inspection.Payload["client_id"])
	require.Equal(t, prepare.Subscription.Topic, inspection.Payload["topic"])
}

func TestMQTTSubscriptionCommandRejectsMalformedOrUnboundedInput(t *testing.T) {
	m := mqttSubscriptionCommandFixture()
	valid, err := EncodeMQTTSubscriptionCommand(m)
	require.NoError(t, err)
	m.OwnerBootID = ""
	_, err = EncodeMQTTSubscriptionCommand(m)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	for _, raw := range [][]byte{
		{1, 68}, append([]byte{1, 68}, bytes.Repeat([]byte(" "), 33<<10)...),
		append([]byte{1, 68}, []byte(`{"version":2}`)...),
		append([]byte{1, 68}, []byte(`{"version":1}`)...),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		append([]byte{1, 68}, append([]byte(`{"unknown":1,`), valid[3:]...)...),
		bytes.Replace(valid, []byte(`"connection_id":4`), []byte(`"connection_id":4,"future":1`), 1),
	} {
		_, err := DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
