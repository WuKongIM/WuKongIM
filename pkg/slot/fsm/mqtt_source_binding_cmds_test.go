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

func mqttSourceBindingCommandFixture() metadb.MQTTSourceBinding {
	return metadb.MQTTSourceBinding{Key: metadb.MQTTSourceBindingKey{Owner: metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingChannel, ID: "group:g", Generation: "source-1"}, Namespace: "main", ClientID: "client", SessionGeneration: 1, SubscriptionGeneration: 2}, UID: "alice", Topic: "wk/v1/groups/Zw/messages", Revision: 1, IntentRevision: 2, OperationID: "subscribe-1", Stage: metadb.MQTTBindingPreparing, RecoveryAtMS: 1000, UpdatedAtMS: 1000}
}

func TestMQTTSourceBindingCommandBatchSnapshotReplayAndOwnership(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{9})
	require.NoError(t, err)
	encode := func(expected uint64, row metadb.MQTTSourceBinding, index uint64) multiraft.Command {
		raw, err := EncodeMQTTSourceBindingCommand(expected, row)
		require.NoError(t, err)
		require.Equal(t, []byte{1, 71}, raw[:2])
		return multiraft.Command{SlotID: 11, HashSlot: 9, Index: index, Term: 1, Data: raw}
	}
	first := mqttSourceBindingCommandFixture()
	protected := first
	protected.Revision, protected.BoundaryKnown, protected.StartAfter, protected.CompletedThrough, protected.ProtectionRevision = 2, true, 100, 100, 1
	active := protected
	active.Revision, active.Stage, active.ProgressRevision = 3, metadb.MQTTBindingActive, 4
	uid := first
	uid.Key.Owner = metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingUID, ID: "alice"}
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{encode(0, first, 1), encode(1, protected, 2), encode(2, active, 3), encode(0, first, 4), encode(0, uid, 5)})
	require.NoError(t, err)
	for i, status := range []metadb.MQTTSessionCASStatus{metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASConflict, metadb.MQTTSessionCASApplied} {
		var result metadb.MQTTSourceBindingResult
		require.NoError(t, json.Unmarshal(results[i], &result))
		require.Equal(t, status, result.Status)
	}
	got, found, err := db.ForHashSlot(9).GetMQTTSourceBinding(ctx, first.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, active, got)
	_, found, err = db.ForHashSlot(9).GetMQTTSession(ctx, "main", "client")
	require.NoError(t, err)
	require.False(t, found)
	for _, ids := range [][2]uint16{{12, 9}, {11, 8}} {
		bad := encode(2, active, 6)
		bad.SlotID = multiraft.SlotID(ids[0])
		bad.HashSlot = ids[1]
		_, err = sm.Apply(ctx, bad)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 5, index)
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{9})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	raw, err := restored.Apply(ctx, encode(2, active, 6))
	require.NoError(t, err)
	var result metadb.MQTTSourceBindingResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, result.Status)
	page, _, _, err := target.ForHashSlot(9).ListMQTTSourceBindingCandidates(ctx, first.Key.Owner, metadb.MQTTSourceBindingKey{}, 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.MQTTSourceBinding{active}, page)
	page, _, _, err = target.ForHashSlot(9).ListMQTTSourceBindingRecovery(ctx, metadb.MQTTSourceBindingRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.MQTTSourceBinding{active, uid}, page)
	page, _, _, err = target.ForHashSlot(9).ListMQTTSourceBindingRetention(ctx, first.Key.Owner, metadb.MQTTSourceBindingRetentionCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.MQTTSourceBinding{active}, page)
	inspection, err := DecodeCommandInspection(encode(0, first, 1).Data)
	require.NoError(t, err)
	require.Equal(t, "mqtt_source_binding", inspection.Type)
	require.Equal(t, "group:g", inspection.Payload["owner_id"])
	require.Equal(t, "client", inspection.Payload["client_id"])
}

func TestMQTTSourceBindingCommandRejectsMalformedAndUnboundedInput(t *testing.T) {
	row := mqttSourceBindingCommandFixture()
	valid, err := EncodeMQTTSourceBindingCommand(0, row)
	require.NoError(t, err)
	_, err = EncodeMQTTSourceBindingCommand(^uint64(0), row)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	row.Key.Owner.Generation = ""
	_, err = EncodeMQTTSourceBindingCommand(0, row)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	for _, raw := range [][]byte{
		{1, 71}, append([]byte{1, 71}, bytes.Repeat([]byte(" "), 33<<10)...),
		append([]byte{1, 71}, []byte(`{"version":2}`)...),
		append([]byte{1, 71}, []byte(`{"version":1}`)...),
		append(bytes.Clone(valid), []byte(` {}`)...),
		append([]byte{1, 71}, append([]byte(`{"unknown":1,`), valid[3:]...)...),
		bytes.Replace(valid, []byte(`"client_id":"client"`), []byte(`"client_id":"client","future":1`), 1),
	} {
		_, err := DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
