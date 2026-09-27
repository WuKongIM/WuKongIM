package meta

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/stretchr/testify/require"
)

func mqttWillFixture() MQTTWill {
	k := MQTTWillKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, WillGeneration: 2}
	id, _ := MQTTWillIdempotencyKey(k)
	return MQTTWill{Key: k, UID: "alice", OwnerGeneration: 1, OwnerNodeID: 3, OwnerBootID: "boot-3", ConnectionID: 42, Revision: 1, DecisionRevision: 2,
		Topic: "wk/v1/groups/Zw/messages", TargetID: "g", TargetType: 2, Payload: []byte("gone"), PublicationMetadata: []byte{1, 9, 0},
		DelaySeconds: 5, QoS: 1, ClientMsgNo: "client-will", IdempotencyKey: id, Stage: MQTTWillArmed, UpdatedAtMS: 1000}
}

func writeMQTTWill(t *testing.T, db *MetaDB, expected uint64, r MQTTWill) MQTTWillResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	result, err := b.CompareAndSwapMQTTWill(7, expected, r)
	require.NoError(t, err)
	require.Equal(t, MQTTWillResult{}, *result)
	require.NoError(t, b.Commit(context.Background()))
	return *result
}

func readyMQTTWill(t *testing.T, db *MetaDB) MQTTWill {
	t.Helper()
	r := mqttWillFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, db, 0, r).Status)
	r.Revision, r.DecisionRevision, r.Stage, r.DisconnectedAtMS, r.DueAtMS, r.UpdatedAtMS = 2, 3, MQTTWillWaiting, 2000, 7000, 2000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, db, 1, r).Status)
	r.Revision, r.DecisionRevision, r.Stage, r.UpdatedAtMS = 3, 4, MQTTWillReady, 7000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, db, 2, r).Status)
	return r
}

func TestMQTTWillImmutableLifecycleAndDurableReceipt(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	r := mqttWillFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 0, r).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTWill(t, s.db, 0, r).Status)
	changed := r
	changed.UpdatedAtMS++
	require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, 0, changed).Status)
	for _, change := range []func(*MQTTWill){
		func(r *MQTTWill) { r.UID = "bob" }, func(r *MQTTWill) { r.OwnerGeneration++ }, func(r *MQTTWill) { r.OwnerBootID = "other" },
		func(r *MQTTWill) { r.TargetID = "other" }, func(r *MQTTWill) { r.QoS = 0 }, func(r *MQTTWill) { r.DelaySeconds++ },
		func(r *MQTTWill) { r.Payload = []byte("other") }, func(r *MQTTWill) { r.PublicationMetadata = []byte{1, 8} }, func(r *MQTTWill) { r.ClientMsgNo = "other" },
	} {
		bad := r
		bad.Revision = 2
		change(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, 1, bad).Status)
	}
	r.Revision, r.DecisionRevision, r.Stage, r.DisconnectedAtMS, r.DueAtMS, r.UpdatedAtMS = 2, 3, MQTTWillWaiting, 2000, 7000, 2000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 1, r).Status)
	for _, change := range []func(*MQTTWill){
		func(r *MQTTWill) { r.DecisionRevision = 2 }, func(r *MQTTWill) { r.DueAtMS = 8000 }, func(r *MQTTWill) { r.DisconnectedAtMS = 2100; r.UpdatedAtMS = 2100 },
		func(r *MQTTWill) { r.Stage = MQTTWillReady; r.DueAtMS = 3000; r.UpdatedAtMS = 3000 }, // Earlier publication needs a newer Session-end proof.
	} {
		bad := r
		bad.Revision = 3
		change(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, 2, bad).Status)
	}
	r.Revision, r.DecisionRevision, r.Stage, r.UpdatedAtMS = 3, 4, MQTTWillReady, 7000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 2, r).Status)
	r.Revision, r.Stage, r.ExecutionGeneration, r.ExecutorNodeID, r.ExecutorBootID, r.LeaseUntilMS, r.UpdatedAtMS = 4, MQTTWillExecuting, 1, 10, "exec-a", 9000, 7001
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 3, r).Status)
	r.Revision, r.Stage, r.MessageID, r.MessageSeq, r.PublishedAtMS, r.LeaseUntilMS, r.UpdatedAtMS = 5, MQTTWillPublished, 55, 6, 7500, 0, 7501
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 4, r).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTWill(t, s.db, 4, r).Status)
	// Terminal receipts cannot resurrect or be overwritten with a different result.
	bad := r
	bad.Revision = 6
	bad.MessageID = 66
	require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, 5, bad).Status)
	got, found, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), r.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, r, got)
	fresh := mqttWillFixture()
	fresh.Key.SessionGeneration = 2
	fresh.Key.WillGeneration = 10
	fresh.IdempotencyKey, err = MQTTWillIdempotencyKey(fresh.Key)
	require.NoError(t, err)
	require.NotEqual(t, r.IdempotencyKey, fresh.IdempotencyKey)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 0, fresh).Status)
	got, found, err = s.db.HashSlot(7).GetMQTTWill(context.Background(), r.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, r, got, "a replacement Session cannot erase an old receipt")
}

func TestMQTTWillCancellationAndEarlySessionEnd(t *testing.T) {
	for _, mode := range []string{"normal", "resume", "late-resume", "session-end", "zero-delay"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			r := mqttWillFixture()
			if mode == "zero-delay" {
				r.DelaySeconds = 0
			}
			writeMQTTWill(t, s.db, 0, r)
			if mode == "normal" {
				r.Revision, r.DecisionRevision, r.Stage, r.CancelReason, r.UpdatedAtMS = 2, 3, MQTTWillCancelled, MQTTWillNormalDisconnect, 2000
				require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 1, r).Status)
				return
			}
			if mode == "zero-delay" {
				bad := r
				bad.Revision, bad.DecisionRevision, bad.Stage, bad.CancelReason = 2, 3, MQTTWillCancelled, MQTTWillSessionResumed
				b := s.db.NewBatch()
				defer b.Close()
				_, err := b.CompareAndSwapMQTTWill(7, 1, bad)
				require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
				r.Revision, r.DecisionRevision, r.Stage, r.DisconnectedAtMS, r.DueAtMS, r.UpdatedAtMS = 2, 3, MQTTWillReady, 2000, 2000, 2000
				require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 1, r).Status)
				return
			}
			r.Revision, r.DecisionRevision, r.Stage, r.DisconnectedAtMS, r.DueAtMS, r.UpdatedAtMS = 2, 3, MQTTWillWaiting, 2000, 7000, 2000
			writeMQTTWill(t, s.db, 1, r)
			r.Revision, r.DecisionRevision, r.UpdatedAtMS = 3, 4, 6000
			if mode == "session-end" {
				r.Stage, r.DueAtMS = MQTTWillReady, 6000
				require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 2, r).Status)
				return
			}
			r.Stage, r.CancelReason = MQTTWillCancelled, MQTTWillSessionResumed
			if mode == "late-resume" {
				r.UpdatedAtMS = 7000
				require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, 2, r).Status)
			} else {
				require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 2, r).Status)
			}
		})
	}
}

func TestMQTTWillExecutionLeaseFencesStaleWorkers(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	r := readyMQTTWill(t, s.db)
	r.Revision, r.Stage, r.ExecutionGeneration, r.ExecutorNodeID, r.ExecutorBootID, r.LeaseUntilMS, r.UpdatedAtMS = 4, MQTTWillExecuting, 1, 10, "exec-a", 9000, 7001
	writeMQTTWill(t, s.db, 3, r)
	stolen := r
	stolen.Revision, stolen.ExecutionGeneration, stolen.ExecutorNodeID, stolen.ExecutorBootID, stolen.LeaseUntilMS, stolen.UpdatedAtMS = 5, 2, 11, "exec-b", 10000, 8000
	require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, 4, stolen).Status)
	renewal := r
	renewal.Revision, renewal.LeaseUntilMS, renewal.UpdatedAtMS = 5, 10000, 8000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 4, renewal).Status)
	expired := renewal
	expired.Revision, expired.Stage, expired.LeaseUntilMS, expired.RejectReason, expired.UpdatedAtMS = 6, MQTTWillRejected, 0, MQTTWillPermissionRevoked, 10000
	require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, 5, expired).Status)
	reclaimed := renewal
	reclaimed.Revision, reclaimed.ExecutionGeneration, reclaimed.ExecutorNodeID, reclaimed.ExecutorBootID, reclaimed.LeaseUntilMS, reclaimed.UpdatedAtMS = 6, 2, 11, "exec-b", 12000, 10000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 5, reclaimed).Status)
	stale := expired
	stale.Revision, stale.UpdatedAtMS = 7, 10001
	require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, 6, stale).Status)
	rejected := reclaimed
	rejected.Revision, rejected.Stage, rejected.LeaseUntilMS, rejected.RejectReason, rejected.UpdatedAtMS = 7, MQTTWillRejected, 0, MQTTWillPermissionRevoked, 10001
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 6, rejected).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTWill(t, s.db, 6, rejected).Status)
}

func TestMQTTWillBatchOwnershipAndRollback(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	r := mqttWillFixture()
	want := mqttWillFixture()
	b := s.db.NewBatch()
	defer b.Close()
	first, err := b.CompareAndSwapMQTTWill(7, 0, r)
	require.NoError(t, err)
	r.Payload[0] = 'X'
	r.PublicationMetadata[1] = 88
	next := want
	next.Revision, next.DecisionRevision, next.Stage, next.CancelReason = 2, 3, MQTTWillCancelled, MQTTWillNormalDisconnect
	second, err := b.CompareAndSwapMQTTWill(7, 1, next)
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background()))
	require.Equal(t, MQTTSessionCASApplied, first.Status)
	require.Equal(t, MQTTSessionCASApplied, second.Status)
	got, _, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), r.Key)
	require.NoError(t, err)
	require.Equal(t, next, got)
	got.Payload[0] = 'Z'
	got.PublicationMetadata[1] = 77
	got, _, err = s.db.HashSlot(7).GetMQTTWill(context.Background(), r.Key)
	require.NoError(t, err)
	require.Equal(t, next, got)
	fresh := mqttWillFixture()
	fresh.Key.WillGeneration++
	fresh.IdempotencyKey, err = MQTTWillIdempotencyKey(fresh.Key)
	require.NoError(t, err)
	rb := s.db.NewBatch()
	defer rb.Close()
	_, err = rb.CompareAndSwapMQTTWill(7, 0, fresh)
	require.NoError(t, err)
	sentinel := errors.New("neighbor rejected")
	rb.addOp(7, func(context.Context, *batchCommitState, *engine.Batch) error { return sentinel })
	require.ErrorIs(t, rb.Commit(context.Background()), sentinel)
	_, found, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), fresh.Key)
	require.NoError(t, err)
	require.False(t, found)
}

func TestMQTTWillCodecAndBounds(t *testing.T) {
	r := mqttWillFixture()
	require.Equal(t, "mqtt-will-v1:52f53ab6b15b80cd53a2194bb94c4df86ede4e7e3b0a011c624435248be7f28e", r.IdempotencyKey)
	pk := mqttWillPrimaryKey(r.Key)
	key, err := mqttWillTable.primaryRowKey(7, pk)
	require.NoError(t, err)
	value, err := mqttWillTable.encodeValue(key, r)
	require.NoError(t, err)
	got, err := mqttWillTable.decodeValue(key, pk, value)
	require.NoError(t, err)
	require.Equal(t, r, got)
	for i := 0; i < len(value); i++ {
		_, err = mqttWillTable.decodeValue(key, pk, value[:i])
		require.Error(t, err)
	}
	other := bytes.Clone(key)
	other[2] ^= 1
	_, err = mqttWillTable.decodeValue(other, pk, value)
	require.ErrorIs(t, err, dberrors.ErrChecksumMismatch)
	env, err := rowcodec.Unwrap(key, value)
	require.NoError(t, err)
	for _, bad := range [][]byte{rowcodec.Wrap(key, 2, env.Codec, env.Flags, env.Payload), rowcodec.Wrap(key, 1, rowcodec.CodecRaw, env.Flags, env.Payload), rowcodec.Wrap(key, 1, env.Codec, 0, env.Payload), rowcodec.Wrap(key, 1, env.Codec, env.Flags, nil), bytes.Repeat([]byte{0}, (128<<10)+1)} {
		_, err = mqttWillTable.decodeValue(key, pk, bad)
		require.Error(t, err)
	}
	// Required column 33 is followed by unknown 37; derived column 34 stays reserved.
	future := append(bytes.Clone(env.Payload), 0x46, 7)
	got, err = mqttWillTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, future))
	require.NoError(t, err)
	require.Equal(t, r, got)
	for _, change := range []func(*MQTTWill){
		func(r *MQTTWill) { r.Key.ClientID = strings.Repeat("x", 1025) }, func(r *MQTTWill) { r.Key.WillGeneration = 0 },
		func(r *MQTTWill) { r.OwnerBootID = "" }, func(r *MQTTWill) { r.DecisionRevision = 0 }, func(r *MQTTWill) { r.Revision = 0 },
		func(r *MQTTWill) { r.Payload = bytes.Repeat([]byte{1}, 65536) }, func(r *MQTTWill) { r.PublicationMetadata = bytes.Repeat([]byte{1}, (32<<10)+1) },
		func(r *MQTTWill) { r.PublicationMetadata = []byte{2} }, func(r *MQTTWill) { r.QoS = 2 }, func(r *MQTTWill) { r.TargetType = 99 },
		func(r *MQTTWill) { r.IdempotencyKey = "client-controlled" }, func(r *MQTTWill) { r.DueAtMS = 2000 }, func(r *MQTTWill) { r.LeaseUntilMS = 3000 },
		func(r *MQTTWill) { r.MessageID = 2 }, func(r *MQTTWill) { r.CancelReason = MQTTWillNormalDisconnect },
	} {
		bad := r
		change(&bad)
		require.ErrorIs(t, ValidateMQTTWill(bad), dberrors.ErrInvalidArgument)
	}
	r.Payload = bytes.Repeat([]byte{0xff}, 65535)
	r.PublicationMetadata = bytes.Repeat([]byte{1}, 32<<10)
	value, err = mqttWillTable.encodeValue(key, r)
	require.NoError(t, err)
	require.Less(t, len(value), 128<<10)
	got, err = mqttWillTable.decodeValue(key, pk, value)
	require.NoError(t, err)
	require.Equal(t, r, got)
	b := openTestMetaStore(t)
	defer b.close(t)
	batch := b.db.NewBatch()
	defer batch.Close()
	_, err = batch.CompareAndSwapMQTTWill(7, ^uint64(0), r)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
}

func TestMQTTWillRecoveryPagesPinnedSnapshotAndRedactedInspection(t *testing.T) {
	ctx := context.Background()
	s := openTestMetaStore(t)
	defer s.close(t)
	base := readyMQTTWill(t, s.db)
	second := mqttWillFixture()
	second.Key.WillGeneration = 3
	second.IdempotencyKey, _ = MQTTWillIdempotencyKey(second.Key)
	writeMQTTWill(t, s.db, 0, second)
	second.Revision, second.DecisionRevision, second.Stage, second.DisconnectedAtMS, second.DueAtMS, second.UpdatedAtMS = 2, 3, MQTTWillWaiting, 2000, 7000, 2000
	writeMQTTWill(t, s.db, 1, second)
	another := mqttWillFixture()
	another.Key.Namespace = "next"
	another.IdempotencyKey, _ = MQTTWillIdempotencyKey(another.Key)
	writeMQTTWill(t, s.db, 0, another)
	shard := s.db.HashSlot(7)
	page, after, done, err := shard.ListMQTTWillRecovery(ctx, MQTTWillRecoveryCursor{}, 1)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []MQTTWill{base}, page)
	page, _, done, err = shard.ListMQTTWillRecovery(ctx, after, 1)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []MQTTWill{second}, page)
	for _, n := range []int{-1, 0, 257} {
		_, _, _, err = shard.ListMQTTWillRecovery(ctx, MQTTWillRecoveryCursor{}, n)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	_, _, _, err = shard.ListMQTTWillRecovery(ctx, MQTTWillRecoveryCursor{RecoveryAtMS: 1}, 10)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	reader, err := s.db.OpenBackupHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, err)
	defer reader.Close()
	base.Revision, base.Stage, base.ExecutionGeneration, base.ExecutorNodeID, base.ExecutorBootID, base.LeaseUntilMS, base.UpdatedAtMS = 4, MQTTWillExecuting, 1, 10, "executor", 10000, 7001
	writeMQTTWill(t, s.db, 3, base)
	page, _, _, err = shard.ListMQTTWillRecovery(ctx, MQTTWillRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, []MQTTWill{second, base}, page)
	base.Revision, base.Stage, base.MessageID, base.MessageSeq, base.PublishedAtMS, base.LeaseUntilMS, base.UpdatedAtMS = 5, MQTTWillPublished, 55, 6, 7100, 0, 7101
	writeMQTTWill(t, s.db, 4, base)
	page, _, _, err = shard.ListMQTTWillRecovery(ctx, MQTTWillRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, []MQTTWill{second}, page)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	_, err = VerifyBackupHashSlotSnapshotReader(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)), false))
	page, _, _, err = target.db.HashSlot(7).ListMQTTWillRecovery(ctx, MQTTWillRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.Equal(t, MQTTWillReady, page[0].Stage)
	inspection, err := InspectScan(ctx, target.db, InspectScanRequest{Table: "mqtt_will", HashSlot: 7, HashSlotSet: true, Limit: 10})
	require.NoError(t, err)
	require.Len(t, inspection.Rows, 3)
	for _, row := range inspection.Rows {
		require.NotContains(t, row, "payload")
		require.NotContains(t, row, "publication_metadata")
		require.EqualValues(t, 4, row["payload_bytes"])
	}
}
