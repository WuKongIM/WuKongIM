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

func mqttLifecycleCommandFixture() metadb.MQTTLifecycleMutation {
	m := metadb.MQTTLifecycleMutation{Event: metadb.MQTTLifecycleConnect, Session: mqttSessionCommandFixture()}
	m.Will = mqttLifecycleCommandWill(m.Session)
	return m
}
func mqttLifecycleCommandWill(s metadb.MQTTSession) *metadb.MQTTWill {
	w := mqttWillCommandFixture()
	w.Key = metadb.MQTTWillKey{Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.Generation, WillGeneration: s.Revision}
	w.UID, w.OwnerGeneration, w.OwnerNodeID, w.OwnerBootID, w.ConnectionID = s.UID, s.OwnerGeneration, s.OwnerNodeID, s.OwnerBootID, s.ConnectionID
	w.DecisionRevision, w.UpdatedAtMS = s.Revision, s.UpdatedAtMS
	w.IdempotencyKey, _ = metadb.MQTTWillIdempotencyKey(w.Key)
	return &w
}

func TestMQTTLifecycleCommandAtomicBatchOwnerChangeSnapshotAndRetry(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	require.NoError(t, err)
	encode := func(m metadb.MQTTLifecycleMutation, index uint64) multiraft.Command {
		raw, err := EncodeMQTTLifecycleCommand(m)
		require.NoError(t, err)
		require.Equal(t, []byte{1, 73}, raw[:2])
		return multiraft.Command{SlotID: 11, HashSlot: 7, Index: index, Term: 1, Data: raw}
	}
	initial := mqttLifecycleCommandFixture()
	owner := initial.Session
	close := metadb.MQTTLifecycleMutation{ExpectedRevision: 1, ExpectedGeneration: 1, OwnerGeneration: owner.OwnerGeneration, OwnerNodeID: owner.OwnerNodeID, OwnerBootID: owner.OwnerBootID, ConnectionID: owner.ConnectionID, Event: metadb.MQTTLifecycleDisconnectWithWill, Session: owner}
	close.Session.Revision, close.Session.UpdatedAtMS, close.Session.State, close.Session.LeaseUntilMS, close.Session.SessionExpirySec, close.Session.OfflineExpiresAtMS = 2, 2000, metadb.MQTTSessionOffline, 0, 20, 22000
	reconnect := close
	reconnect.ExpectedRevision = 2
	reconnect.Event = metadb.MQTTLifecycleConnect
	reconnect.Session.Revision, reconnect.Session.UpdatedAtMS, reconnect.Session.State, reconnect.Session.LeaseUntilMS, reconnect.Session.OfflineExpiresAtMS = 3, 3000, metadb.MQTTSessionActive, 8000, 0
	reconnect.Session.OwnerGeneration, reconnect.Session.OwnerNodeID, reconnect.Session.OwnerBootID, reconnect.Session.ConnectionID = 2, 2, "boot-b", 8
	reconnect.Will = mqttLifecycleCommandWill(reconnect.Session)
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{encode(initial, 1), encode(close, 2), encode(reconnect, 3), encode(close, 4)})
	require.NoError(t, err)
	for i, status := range []metadb.MQTTSessionCASStatus{metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASConflict} {
		var r metadb.MQTTLifecycleResult
		require.NoError(t, json.Unmarshal(results[i], &r))
		require.Equal(t, status, r.Status)
	}
	old, found, err := db.ForHashSlot(7).GetMQTTWill(ctx, initial.Will.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, metadb.MQTTWillCancelled, old.Stage)
	session, found, err := db.ForHashSlot(7).GetMQTTSession(ctx, owner.Namespace, owner.ClientID)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 2, session.OwnerGeneration)
	require.EqualValues(t, 3, session.WillGeneration)
	require.Len(t, session.LastLifecycleDigest, 64)
	fresh, found, err := db.ForHashSlot(7).GetMQTTWill(ctx, reconnect.Will.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, *reconnect.Will, fresh)
	for _, ids := range [][2]uint16{{12, 7}, {11, 8}} {
		bad := encode(reconnect, 5)
		bad.SlotID = multiraft.SlotID(ids[0])
		bad.HashSlot = ids[1]
		_, err = sm.Apply(ctx, bad)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 4, index)
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{7})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	raw, err := restored.Apply(ctx, encode(reconnect, 5))
	require.NoError(t, err)
	var retry metadb.MQTTLifecycleResult
	require.NoError(t, json.Unmarshal(raw, &retry))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, retry.Status)
	require.EqualValues(t, 3, retry.WillGeneration)
	restoredSession, found, err := target.ForHashSlot(7).GetMQTTSession(ctx, owner.Namespace, owner.ClientID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, session, restoredSession)
	inspection, err := DecodeCommandInspection(encode(initial, 1).Data)
	require.NoError(t, err)
	require.Equal(t, "mqtt_lifecycle", inspection.Type)
	view, err := json.Marshal(inspection)
	require.NoError(t, err)
	require.NotContains(t, string(view), "inspection-secret-body")
}

func TestMQTTLifecycleCommandBoundedStrictInput(t *testing.T) {
	m := mqttLifecycleCommandFixture()
	valid, err := EncodeMQTTLifecycleCommand(m)
	require.NoError(t, err)
	for _, change := range []func(*metadb.MQTTLifecycleMutation){
		func(m *metadb.MQTTLifecycleMutation) { m.ExpectedRevision = ^uint64(0) }, func(m *metadb.MQTTLifecycleMutation) { m.Session.WillGeneration = 1 },
		func(m *metadb.MQTTLifecycleMutation) { m.Session.LastLifecycleDigest = "forged" }, func(m *metadb.MQTTLifecycleMutation) { m.Event = 99 }, func(m *metadb.MQTTLifecycleMutation) { m.OwnerNodeID = 8 },
	} {
		bad := m
		change(&bad)
		_, err = EncodeMQTTLifecycleCommand(bad)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	m.Will.Payload = bytes.Repeat([]byte{0xff}, 65535)
	m.Will.PublicationMetadata = bytes.Repeat([]byte{1}, 32<<10)
	largest, err := EncodeMQTTLifecycleCommand(m)
	require.NoError(t, err)
	require.LessOrEqual(t, len(largest), 256<<10)
	_, err = DecodeCommandInspection(largest)
	require.NoError(t, err)
	for _, raw := range [][]byte{{1, 73}, append([]byte{1, 73}, bytes.Repeat([]byte(" "), (256<<10)+1)...),
		append([]byte{1, 73}, []byte(`{"version":2}`)...), append([]byte{1, 73}, []byte(`{"version":1}`)...),
		append(bytes.Clone(valid), []byte(` {}`)...), append([]byte{1, 73}, append([]byte(`{"unknown":1,`), valid[3:]...)...),
		bytes.Replace(valid, []byte(`"client_id":"phone"`), []byte(`"client_id":"phone","future":1`), 1),
	} {
		_, err := DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
