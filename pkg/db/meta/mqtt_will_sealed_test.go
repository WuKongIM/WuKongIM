package meta

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 4 is the independent durable contract; the baseline must reject it.
func TestMQTTWillSealedRejectionPreservesExactAttemptAndSnapshot(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	w := readyMQTTWill(t, s.db)
	w.Stage, w.Revision, w.ExecutionGeneration = MQTTWillExecuting, w.Revision+1, 1
	w.ExecutorNodeID, w.ExecutorBootID, w.UpdatedAtMS, w.LeaseUntilMS = 10, "old", 7001, 9000
	w.DispatchStage = MQTTWillDispatchPreparing
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, w.Revision-1, w).Status)
	w.Revision++
	w.DispatchStage, w.DispatchPayload = MQTTWillDispatchPrepared, []byte("frozen transformed body")
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, w.Revision-1, w).Status)
	w.Revision++
	w.DispatchStage = MQTTWillDispatchStarted
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, w.Revision-1, w).Status)
	rejected := w
	rejected.Revision++
	rejected.Stage, rejected.DispatchStage = MQTTWillRejected, MQTTWillDispatchStage(4)
	rejected.UpdatedAtMS, rejected.LeaseUntilMS, rejected.RejectReason = 9000, 0, MQTTWillPermissionRevoked
	require.NoError(t, ValidateMQTTWill(rejected), "sealed rejection has a distinct terminal shape")
	for name, mutate := range map[string]func(*MQTTWill){
		"before old grant expires": func(v *MQTTWill) { v.UpdatedAtMS = 8999 },
		"different execution":      func(v *MQTTWill) { v.ExecutionGeneration++ },
		"different boot":           func(v *MQTTWill) { v.ExecutorBootID = "new" },
		"different node":           func(v *MQTTWill) { v.ExecutorNodeID++ },
		"changed frozen body":      func(v *MQTTWill) { v.DispatchPayload = []byte("changed") },
		"changed configured body":  func(v *MQTTWill) { v.Payload = []byte("changed") },
		"changed decision":         func(v *MQTTWill) { v.DecisionRevision++ },
	} {
		t.Run(name, func(t *testing.T) {
			bad := rejected
			mutate(&bad)
			require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, w.Revision, bad).Status)
		})
	}
	for _, phase := range []MQTTWillDispatchStage{MQTTWillDispatchLegacy, MQTTWillDispatchPreparing, MQTTWillDispatchPrepared} {
		// These are valid historical shapes but have no captured Started attempt.
		history := openTestMetaStore(t)
		historical := readyMQTTWill(t, history.db)
		historical.Stage, historical.Revision, historical.ExecutionGeneration = MQTTWillExecuting, historical.Revision+1, 1
		historical.ExecutorNodeID, historical.ExecutorBootID, historical.UpdatedAtMS, historical.LeaseUntilMS = 10, "old", 7001, 9000
		historical.DispatchStage = phase
		if phase == MQTTWillDispatchPrepared {
			historical.DispatchStage = MQTTWillDispatchPreparing
		}
		require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, history.db, historical.Revision-1, historical).Status)
		if phase == MQTTWillDispatchPrepared {
			historical.Revision++
			historical.DispatchStage, historical.DispatchPayload = phase, []byte("frozen transformed body")
			require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, history.db, historical.Revision-1, historical).Status)
		}
		bad := historical
		bad.Revision++
		bad.Stage, bad.DispatchStage, bad.UpdatedAtMS, bad.LeaseUntilMS, bad.RejectReason = MQTTWillRejected, MQTTWillDispatchStage(4), 9000, 0, MQTTWillPermissionRevoked
		require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, history.db, historical.Revision, bad).Status)
		history.close(t)
	}
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, w.Revision, rejected).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTWill(t, s.db, w.Revision, rejected).Status)
	reader, err := s.db.OpenHashSlotSnapshot(context.Background(), []uint16{7})
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	other := openTestMetaStore(t)
	defer other.close(t)
	require.NoError(t, other.db.ImportHashSlotSnapshot(context.Background(), SlotSnapshot{HashSlots: []uint16{7}, Data: data}))
	got, found, err := other.db.HashSlot(7).GetMQTTWill(context.Background(), rejected.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, rejected, got)
	rows, _, _, err := other.db.HashSlot(7).ListMQTTWillRecovery(context.Background(), MQTTWillRecoveryCursor{}, 16)
	require.NoError(t, err)
	require.Empty(t, rows, "terminal rejection must leave recovery discovery")
	for _, mutate := range []func(*MQTTWill){
		func(v *MQTTWill) { v.Stage, v.RejectReason, v.LeaseUntilMS = MQTTWillExecuting, 0, 11000 },
		func(v *MQTTWill) { v.RejectReason = MQTTWillTargetDeleted },
		func(v *MQTTWill) {
			v.Stage, v.RejectReason, v.MessageID, v.MessageSeq, v.PublishedAtMS = MQTTWillPublished, 0, 44, 9, 9000
		},
	} {
		bad := rejected
		mutate(&bad)
		require.Error(t, ValidateMQTTWill(bad), "sealed is never an executable phase or a positive receipt")
	}
}
