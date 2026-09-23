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

func mqttWillCommandFixture() metadb.MQTTWill {
	k := metadb.MQTTWillKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, WillGeneration: 2}
	id, _ := metadb.MQTTWillIdempotencyKey(k)
	return metadb.MQTTWill{Key: k, UID: "alice", OwnerGeneration: 1, OwnerNodeID: 3, OwnerBootID: "boot-3", ConnectionID: 42, Revision: 1, DecisionRevision: 2,
		Topic: "wk/v1/groups/Zw/messages", TargetID: "g", TargetType: 2, Payload: []byte("inspection-secret-body"), PublicationMetadata: []byte{1, 2},
		DelaySeconds: 5, QoS: 1, ClientMsgNo: "client-will", IdempotencyKey: id, Stage: metadb.MQTTWillArmed, UpdatedAtMS: 1000}
}

func TestMQTTWillCommandBatchReceiptSnapshotAndOwnership(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	require.NoError(t, err)
	encode := func(expected uint64, row metadb.MQTTWill, index uint64) multiraft.Command {
		raw, err := EncodeMQTTWillCommand(expected, row)
		require.NoError(t, err)
		require.Equal(t, []byte{1, 72}, raw[:2])
		return multiraft.Command{SlotID: 11, HashSlot: 7, Index: index, Term: 1, Data: raw}
	}
	armed := mqttWillCommandFixture()
	waiting := armed
	waiting.Revision, waiting.DecisionRevision, waiting.Stage, waiting.DisconnectedAtMS, waiting.DueAtMS, waiting.UpdatedAtMS = 2, 3, metadb.MQTTWillWaiting, 2000, 7000, 2000
	ready := waiting
	ready.Revision, ready.DecisionRevision, ready.Stage, ready.UpdatedAtMS = 3, 4, metadb.MQTTWillReady, 7000
	exec := ready
	exec.Revision, exec.Stage, exec.ExecutionGeneration, exec.ExecutorNodeID, exec.ExecutorBootID, exec.LeaseUntilMS, exec.UpdatedAtMS = 4, metadb.MQTTWillExecuting, 1, 10, "exec-a", 9000, 7001
	published := exec
	published.Revision, published.Stage, published.MessageID, published.MessageSeq, published.PublishedAtMS, published.LeaseUntilMS, published.UpdatedAtMS = 5, metadb.MQTTWillPublished, 55, 6, 7100, 0, 7101
	commands := []multiraft.Command{encode(0, armed, 1), encode(1, waiting, 2), encode(2, ready, 3), encode(3, exec, 4), encode(4, published, 5), encode(0, armed, 6)}
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, commands)
	require.NoError(t, err)
	for i, result := range results {
		var r metadb.MQTTWillResult
		require.NoError(t, json.Unmarshal(result, &r))
		want := metadb.MQTTSessionCASApplied
		if i == 5 {
			want = metadb.MQTTSessionCASConflict
		}
		require.Equal(t, want, r.Status)
	}
	got, found, err := db.ForHashSlot(7).GetMQTTWill(ctx, armed.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, published, got)
	page, _, _, err := db.ForHashSlot(7).ListMQTTWillRecovery(ctx, metadb.MQTTWillRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Empty(t, page)
	for _, ids := range [][2]uint16{{12, 7}, {11, 8}} {
		bad := encode(4, published, 7)
		bad.SlotID = multiraft.SlotID(ids[0])
		bad.HashSlot = ids[1]
		_, err = sm.Apply(ctx, bad)
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
	raw, err := restored.Apply(ctx, encode(4, published, 7))
	require.NoError(t, err)
	var result metadb.MQTTWillResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, result.Status)
	got, found, err = target.ForHashSlot(7).GetMQTTWill(ctx, armed.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, published, got)
	inspection, err := DecodeCommandInspection(commands[0].Data)
	require.NoError(t, err)
	require.Equal(t, "mqtt_will", inspection.Type)
	require.Equal(t, "client", inspection.Payload["client_id"])
	require.NotContains(t, inspection.Payload, "payload")
	require.NotContains(t, inspection.Payload, "publication_metadata")
	view, err := json.Marshal(inspection)
	require.NoError(t, err)
	require.NotContains(t, string(view), "inspection-secret-body")
}

func TestMQTTWillCommandBoundsAndStrictDecoding(t *testing.T) {
	row := mqttWillCommandFixture()
	valid, err := EncodeMQTTWillCommand(0, row)
	require.NoError(t, err)
	_, err = EncodeMQTTWillCommand(^uint64(0), row)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	row.Payload = bytes.Repeat([]byte{0xff}, 65535)
	row.PublicationMetadata = bytes.Repeat([]byte{1}, 32<<10)
	largest, err := EncodeMQTTWillCommand(0, row)
	require.NoError(t, err)
	require.LessOrEqual(t, len(largest), 256<<10)
	_, err = DecodeCommandInspection(largest)
	require.NoError(t, err)
	row.Payload = append(row.Payload, 1)
	_, err = EncodeMQTTWillCommand(0, row)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	for _, raw := range [][]byte{{1, 72}, append([]byte{1, 72}, bytes.Repeat([]byte(" "), (256<<10)+1)...),
		append([]byte{1, 72}, []byte(`{"version":2}`)...), append([]byte{1, 72}, []byte(`{"version":1}`)...),
		append(bytes.Clone(valid), []byte(` {}`)...), append([]byte{1, 72}, append([]byte(`{"unknown":1,`), valid[3:]...)...),
		bytes.Replace(valid, []byte(`"client_id":"client"`), []byte(`"client_id":"client","future":1`), 1),
	} {
		_, err := DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
