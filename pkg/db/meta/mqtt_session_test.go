package meta

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/stretchr/testify/require"
)

func mqttSessionFixture() MQTTSession {
	return MQTTSession{Namespace: "main", ClientID: "client", UID: "alice", Generation: 1, Revision: 1,
		OwnerGeneration: 1, OwnerNodeID: 2, OwnerBootID: "boot-a", ConnectionID: 17, LeaseUntilMS: 5000,
		State: MQTTSessionActive, SessionExpirySec: 86400, DeviceFlag: 1, ReceiveMaximum: 64, MaxPacketBytes: 1 << 20,
		NextPacketID: 1, NextDeliveryOrder: 1, PendingMessages: 0, PendingBytes: 0,
		QuotaMessages: 10000, QuotaBytes: 64 << 20, WillGeneration: 0, UpdatedAtMS: 1000}
}

func writeMQTTSession(t *testing.T, db *MetaDB, row MQTTSession, expected uint64) MQTTSessionCASResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	r, err := b.CompareAndSwapMQTTSession(7, expected, row)
	require.NoError(t, err)
	require.Equal(t, MQTTSessionCASResult{}, *r, "result published before commit")
	require.NoError(t, b.Commit(context.Background()))
	return *r
}

func TestMQTTSessionCASPreservesBindingAndGenerations(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	row := mqttSessionFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, row, 0).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTSession(t, s.db, row, 0).Status)
	changedRetry := row
	changedRetry.QuotaBytes++
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, s.db, changedRetry, 0).Status)
	for _, change := range []func(*MQTTSession){
		func(r *MQTTSession) { r.UID = "mallory" },
		func(r *MQTTSession) { r.OwnerNodeID++ },
		func(r *MQTTSession) { r.OwnerBootID = "restarted" },
		func(r *MQTTSession) { r.ConnectionID++ },
		func(r *MQTTSession) { r.Generation++ },
	} {
		candidate := row
		candidate.Revision++
		change(&candidate)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, s.db, candidate, 1).Status)
	}
	row.OwnerGeneration++
	row.OwnerNodeID++
	row.ConnectionID++
	row.Revision++
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, row, 1).Status)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, s.db, mqttSessionFixture(), 0).Status)
	regressed := row
	regressed.Revision++
	regressed.OwnerGeneration--
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, s.db, regressed, 2).Status)
	row.State, row.LeaseUntilMS, row.TerminationReason = MQTTSessionEnded, 0, MQTTSessionExpired
	row.Revision++
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, row, 2).Status)
	resurrect := row
	resurrect.Revision++
	resurrect.State, resurrect.LeaseUntilMS, resurrect.TerminationReason = MQTTSessionActive, 9000, 0
	resurrect.OwnerGeneration++
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, s.db, resurrect, 3).Status)
	resurrect.Generation++
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, resurrect, 3).Status)
	otherNamespace := mqttSessionFixture()
	otherNamespace.Namespace, otherNamespace.UID = "other", "bob"
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, otherNamespace, 0).Status)
	got, found, err := s.db.HashSlot(7).GetMQTTSession(context.Background(), "main", "client")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, resurrect, got)
}

func TestMQTTSessionBatchOverlayAndAtomicFailure(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	b := s.db.NewBatch()
	defer b.Close()
	row := mqttSessionFixture()
	r1, err := b.CompareAndSwapMQTTSession(7, 0, row)
	require.NoError(t, err)
	row.Revision, row.QuotaBytes = 2, 128<<20
	r2, err := b.CompareAndSwapMQTTSession(7, 1, row)
	require.NoError(t, err)
	require.NoError(t, b.SetSlotAppliedIndex(1, 99))
	require.NoError(t, b.Commit(ctx))
	require.Equal(t, MQTTSessionCASApplied, r1.Status)
	require.Equal(t, MQTTSessionCASApplied, r2.Status)
	got, found, err := s.db.HashSlot(7).GetMQTTSession(ctx, row.Namespace, row.ClientID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, row, got)
	index, err := s.db.SlotAppliedIndex(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 99, index)
	b = s.db.NewBatch()
	defer b.Close()
	row.Revision, row.LeaseUntilMS = 3, 20000
	_, err = b.CompareAndSwapMQTTSession(7, 2, row)
	require.NoError(t, err)
	require.NoError(t, b.CreateUser(7, User{UID: "duplicate"}))
	require.NoError(t, b.CreateUser(7, User{UID: "duplicate"}))
	require.ErrorIs(t, b.Commit(ctx), dberrors.ErrAlreadyExists)
	got, _, err = s.db.HashSlot(7).GetMQTTSession(ctx, row.Namespace, row.ClientID)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Revision)
	page, _, _, err := s.db.HashSlot(7).ListMQTTSessionDeadlines(ctx, MQTTSessionDeadlineCursor{}, 20)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.EqualValues(t, 5000, page[0].LeaseUntilMS)
}

func TestMQTTSessionDeadlinePaginationAndRemoval(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		row := mqttSessionFixture()
		row.ClientID = id
		writeMQTTSession(t, s.db, row, 0)
	}
	row := mqttSessionFixture()
	row.ClientID, row.Revision, row.LeaseUntilMS = "b", 2, 0
	row.State, row.OfflineExpiresAtMS = MQTTSessionOffline, 8000
	writeMQTTSession(t, s.db, row, 1)
	row.ClientID, row.State, row.OfflineExpiresAtMS, row.TerminationReason = "c", MQTTSessionEnded, 0, MQTTSessionExpired
	writeMQTTSession(t, s.db, row, 1)
	shard := s.db.HashSlot(7)
	var after MQTTSessionDeadlineCursor
	var ids []string
	for range 3 {
		rows, next, done, err := shard.ListMQTTSessionDeadlines(ctx, after, 1)
		require.NoError(t, err)
		for _, r := range rows {
			ids = append(ids, r.ClientID)
		}
		if done {
			break
		}
		require.NotEqual(t, after, next)
		after = next
	}
	require.Equal(t, []string{"a", "b"}, ids)
	for _, limit := range []int{0, -1, 257} {
		_, _, _, err := shard.ListMQTTSessionDeadlines(ctx, MQTTSessionDeadlineCursor{}, limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
}

func TestMQTTSessionPinnedSnapshotAndInspection(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	row := mqttSessionFixture()
	writeMQTTSession(t, s.db, row, 0)
	reader, err := s.db.OpenBackupHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, err)
	defer reader.Close()
	updated := row
	updated.Revision, updated.LeaseUntilMS = 2, 9000
	writeMQTTSession(t, s.db, updated, 1)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	_, err = VerifyBackupHashSlotSnapshotReader(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)), false))
	got, found, err := target.db.HashSlot(7).GetMQTTSession(ctx, row.Namespace, row.ClientID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, row, got)
	page, _, _, err := target.db.HashSlot(7).ListMQTTSessionDeadlines(ctx, MQTTSessionDeadlineCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, []MQTTSession{row}, page)
	inspection, err := InspectScan(ctx, target.db, InspectScanRequest{Table: "mqtt_session", HashSlot: 7, HashSlotSet: true, Limit: 10})
	require.NoError(t, err)
	require.Len(t, inspection.Rows, 1)
	require.Equal(t, "alice", inspection.Rows[0]["uid"])
	require.Equal(t, "client", inspection.Rows[0]["client_id"])
	_, token := inspection.Rows[0]["token"]
	require.False(t, token)
}

func TestMQTTSessionRejectsInvalidRowsAndCorruptValues(t *testing.T) {
	row := mqttSessionFixture()
	for _, change := range []func(*MQTTSession){
		func(r *MQTTSession) { r.Namespace = "" }, func(r *MQTTSession) { r.ClientID = strings.Repeat("a", 1025) },
		func(r *MQTTSession) { r.UID = "a\x00b" }, func(r *MQTTSession) { r.OwnerBootID = "" },
		func(r *MQTTSession) { r.Generation = 0 }, func(r *MQTTSession) { r.Revision = 0 },
		func(r *MQTTSession) { r.OwnerGeneration = 0 }, func(r *MQTTSession) { r.OwnerNodeID = 0 },
		func(r *MQTTSession) { r.ConnectionID = 0 }, func(r *MQTTSession) { r.State = 99 },
		func(r *MQTTSession) { r.DeviceFlag = 99 }, func(r *MQTTSession) { r.LeaseUntilMS = -1 },
		func(r *MQTTSession) { r.ReceiveMaximum = 0 }, func(r *MQTTSession) { r.MaxPacketBytes = 0 },
		func(r *MQTTSession) { r.NextPacketID = 0 }, func(r *MQTTSession) { r.NextDeliveryOrder = 0 },
		func(r *MQTTSession) { r.PendingMessages = r.QuotaMessages + 1 }, func(r *MQTTSession) { r.TerminationReason = MQTTSessionExpired },
	} {
		candidate := row
		change(&candidate)
		require.ErrorIs(t, ValidateMQTTSession(candidate), dberrors.ErrInvalidArgument)
	}
	pk := mqttSessionTable.spec.Primary.Key(row)
	key, err := mqttSessionTable.primaryRowKey(7, pk)
	require.NoError(t, err)
	value, err := mqttSessionTable.encodeValue(key, row)
	require.NoError(t, err)
	got, err := mqttSessionTable.decodeValue(key, pk, value)
	require.NoError(t, err)
	require.Equal(t, row, got)
	for n := 0; n < len(value); n++ {
		_, err := mqttSessionTable.decodeValue(key, pk, value[:n])
		require.Error(t, err)
	}
	otherKey := append([]byte(nil), key...)
	otherKey[2] ^= 1
	_, err = mqttSessionTable.decodeValue(otherKey, pk, value)
	require.ErrorIs(t, err, dberrors.ErrChecksumMismatch)
	env, err := rowcodec.Unwrap(key, value)
	require.NoError(t, err)
	for _, corrupt := range [][]byte{
		rowcodec.Wrap(key, 2, env.Codec, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, rowcodec.CodecRaw, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, 0, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, env.Flags, nil),
	} {
		_, err := mqttSessionTable.decodeValue(key, pk, corrupt)
		require.Error(t, err)
	}
	// This zero-marker row ends at column 29; optional column 30 is omitted.
	// Unknown uint8 column 31 must not change old fields or require an envelope bump.
	future := append(append([]byte(nil), env.Payload...), 0x26, 7)
	got, err = mqttSessionTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, future))
	require.NoError(t, err)
	require.Equal(t, row, got)
}

func TestMQTTSessionLifecycleCannotRewriteDeliveryAccounting(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	row := mqttSessionFixture()
	row.PendingMessages, row.PendingBytes = 0, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, row, 0).Status)
	for _, change := range []func(*MQTTSession){
		func(r *MQTTSession) { r.PendingMessages = 1 },
		func(r *MQTTSession) { r.PendingBytes = 1 },
		func(r *MQTTSession) { r.NextPacketID++ },
		func(r *MQTTSession) { r.NextDeliveryOrder++ },
	} {
		next := row
		next.Revision++
		change(&next)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, s.db, next, 1).Status)
	}
}
