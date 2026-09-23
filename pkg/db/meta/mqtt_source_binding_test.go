package meta

import (
	"bytes"
	"context"
	"errors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func mqttSourceBindingFixture() MQTTSourceBinding {
	return MQTTSourceBinding{Key: MQTTSourceBindingKey{Owner: MQTTBindingOwner{Kind: MQTTBindingChannel, ID: "group:g", Generation: "source-1"},
		Namespace: "main", ClientID: "client", SessionGeneration: 1, SubscriptionGeneration: 2}, UID: "alice", Topic: mqttSubscriptionFixture().Topic,
		Revision: 1, IntentRevision: 2, AuthorizationVersion: 9, OperationID: "subscribe-1", Stage: MQTTBindingPreparing, RecoveryAtMS: 1000, UpdatedAtMS: 1000}
}

func writeMQTTSourceBinding(t *testing.T, db *MetaDB, expected uint64, row MQTTSourceBinding) MQTTSourceBindingResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	result, err := b.CompareAndSwapMQTTSourceBinding(9, expected, row)
	require.NoError(t, err)
	require.Equal(t, MQTTSourceBindingResult{}, *result)
	require.NoError(t, b.Commit(context.Background()))
	return *result
}

func TestMQTTSourceBindingFencesDelayedLifecycleAndProgress(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	row := mqttSourceBindingFixture()
	// This is a source-owned Slot: no Session row is present on it.
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, row).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTSourceBinding(t, s.db, 0, row).Status)
	changed := row
	changed.RecoveryAtMS++
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 0, changed).Status)
	row.Revision, row.BoundaryKnown, row.StartAfter, row.CompletedThrough, row.ProtectionRevision = 2, true, 100, 100, 1
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, row).Status)
	row.Revision, row.Stage, row.ProgressRevision = 3, MQTTBindingActive, 4
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 2, row).Status)
	row.Revision, row.CompletedThrough, row.ProgressRevision = 4, 105, 8
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 3, row).Status)
	for _, change := range []func(*MQTTSourceBinding){
		func(r *MQTTSourceBinding) { r.IntentRevision = 1 },
		func(r *MQTTSourceBinding) { r.ProgressRevision = 7 },
		func(r *MQTTSourceBinding) { r.UID = "bob" },
		func(r *MQTTSourceBinding) { r.Topic = "other" },
		func(r *MQTTSourceBinding) { r.OperationID = "other" },
		func(r *MQTTSourceBinding) { r.AuthorizationVersion++ },
		func(r *MQTTSourceBinding) { r.StartAfter = 101 },
		func(r *MQTTSourceBinding) { r.CompletedThrough = 104 },
		func(r *MQTTSourceBinding) { r.CompletedThrough = 106 },
		func(r *MQTTSourceBinding) { r.Stage = MQTTBindingPreparing },
	} {
		bad := row
		bad.Revision = 5
		change(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 4, bad).Status)
	}
	row.Revision, row.IntentRevision, row.Stage, row.EndKnown, row.EndThrough = 5, 10, MQTTBindingRemoving, true, 110
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 4, row).Status)
	late := row
	late.Revision = 6
	late.Stage = MQTTBindingActive
	late.EndKnown = false
	late.EndThrough = 0
	late.IntentRevision = 9
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 5, late).Status)
	row.Revision, row.ProgressRevision, row.CompletedThrough = 6, 12, 110
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 5, row).Status)
	row.Revision, row.Stage, row.ReleaseReason, row.RecoveryAtMS, row.ProtectionRevision = 7, MQTTBindingRemoved, MQTTBindingDrained, 0, 6
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 6, row).Status)
	resurrection := mqttSourceBindingFixture()
	resurrection.Revision = 8
	resurrection.IntentRevision = 100
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 7, resurrection).Status)
	got, found, err := s.db.HashSlot(9).GetMQTTSourceBinding(context.Background(), row.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, row, got)
}

func TestMQTTSourceBindingRemoveBeforePrepareCannotResurrect(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	row := mqttSourceBindingFixture()
	row.Stage, row.IntentRevision, row.ProgressRevision, row.ReleaseReason, row.RecoveryAtMS = MQTTBindingRemoved, 4, 5, MQTTBindingSessionEnded, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, row).Status)
	late := mqttSourceBindingFixture()
	late.Revision = 2
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 1, late).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTSourceBinding(t, s.db, 0, row).Status)
	// A fresh subscription lifetime gets a distinct key and may prepare normally.
	fresh := mqttSourceBindingFixture()
	fresh.Key.SubscriptionGeneration, fresh.IntentRevision = 6, 6
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, fresh).Status)
}

// The deterministic storage boundary must reject missing acknowledgments, while
// the use case remains responsible for obtaining actual remote authority proofs.
func TestMQTTSourceBindingBoundaryCleanupAndUIDDiscovery(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	r := mqttSourceBindingFixture()
	writeMQTTSourceBinding(t, s.db, 0, r)
	b := s.db.NewBatch()
	defer b.Close()
	bad := r
	bad.Revision = 2
	bad.Stage = MQTTBindingActive
	_, err := b.CompareAndSwapMQTTSourceBinding(9, 1, bad)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	r.Revision, r.BoundaryKnown, r.StartAfter, r.CompletedThrough, r.ProtectionRevision = 2, true, 10, 10, 1
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, r).Status)
	r.Revision, r.Stage, r.IntentRevision, r.EndKnown, r.EndThrough = 3, MQTTBindingRemoving, 5, true, 12
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 2, r).Status)
	bad = r
	bad.Revision = 4
	bad.Stage = MQTTBindingRemoved
	bad.ReleaseReason = MQTTBindingDrained
	bad.RecoveryAtMS = 0
	_, err = b.CompareAndSwapMQTTSourceBinding(9, 3, bad)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	// Ending the Session discharges pending work, but still needs source cleanup.
	r.Revision, r.ProgressRevision, r.ReleaseReason = 4, 7, MQTTBindingSessionEnded
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 3, r).Status)
	bad = r
	bad.Revision = 5
	bad.Stage = MQTTBindingRemoved
	bad.RecoveryAtMS = 0
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 4, bad).Status)
	bad.ProtectionRevision = 4
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 4, bad).Status)
	u := mqttSourceBindingFixture()
	u.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, u).Status)
	u.Revision, u.DiscoveryAfterChannelID, u.DiscoveryAfterChannelType = 2, "z", 1
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, u).Status)
	u.Revision, u.DiscoveryAfterChannelID = 3, "aa" // Encoded length precedes bytes.
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 2, u).Status)
	regressed := u
	regressed.Revision = 4
	regressed.DiscoveryAfterChannelID = "z"
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 3, regressed).Status)
	u.Revision, u.DiscoveryDone, u.Stage = 4, true, MQTTBindingActive
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 3, u).Status)
	regressed = u
	regressed.Revision = 5
	regressed.DiscoveryAfterChannelType = 2
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 4, regressed).Status)
	for _, change := range []func(*MQTTSourceBinding){
		func(r *MQTTSourceBinding) { r.Key.Owner.Generation = "invalid" },
		func(r *MQTTSourceBinding) { r.UID = "bob" },
		func(r *MQTTSourceBinding) { r.BoundaryKnown = true },
		func(r *MQTTSourceBinding) { r.ProtectionRevision = 1 },
		func(r *MQTTSourceBinding) { r.DiscoveryDone = false },
	} {
		bad := u
		change(&bad)
		require.ErrorIs(t, ValidateMQTTSourceBinding(bad), dberrors.ErrInvalidArgument)
	}
}

func TestMQTTSourceBindingAtomicBatchAndRollback(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	r := mqttSourceBindingFixture()
	b := s.db.NewBatch()
	defer b.Close()
	first, err := b.CompareAndSwapMQTTSourceBinding(9, 0, r)
	require.NoError(t, err)
	r.Revision, r.RecoveryAtMS = 2, 2000
	second, err := b.CompareAndSwapMQTTSourceBinding(9, 1, r)
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background()))
	require.Equal(t, MQTTSessionCASApplied, first.Status)
	require.Equal(t, MQTTSessionCASApplied, second.Status)
	rollback := s.db.NewBatch()
	defer rollback.Close()
	r.Revision = 3
	_, err = rollback.CompareAndSwapMQTTSourceBinding(9, 2, r)
	require.NoError(t, err)
	sentinel := errors.New("neighbor rejected")
	rollback.addOp(9, func(context.Context, *batchCommitState, *engine.Batch) error { return sentinel })
	require.ErrorIs(t, rollback.Commit(context.Background()), sentinel)
	got, _, err := s.db.HashSlot(9).GetMQTTSourceBinding(context.Background(), r.Key)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Revision)
}

func TestMQTTSourceBindingDurableCodec(t *testing.T) {
	r := mqttSourceBindingFixture()
	pk := mqttSourceBindingPrimaryKey(r.Key)
	key, err := mqttSourceBindingTable.primaryRowKey(9, pk)
	require.NoError(t, err)
	value, err := mqttSourceBindingTable.encodeValue(key, r)
	require.NoError(t, err)
	got, err := mqttSourceBindingTable.decodeValue(key, pk, value)
	require.NoError(t, err)
	require.Equal(t, r, got)
	for i := 0; i < len(value); i++ {
		_, err = mqttSourceBindingTable.decodeValue(key, pk, value[:i])
		require.Error(t, err)
	}
	other := bytes.Clone(key)
	other[2] ^= 1
	_, err = mqttSourceBindingTable.decodeValue(other, pk, value)
	require.ErrorIs(t, err, dberrors.ErrChecksumMismatch)
	env, err := rowcodec.Unwrap(key, value)
	require.NoError(t, err)
	for _, bad := range [][]byte{
		rowcodec.Wrap(key, 2, env.Codec, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, rowcodec.CodecRaw, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, 0, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, env.Flags, nil),
		bytes.Repeat([]byte{0}, (16<<10)+1),
	} {
		_, err = mqttSourceBindingTable.decodeValue(key, pk, bad)
		require.Error(t, err)
	}
	// Optional column 29 follows required 27; virtual retention column 28 is reserved.
	future := append(bytes.Clone(env.Payload), 0x26, 7)
	got, err = mqttSourceBindingTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, future))
	require.NoError(t, err)
	require.Equal(t, r, got)
	for _, change := range []func(*MQTTSourceBinding){
		func(r *MQTTSourceBinding) { r.Key.Owner.Kind = 3 },
		func(r *MQTTSourceBinding) { r.Key.Owner.Generation = "" },
		func(r *MQTTSourceBinding) { r.Key.SubscriptionGeneration = 0 },
		func(r *MQTTSourceBinding) { r.Key.ClientID = strings.Repeat("x", 1025) },
		func(r *MQTTSourceBinding) { r.IntentRevision = 0 },
		func(r *MQTTSourceBinding) { r.Revision = 0 },
		func(r *MQTTSourceBinding) { r.ProtectionRevision = 2 },
		func(r *MQTTSourceBinding) { r.CompletedThrough = 1 },
		func(r *MQTTSourceBinding) { r.RecoveryAtMS = 0 },
		func(r *MQTTSourceBinding) { r.DiscoveryDone = true },
		func(r *MQTTSourceBinding) { r.ReleaseReason = MQTTBindingDrained },
	} {
		bad := r
		change(&bad)
		require.ErrorIs(t, ValidateMQTTSourceBinding(bad), dberrors.ErrInvalidArgument)
	}
}

func TestMQTTSourceBindingBoundedIndexesPinnedSnapshotAndInspection(t *testing.T) {
	ctx := context.Background()
	s := openTestMetaStore(t)
	defer s.close(t)
	owner := mqttSourceBindingFixture().Key.Owner
	var expected []MQTTSourceBinding
	for i, id := range []string{"a", "b", "aa"} {
		r := mqttSourceBindingFixture()
		r.Key.ClientID = id
		writeMQTTSourceBinding(t, s.db, 0, r)
		if i > 0 {
			r.Revision, r.BoundaryKnown, r.StartAfter, r.CompletedThrough, r.ProtectionRevision = 2, true, uint64(30-i*10), uint64(30-i*10), 1
			writeMQTTSourceBinding(t, s.db, 1, r)
			r.Revision, r.Stage, r.ProgressRevision = 3, MQTTBindingActive, 3
			writeMQTTSourceBinding(t, s.db, 2, r)
		}
		expected = append(expected, r)
	}
	u := mqttSourceBindingFixture()
	u.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}
	writeMQTTSourceBinding(t, s.db, 0, u)
	shard := s.db.HashSlot(9)
	var key MQTTSourceBindingKey
	var candidates []MQTTSourceBinding
	for i := 0; i < 4; i++ {
		page, next, done, err := shard.ListMQTTSourceBindingCandidates(ctx, owner, key, 1)
		require.NoError(t, err)
		candidates = append(candidates, page...)
		if done {
			break
		}
		require.NotEqual(t, key, next)
		key = next
	}
	require.Equal(t, expected, candidates)
	var floor MQTTSourceBindingRetentionCursor
	var retained []MQTTSourceBinding
	for i := 0; i < 4; i++ {
		page, next, done, err := shard.ListMQTTSourceBindingRetention(ctx, owner, floor, 1)
		require.NoError(t, err)
		retained = append(retained, page...)
		if done {
			break
		}
		require.NotEqual(t, floor, next)
		floor = next
	}
	require.Equal(t, []MQTTSourceBinding{expected[0], expected[2], expected[1]}, retained, "an unknown protected boundary blocks reclamation")
	var cursor MQTTSourceBindingRecoveryCursor
	var recoverable []MQTTSourceBinding
	for i := 0; i < 5; i++ {
		page, next, done, err := shard.ListMQTTSourceBindingRecovery(ctx, cursor, 1)
		require.NoError(t, err)
		recoverable = append(recoverable, page...)
		if done {
			break
		}
		require.NotEqual(t, cursor, next)
		cursor = next
	}
	require.Equal(t, append(append([]MQTTSourceBinding{}, expected...), u), recoverable)
	page, _, done, err := shard.ListMQTTSourceBindingCandidates(ctx, u.Key.Owner, MQTTSourceBindingKey{}, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []MQTTSourceBinding{u}, page)
	_, _, _, err = shard.ListMQTTSourceBindingRetention(ctx, u.Key.Owner, MQTTSourceBindingRetentionCursor{}, 10)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	for _, limit := range []int{-1, 0, 257} {
		_, _, _, err = shard.ListMQTTSourceBindingCandidates(ctx, owner, MQTTSourceBindingKey{}, limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
		_, _, _, err = shard.ListMQTTSourceBindingRecovery(ctx, MQTTSourceBindingRecoveryCursor{}, limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
		_, _, _, err = shard.ListMQTTSourceBindingRetention(ctx, owner, MQTTSourceBindingRetentionCursor{}, limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	_, _, _, err = shard.ListMQTTSourceBindingCandidates(ctx, owner, u.Key, 10)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	_, _, _, err = shard.ListMQTTSourceBindingRecovery(ctx, MQTTSourceBindingRecoveryCursor{RecoveryAtMS: 1}, 10)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	reader, err := s.db.OpenBackupHashSlotSnapshot(ctx, []uint16{9})
	require.NoError(t, err)
	defer reader.Close()
	removed := expected[0]
	removed.Revision, removed.Stage, removed.ProgressRevision, removed.ReleaseReason = 2, MQTTBindingRemoving, 10, MQTTBindingSessionEnded
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, removed).Status)
	// Removing no longer creates new recipients, but still protects retention.
	page, _, _, err = shard.ListMQTTSourceBindingCandidates(ctx, owner, MQTTSourceBindingKey{}, 10)
	require.NoError(t, err)
	require.Equal(t, expected[1:], page)
	page, _, _, err = shard.ListMQTTSourceBindingRetention(ctx, owner, MQTTSourceBindingRetentionCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, removed, page[0])
	removed.Revision, removed.Stage, removed.ProtectionRevision, removed.RecoveryAtMS = 3, MQTTBindingRemoved, 2, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 2, removed).Status)
	page, _, _, err = shard.ListMQTTSourceBindingRetention(ctx, owner, MQTTSourceBindingRetentionCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, []MQTTSourceBinding{expected[2], expected[1]}, page)
	page, _, _, err = shard.ListMQTTSourceBindingRecovery(ctx, MQTTSourceBindingRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, []MQTTSourceBinding{expected[1], expected[2], u}, page)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	_, err = VerifyBackupHashSlotSnapshotReader(ctx, []uint16{9}, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{9}, bytes.NewReader(payload), int64(len(payload)), false))
	page, _, _, err = target.db.HashSlot(9).ListMQTTSourceBindingRecovery(ctx, MQTTSourceBindingRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, recoverable, page)
	inspection, err := InspectScan(ctx, target.db, InspectScanRequest{Table: "mqtt_source_binding", HashSlot: 9, HashSlotSet: true, Limit: 10})
	require.NoError(t, err)
	require.Len(t, inspection.Rows, 4)
	require.Equal(t, "a", inspection.Rows[0]["client_id"])
	require.EqualValues(t, 0, inspection.Rows[0]["retention_floor"])
	_, found, err := target.db.HashSlot(8).GetMQTTSourceBinding(ctx, u.Key)
	require.NoError(t, err)
	require.False(t, found)
}
