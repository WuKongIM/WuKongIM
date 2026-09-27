package meta

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTWillPreparationPhasesAndFrozenPayload(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	w := readyMQTTWill(t, s.db)
	w.Stage, w.Revision, w.ExecutionGeneration = MQTTWillExecuting, w.Revision+1, 1
	w.ExecutorNodeID, w.ExecutorBootID, w.LeaseUntilMS, w.UpdatedAtMS = 10, "a", 9000, 7001
	w.DispatchStage = MQTTWillDispatchPreparing
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, w.Revision-1, w).Status)
	for _, phase := range []MQTTWillDispatchStage{MQTTWillDispatchLegacy, MQTTWillDispatchStarted} {
		bad := w
		bad.Revision++
		bad.DispatchStage = phase
		require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, w.Revision, bad).Status)
	}
	w.Revision++
	w.DispatchStage, w.DispatchPayload = MQTTWillDispatchPrepared, []byte("after hooks")
	b := s.db.NewBatch()
	r, err := b.CompareAndSwapMQTTWill(7, w.Revision-1, w)
	require.NoError(t, err)
	w.DispatchPayload[0] = 'X'
	require.NoError(t, b.Commit(context.Background()))
	require.Equal(t, MQTTSessionCASApplied, r.Status)
	require.NoError(t, b.Close())
	w.DispatchPayload[0] = 'a'
	got, found, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), w.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, w, got)
	for _, mutate := range []func(*MQTTWill){
		func(v *MQTTWill) { v.DispatchPayload = []byte("changed") },
		func(v *MQTTWill) { v.DispatchStage, v.DispatchPayload = MQTTWillDispatchPreparing, nil },
		func(v *MQTTWill) {
			v.ExecutionGeneration++
			v.ExecutorBootID = "b"
			v.UpdatedAtMS = 9000
			v.LeaseUntilMS = 11000
			v.DispatchPayload = []byte("changed")
		},
	} {
		bad := w
		bad.Revision++
		mutate(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, w.Revision, bad).Status)
	}
	w.Revision++
	w.ExecutionGeneration++
	w.ExecutorBootID, w.UpdatedAtMS, w.LeaseUntilMS = "b", 9000, 11000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, w.Revision-1, w).Status)
	w.Revision++
	w.DispatchStage = MQTTWillDispatchStarted
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, w.Revision-1, w).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTWill(t, s.db, w.Revision-1, w).Status)
	bad := w
	bad.Revision++
	bad.Stage, bad.LeaseUntilMS, bad.RejectReason = MQTTWillRejected, 0, MQTTWillPermissionRevoked
	require.Error(t, ValidateMQTTWill(bad))
	w.Revision++
	w.Stage, w.LeaseUntilMS, w.MessageID, w.MessageSeq, w.PublishedAtMS = MQTTWillPublished, 0, 44, 9, 9001
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, w.Revision-1, w).Status)
	reader, err := s.db.OpenHashSlotSnapshot(context.Background(), []uint16{7})
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	other := openTestMetaStore(t)
	defer other.close(t)
	require.NoError(t, other.db.ImportHashSlotSnapshot(context.Background(), SlotSnapshot{HashSlots: []uint16{7}, Data: data}))
	got, found, err = other.db.HashSlot(7).GetMQTTWill(context.Background(), w.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, w, got)
}

func TestMQTTWillPreparationBoundsLegacyAndEmptyPayload(t *testing.T) {
	w := mqttWillFixture()
	w.Stage, w.DisconnectedAtMS, w.DueAtMS, w.UpdatedAtMS = MQTTWillExecuting, 2000, 7000, 7001
	w.ExecutionGeneration, w.ExecutorNodeID, w.ExecutorBootID, w.LeaseUntilMS = 1, 10, "a", 9000
	w.DispatchStage = MQTTWillDispatchPrepared
	key, err := mqttWillTable.primaryRowKey(7, mqttWillPrimaryKey(w.Key))
	require.NoError(t, err)
	for _, payload := range [][]byte{nil, bytes.Repeat([]byte{7}, 65535)} {
		w.DispatchPayload = payload
		w.Payload = bytes.Repeat([]byte{1}, 65535)
		w.PublicationMetadata = bytes.Repeat([]byte{1}, 32<<10)
		encoded, err := encodeMQTTWillRow(key, w)
		require.NoError(t, err)
		decoded, err := decodeMQTTWillRow(key, mqttWillPrimaryKey(w.Key), encoded)
		require.NoError(t, err)
		require.True(t, equalMQTTWill(w, decoded))
		require.Less(t, len(encoded), 192<<10)
		inspection := inspectMQTTWillRow(decoded)
		require.NotContains(t, inspection, "dispatch_payload")
		require.Equal(t, len(payload), inspection["dispatch_payload_bytes"])
	}
	for _, mutate := range []func(*MQTTWill){
		func(v *MQTTWill) { v.DispatchStage = 99 },
		func(v *MQTTWill) { v.DispatchPayload = make([]byte, 65536) },
		func(v *MQTTWill) { v.DispatchStage = MQTTWillDispatchPreparing },
		func(v *MQTTWill) { v.DispatchStage = MQTTWillDispatchLegacy },
		func(v *MQTTWill) { v.Stage, v.LeaseUntilMS = MQTTWillReady, 0 },
	} {
		bad := w
		mutate(&bad)
		require.Error(t, ValidateMQTTWill(bad))
	}
}

func TestMQTTWillPreparationRejectsImpossibleRestoredTerminalPhases(t *testing.T) {
	w := mqttWillFixture()
	w.Stage, w.DisconnectedAtMS, w.DueAtMS, w.UpdatedAtMS = MQTTWillPublished, 2000, 7000, 7001
	w.ExecutionGeneration, w.ExecutorNodeID, w.ExecutorBootID = 1, 10, "a"
	w.MessageID, w.MessageSeq, w.PublishedAtMS = 44, 9, 7001
	w.DispatchStage = MQTTWillDispatchPreparing
	require.Error(t, ValidateMQTTWill(w))
	w.DispatchStage, w.DispatchPayload = MQTTWillDispatchPrepared, []byte("body")
	require.Error(t, ValidateMQTTWill(w))
	w.DispatchStage = MQTTWillDispatchStarted
	require.NoError(t, ValidateMQTTWill(w))
	w.Stage, w.RejectReason, w.MessageID, w.MessageSeq, w.PublishedAtMS = MQTTWillRejected, MQTTWillPermissionRevoked, 0, 0, 0
	require.Error(t, ValidateMQTTWill(w))
}
