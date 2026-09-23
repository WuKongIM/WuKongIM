package meta

import (
	"bytes"
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func mqttCursorFixture() MQTTDeliveryCursorKey {
	return MQTTDeliveryCursorKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, SubscriptionGeneration: 2,
		SourceKind: MQTTSourceChannel, SourceID: "group:g", SourceGeneration: "incarnation-1"}
}

func mqttCursorMutation(session MQTTSession) MQTTDeliveryCursorMutation {
	return MQTTDeliveryCursorMutation{Key: mqttCursorFixture(), ExpectedRevision: session.Revision,
		OwnerGeneration: session.OwnerGeneration, OwnerNodeID: session.OwnerNodeID, OwnerBootID: session.OwnerBootID,
		ConnectionID: session.ConnectionID, Op: MQTTCursorInit, Topic: mqttSubscriptionFixture().Topic,
		AuthorizationVersion: 9, Through: 100, UpdatedAtMS: 1000}
}

func writeMQTTCursor(t *testing.T, db *MetaDB, m MQTTDeliveryCursorMutation) MQTTDeliveryCursorResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	result, err := b.MutateMQTTDeliveryCursor(7, m)
	require.NoError(t, err)
	require.Equal(t, MQTTDeliveryCursorResult{}, *result)
	require.NoError(t, b.Commit(context.Background()))
	return *result
}

func prepareMQTTCursorSession(t *testing.T, db *MetaDB) MQTTSession {
	t.Helper()
	session := mqttSessionFixture()
	session.PendingMessages, session.PendingBytes = 0, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, db, session, 0).Status)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, db, mqttSubscriptionMutation(session, mqttSubscriptionFixture())).Status)
	session.Revision = 2
	return session
}

func TestMQTTDeliveryCursorCountsBacklogWithoutCompletingDelivery(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	session := prepareMQTTCursorSession(t, s.db)
	m := mqttCursorMutation(session)
	result := writeMQTTCursor(t, s.db, m)
	require.Equal(t, MQTTSessionCASApplied, result.Status)
	require.EqualValues(t, 3, result.CurrentRevision)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTCursor(t, s.db, m).Status)
	changed := m
	changed.Through++
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, changed).Status)
	m.ExpectedRevision, m.Op, m.Through, m.AddedMessages, m.AddedBytes = 3, MQTTCursorAccount, 105, 3, 120
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	row, found, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), m.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 100, row.StartAfter)
	require.EqualValues(t, 105, row.AccountedThrough)
	require.EqualValues(t, 100, row.WindowThrough)
	require.EqualValues(t, 100, row.CompletedThrough)
	require.EqualValues(t, 3, row.PendingMessages)
	require.EqualValues(t, 120, row.PendingBytes)
	require.EqualValues(t, 4, row.Revision)
	require.Len(t, row.LastMutationDigest, 64)
	got, _, err := s.db.HashSlot(7).GetMQTTSession(context.Background(), "main", "client")
	require.NoError(t, err)
	require.EqualValues(t, 3, got.PendingMessages)
	require.EqualValues(t, 120, got.PendingBytes)
	require.EqualValues(t, 4, got.Revision)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTCursor(t, s.db, m).Status)
	// Empty qualifying coverage is valid, but it must not move safe completion.
	m.ExpectedRevision, m.Through, m.AddedMessages, m.AddedBytes = 4, 109, 0, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	row, _, err = s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), m.Key)
	require.NoError(t, err)
	require.EqualValues(t, 109, row.AccountedThrough)
	require.EqualValues(t, 100, row.CompletedThrough)
}

func TestMQTTDeliveryCursorRejectsStaleAuthorityAndRebinding(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	session := prepareMQTTCursorSession(t, s.db)
	m := mqttCursorMutation(session)
	for _, change := range []func(*MQTTDeliveryCursorMutation){
		func(m *MQTTDeliveryCursorMutation) { m.ExpectedRevision++ },
		func(m *MQTTDeliveryCursorMutation) { m.OwnerGeneration++ },
		func(m *MQTTDeliveryCursorMutation) { m.OwnerNodeID++ },
		func(m *MQTTDeliveryCursorMutation) { m.OwnerBootID = "restarted" },
		func(m *MQTTDeliveryCursorMutation) { m.ConnectionID++ },
		func(m *MQTTDeliveryCursorMutation) { m.Key.SessionGeneration++ },
		func(m *MQTTDeliveryCursorMutation) { m.Key.SubscriptionGeneration++ },
		func(m *MQTTDeliveryCursorMutation) { m.AuthorizationVersion++ },
		func(m *MQTTDeliveryCursorMutation) { m.Topic = "missing" },
		func(m *MQTTDeliveryCursorMutation) { m.Key.ClientID = "missing" },
	} {
		bad := m
		change(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, bad).Status)
	}
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	m.ExpectedRevision = 3
	m.Through++
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status, "initial source boundary is immutable")
	m.Op = MQTTCursorAccount
	for _, through := range []uint64{99, 100} {
		bad := m
		bad.Through = through
		require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, bad).Status)
	}
	m.Through, m.AddedMessages = 101, 2
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status)
	m.AddedMessages = 1
	m.Key.SourceGeneration = "new-source"
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status, "missing source is not an empty cursor")
	m.Key.SourceGeneration = "incarnation-1"
	session.Revision, session.State, session.LeaseUntilMS, session.TerminationReason = 4, MQTTSessionEnded, 0, MQTTSessionExpired
	writeMQTTSession(t, s.db, session, 3)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status)
	m.ExpectedRevision = 4
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status)
}

func TestMQTTDeliveryCursorQuotaEndsSessionAtomically(t *testing.T) {
	for _, bytesQuota := range []bool{false, true} {
		t.Run(map[bool]string{false: "count", true: "bytes"}[bytesQuota], func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			session := prepareMQTTCursorSession(t, s.db)
			session.Revision = 3
			if bytesQuota {
				session.QuotaBytes = 100
			} else {
				session.QuotaMessages = 2
			}
			writeMQTTSession(t, s.db, session, 2)
			m := mqttCursorMutation(session)
			require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
			m.ExpectedRevision, m.Op, m.Through, m.AddedMessages, m.AddedBytes = 4, MQTTCursorAccount, 103, 3, 120
			result := writeMQTTCursor(t, s.db, m)
			require.Equal(t, MQTTSessionCASApplied, result.Status)
			require.Equal(t, MQTTSessionEnded, result.SessionState)
			require.Equal(t, MQTTSessionQuota, result.TerminationReason)
			retry := writeMQTTCursor(t, s.db, m)
			require.Equal(t, MQTTSessionCASUnchanged, retry.Status)
			require.Equal(t, MQTTSessionQuota, retry.TerminationReason)
			got, _, err := s.db.HashSlot(7).GetMQTTSession(context.Background(), "main", "client")
			require.NoError(t, err)
			require.EqualValues(t, 3, got.PendingMessages)
			require.EqualValues(t, 120, got.PendingBytes)
			require.EqualValues(t, 0, got.LeaseUntilMS)
			row, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), m.Key)
			require.NoError(t, err)
			require.EqualValues(t, 103, row.AccountedThrough)
			require.EqualValues(t, 100, row.CompletedThrough)
		})
	}
}

func TestMQTTDeliveryCursorStorageFormatAndBounds(t *testing.T) {
	row := MQTTDeliveryCursor{Key: mqttCursorFixture(), Topic: mqttSubscriptionFixture().Topic,
		AuthorizationVersion: 9, StartAfter: 100, AccountedThrough: 105, WindowThrough: 103, CompletedThrough: 101,
		PendingMessages: 2, PendingBytes: 120, InflightCount: 1, InflightBytes: 60, HeadPacketID: 77, TailPacketID: 77, Revision: 8, LastMutationDigest: strings.Repeat("a", 64), UpdatedAtMS: 1000}
	for _, change := range []func(*MQTTDeliveryCursor){
		func(r *MQTTDeliveryCursor) { r.Key.Namespace = "" },
		func(r *MQTTDeliveryCursor) { r.Key.ClientID = strings.Repeat("a", 1025) },
		func(r *MQTTDeliveryCursor) { r.Key.SessionGeneration = 0 },
		func(r *MQTTDeliveryCursor) { r.Key.SubscriptionGeneration = 0 },
		func(r *MQTTDeliveryCursor) { r.Key.SourceKind = 2 },
		func(r *MQTTDeliveryCursor) { r.Key.SourceID = strings.Repeat("a", 4097) },
		func(r *MQTTDeliveryCursor) { r.Key.SourceGeneration = "" },
		func(r *MQTTDeliveryCursor) { r.Topic = "" },
		func(r *MQTTDeliveryCursor) { r.CompletedThrough = 99 },
		func(r *MQTTDeliveryCursor) { r.WindowThrough = 100 },
		func(r *MQTTDeliveryCursor) { r.AccountedThrough = 102 },
		func(r *MQTTDeliveryCursor) { r.PendingMessages = 6 },
		func(r *MQTTDeliveryCursor) { r.PendingMessages = 0 },
		func(r *MQTTDeliveryCursor) { r.Revision = 0 },
		func(r *MQTTDeliveryCursor) { r.LastMutationDigest = strings.Repeat("z", 64) },
		func(r *MQTTDeliveryCursor) { r.UpdatedAtMS = 0 },
	} {
		bad := row
		change(&bad)
		require.ErrorIs(t, ValidateMQTTDeliveryCursor(bad), dberrors.ErrInvalidArgument)
	}
	pk := mqttDeliveryCursorTable.spec.Primary.Key(row)
	key, err := mqttDeliveryCursorTable.primaryRowKey(7, pk)
	require.NoError(t, err)
	value, err := mqttDeliveryCursorTable.encodeValue(key, row)
	require.NoError(t, err)
	got, err := mqttDeliveryCursorTable.decodeValue(key, pk, value)
	require.NoError(t, err)
	require.Equal(t, row, got)
	for i := 0; i < len(value); i++ {
		_, err = mqttDeliveryCursorTable.decodeValue(key, pk, value[:i])
		require.Error(t, err)
	}
	other := append([]byte(nil), key...)
	other[2] ^= 1
	_, err = mqttDeliveryCursorTable.decodeValue(other, pk, value)
	require.ErrorIs(t, err, dberrors.ErrChecksumMismatch)
	env, err := rowcodec.Unwrap(key, value)
	require.NoError(t, err)
	for _, bad := range [][]byte{
		rowcodec.Wrap(key, 2, env.Codec, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, rowcodec.CodecRaw, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, 0, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, env.Flags, nil),
	} {
		_, err = mqttDeliveryCursorTable.decodeValue(key, pk, bad)
		require.Error(t, err)
	}
	future := append(append([]byte(nil), env.Payload...), 0x16, 7) // optional column 25
	got, err = mqttDeliveryCursorTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, future))
	require.NoError(t, err)
	require.Equal(t, row, got)
	for _, change := range []func(*MQTTDeliveryCursorMutation){
		func(m *MQTTDeliveryCursorMutation) { m.Op = 0 },
		func(m *MQTTDeliveryCursorMutation) { m.ExpectedRevision = 0 },
		func(m *MQTTDeliveryCursorMutation) { m.ExpectedRevision = ^uint64(0) },
		func(m *MQTTDeliveryCursorMutation) { m.OwnerGeneration = 0 },
		func(m *MQTTDeliveryCursorMutation) { m.OwnerBootID = "" },
		func(m *MQTTDeliveryCursorMutation) { m.ConnectionID = 0 },
		func(m *MQTTDeliveryCursorMutation) { m.AddedMessages = 1 },
		func(m *MQTTDeliveryCursorMutation) { m.Op = MQTTCursorAccount; m.AddedBytes = 1 },
	} {
		m := mqttCursorMutation(mqttSessionFixture())
		change(&m)
		require.ErrorIs(t, ValidateMQTTDeliveryCursorMutation(m), dberrors.ErrInvalidArgument)
	}
}

func TestMQTTDeliveryCursorBatchOverlayAndRollback(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	session := mqttSessionFixture()
	session.PendingMessages, session.PendingBytes = 0, 0
	b := s.db.NewBatch()
	defer b.Close()
	_, err := b.CompareAndSwapMQTTSession(7, 0, session)
	require.NoError(t, err)
	_, err = b.MutateMQTTSubscription(7, mqttSubscriptionMutation(session, mqttSubscriptionFixture()))
	require.NoError(t, err)
	session.Revision = 2
	m := mqttCursorMutation(session)
	r1, err := b.MutateMQTTDeliveryCursor(7, m)
	require.NoError(t, err)
	m.ExpectedRevision, m.Op, m.Through, m.AddedMessages, m.AddedBytes = 3, MQTTCursorAccount, 104, 2, 80
	r2, err := b.MutateMQTTDeliveryCursor(7, m)
	require.NoError(t, err)
	require.NoError(t, b.SetSlotAppliedIndex(1, 99))
	require.NoError(t, b.Commit(ctx))
	require.Equal(t, MQTTSessionCASApplied, r1.Status)
	require.Equal(t, MQTTSessionCASApplied, r2.Status)
	before, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, m.Key)
	require.NoError(t, err)
	b = s.db.NewBatch()
	defer b.Close()
	m.ExpectedRevision, m.Through, m.AddedMessages, m.AddedBytes = 4, 106, 1, 20
	_, err = b.MutateMQTTDeliveryCursor(7, m)
	require.NoError(t, err)
	require.NoError(t, b.CreateUser(7, User{UID: "duplicate"}))
	require.NoError(t, b.CreateUser(7, User{UID: "duplicate"}))
	require.NoError(t, b.SetSlotAppliedIndex(1, 100))
	require.ErrorIs(t, b.Commit(ctx), dberrors.ErrAlreadyExists)
	got, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, m.Key)
	require.NoError(t, err)
	require.Equal(t, before, got)
	aggregate, _, err := s.db.HashSlot(7).GetMQTTSession(ctx, "main", "client")
	require.NoError(t, err)
	require.EqualValues(t, 2, aggregate.PendingMessages)
	require.EqualValues(t, 80, aggregate.PendingBytes)
	require.EqualValues(t, 4, aggregate.Revision)
	index, err := s.db.SlotAppliedIndex(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 99, index)
}

func TestMQTTDeliveryCursorMultipleSourcesOfflineAccountingAndOverflow(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	session := prepareMQTTCursorSession(t, s.db)
	m := mqttCursorMutation(session)
	writeMQTTCursor(t, s.db, m)
	second := m
	second.ExpectedRevision, second.Key.SourceID = 3, "person:alice:bob"
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, second).Status)
	session.Revision, session.State, session.LeaseUntilMS, session.OfflineExpiresAtMS = 5, MQTTSessionOffline, 0, 5000
	writeMQTTSession(t, s.db, session, 4)
	m.ExpectedRevision, m.Op, m.Through, m.AddedMessages, m.AddedBytes = 5, MQTTCursorAccount, 101, 1, 60
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	second.ExpectedRevision, second.Op, second.Through, second.AddedMessages, second.AddedBytes = 6, MQTTCursorAccount, 102, 2, 80
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, second).Status)
	got, _, err := s.db.HashSlot(7).GetMQTTSession(ctx, "main", "client")
	require.NoError(t, err)
	require.EqualValues(t, 3, got.PendingMessages)
	require.EqualValues(t, 140, got.PendingBytes)
	// Recovery after takeover may account backlog; the displaced owner cannot.
	got.Revision, got.OwnerGeneration, got.OwnerNodeID, got.State, got.LeaseUntilMS, got.OfflineExpiresAtMS = 8, 2, 3, MQTTSessionActive, 9000, 0
	writeMQTTSession(t, s.db, got, 7)
	m.ExpectedRevision, m.Through = 8, 103
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status)
	m.OwnerGeneration, m.OwnerNodeID = 2, 3
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	// An unrelated session write is not the receipt for a cursor request.
	m.ExpectedRevision, m.Through = 9, 104
	got, _, err = s.db.HashSlot(7).GetMQTTSession(ctx, "main", "client")
	require.NoError(t, err)
	got.Revision++
	got.QuotaBytes = ^uint64(0)

	writeMQTTSession(t, s.db, got, 9)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status)
	m.ExpectedRevision = 10
	m.AddedBytes = ^uint64(0) - 201
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	m.ExpectedRevision, m.Through, m.AddedBytes = 11, 105, 60
	require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status, "byte arithmetic must not wrap")
	row, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, m.Key)
	require.NoError(t, err)
	require.EqualValues(t, 104, row.AccountedThrough)
}

func TestMQTTDeliveryCursorPagesPinnedSnapshotAndInspection(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	session := prepareMQTTCursorSession(t, s.db)
	m := mqttCursorMutation(session)
	var original []MQTTDeliveryCursor
	for _, source := range []string{"a", "b", "c"} {
		m.Key.SourceID = source
		require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
		row, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, m.Key)
		require.NoError(t, err)
		original = append(original, row)
		m.ExpectedRevision++
	}
	var after MQTTDeliveryCursorKey
	var rows []MQTTDeliveryCursor
	for range 4 {
		page, next, done, err := s.db.HashSlot(7).ListMQTTDeliveryCursors(ctx, "main", "client", 1, 2, after, 1)
		require.NoError(t, err)
		rows = append(rows, page...)
		if done {
			break
		}
		require.NotEqual(t, after, next)
		after = next
	}
	require.Equal(t, original, rows)
	for _, limit := range []int{-1, 0, 257} {
		_, _, _, err := s.db.HashSlot(7).ListMQTTDeliveryCursors(ctx, "main", "client", 1, 2, MQTTDeliveryCursorKey{}, limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	after.Namespace = "foreign"
	_, _, _, err := s.db.HashSlot(7).ListMQTTDeliveryCursors(ctx, "main", "client", 1, 2, after, 1)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	reader, err := s.db.OpenBackupHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, err)
	defer reader.Close()
	m.Op, m.Through, m.AddedMessages, m.AddedBytes = MQTTCursorAccount, 101, 1, 20
	writeMQTTCursor(t, s.db, m)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	_, err = VerifyBackupHashSlotSnapshotReader(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)), false))
	restored, _, done, err := target.db.HashSlot(7).ListMQTTDeliveryCursors(ctx, "main", "client", 1, 0, MQTTDeliveryCursorKey{}, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, original, restored)
	inspection, err := InspectScan(ctx, target.db, InspectScanRequest{Table: "mqtt_delivery_cursor", HashSlot: 7, HashSlotSet: true, Limit: 10})
	require.NoError(t, err)
	require.Len(t, inspection.Rows, 3)
	require.Equal(t, "a", inspection.Rows[0]["source_id"])
	require.EqualValues(t, 100, inspection.Rows[0]["completed_through"])
}
