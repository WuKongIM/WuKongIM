package fsm

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func mqttInboxAdmissionCommandFixture() metadb.MQTTInboxAdmission {
	return metadb.MQTTInboxAdmission{ChannelID: channelid.EncodePersonChannel("alice", "bob"), DirectoryGeneration: 1, Revision: 1, UpdatedAtMS: 1000}
}

func TestMQTTInboxAdmissionCommandSnapshotAndAtomicRecreation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{9})
	require.NoError(t, err)
	row := mqttInboxAdmissionCommandFixture()
	encode := func(expected uint64, r metadb.MQTTInboxAdmission) []byte {
		raw, e := EncodeMQTTInboxAdmissionCommand(expected, r)
		require.NoError(t, e)
		require.Equal(t, []byte{1, 74}, raw[:2])
		return raw
	}
	cmd := func(index uint64, raw []byte) multiraft.Command {
		return multiraft.Command{SlotID: 11, HashSlot: 9, Index: index, Term: 1, Data: raw}
	}
	recreate, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked([]CreateChannelRuntimeMetaBatchItem{{HashSlot: 9, Meta: fsmTestRuntimeMeta(row.ChannelID, 1)}})
	require.NoError(t, err)
	phase1 := row
	phase1.Revision, phase1.Participant = 2, 1
	done := row
	done.Revision, done.Participant = 3, 2
	raw, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{cmd(1, recreate), cmd(2, encode(0, row)), cmd(3, encode(1, phase1)), cmd(4, encode(2, done))})
	require.NoError(t, err)
	for _, r := range raw[1:] {
		var result metadb.MQTTInboxAdmissionResult
		require.NoError(t, json.Unmarshal(r, &result))
		require.Equal(t, metadb.MQTTSessionCASApplied, result.Status)
	}
	// A rejected batch must publish neither its invalidation nor its applied index.
	deleteRaw := EncodeDeleteChannelRuntimeMetaCommand(row.ChannelID, 1)
	_, err = sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{cmd(5, deleteRaw), cmd(6, []byte{1, 255})})
	require.Error(t, err)
	view, err := db.ReadMQTTState(ctx, 9, metadb.MQTTRead{Kind: metadb.MQTTReadInboxAdmission, AdmissionChannel: row.ChannelID})
	require.NoError(t, err)
	require.Equal(t, done, *view.Admission.Checkpoint)
	index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 4, index)
	// Delete + create + delayed exact retry share one overlay and must conflict.
	raw, err = sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{cmd(5, deleteRaw), cmd(6, recreate), cmd(7, encode(2, done))})
	require.NoError(t, err)
	var result metadb.MQTTInboxAdmissionResult
	require.NoError(t, json.Unmarshal(raw[2], &result))
	require.Equal(t, metadb.MQTTSessionCASConflict, result.Status)
	require.EqualValues(t, 4, result.CurrentRevision)
	snapshot, err := sm.Snapshot(ctx)
	require.NoError(t, err)
	target := openTestDB(t)
	restored, err := NewStateMachineWithHashSlots(target, 11, []uint16{9})
	require.NoError(t, err)
	require.NoError(t, restored.Restore(ctx, snapshot))
	view, err = target.ReadMQTTState(ctx, 9, metadb.MQTTRead{Kind: metadb.MQTTReadInboxAdmission, AdmissionChannel: row.ChannelID})
	require.NoError(t, err)
	require.EqualValues(t, 0, view.Admission.Checkpoint.DirectoryGeneration)
	require.EqualValues(t, 4, view.Admission.Checkpoint.Revision)
	require.NotNil(t, view.Admission.Runtime)
	require.Greater(t, view.Admission.Runtime.DirectoryGeneration, uint64(1))
	row.Revision = 5
	row.DirectoryGeneration = view.Admission.Runtime.DirectoryGeneration
	applied, err := restored.Apply(ctx, cmd(8, encode(4, row)))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(applied, &result))
	require.Equal(t, metadb.MQTTSessionCASApplied, result.Status)
	repeated, err := restored.Apply(ctx, cmd(9, encode(4, row)))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(repeated, &result))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, result.Status)
	for _, ids := range [][2]uint16{{12, 9}, {11, 8}} {
		bad := cmd(10, encode(4, row))
		bad.SlotID = multiraft.SlotID(ids[0])
		bad.HashSlot = ids[1]
		_, err = restored.Apply(ctx, bad)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	inspection, err := DecodeCommandInspection(encode(4, row))
	require.NoError(t, err)
	require.Equal(t, "mqtt_inbox_admission", inspection.Type)
	require.Equal(t, row.ChannelID, inspection.Payload["channel_id"])
}

func TestMQTTInboxAdmissionCommandRejectsMalformedAndUnboundedInput(t *testing.T) {
	row := mqttInboxAdmissionCommandFixture()
	valid, err := EncodeMQTTInboxAdmissionCommand(0, row)
	require.NoError(t, err)
	for _, mutate := range []func(*metadb.MQTTInboxAdmission){
		func(r *metadb.MQTTInboxAdmission) { r.ChannelID = "bob" },
		func(r *metadb.MQTTInboxAdmission) { r.ChannelID = string(bytes.Repeat([]byte("a"), 1025)) + "@b" },
		func(r *metadb.MQTTInboxAdmission) { r.DirectoryGeneration = 0 },
		func(r *metadb.MQTTInboxAdmission) { r.Revision = 0 },
		func(r *metadb.MQTTInboxAdmission) { r.UpdatedAtMS = 0 },
		func(r *metadb.MQTTInboxAdmission) { r.Participant = 3 },
		func(r *metadb.MQTTInboxAdmission) {
			r.After = metadb.MQTTSourceBindingKey{Owner: metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingUID, ID: "foreign"}, Namespace: "main", ClientID: "c", SessionGeneration: 1, SubscriptionGeneration: 1}
		},
	} {
		bad := row
		mutate(&bad)
		_, err = EncodeMQTTInboxAdmissionCommand(0, bad)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
	_, err = EncodeMQTTInboxAdmissionCommand(^uint64(0), row)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	for _, raw := range [][]byte{
		{1, 74}, append([]byte{1, 74}, bytes.Repeat([]byte(" "), 33<<10)...),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":2`), 1),
		append(bytes.Clone(valid), []byte(` {}`)...),
		append([]byte{1, 74}, append([]byte(`{"unknown":1,`), valid[3:]...)...),
	} {
		_, err := DecodeCommandInspection(raw)
		require.Error(t, err)
	}
}
