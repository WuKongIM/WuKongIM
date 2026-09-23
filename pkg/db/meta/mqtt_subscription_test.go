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

func mqttSubscriptionFixture() MQTTSubscription {
	return MQTTSubscription{Namespace: "main", ClientID: "client", SessionGeneration: 1,
		Topic: "wk/v1/groups/Zw/messages", Generation: 2, Revision: 2, TargetKind: MQTTSubscriptionGroup,
		TargetID: "g", GrantedQoS: 1, NoLocal: true, RetainAsPublished: true, RetainHandling: 2,
		SubscriptionIdentifier: 123, AuthorizationVersion: 9, Stage: MQTTSubscriptionPreparing,
		OperationID: "subscribe-1", RecoveryAtMS: 1000, UpdatedAtMS: 1000}
}

func mqttSubscriptionMutation(session MQTTSession, row MQTTSubscription) MQTTSubscriptionMutation {
	row.Revision = session.Revision + 1
	return MQTTSubscriptionMutation{ExpectedRevision: session.Revision, OwnerGeneration: session.OwnerGeneration,
		OwnerNodeID: session.OwnerNodeID, OwnerBootID: session.OwnerBootID, ConnectionID: session.ConnectionID,
		Subscription: row}
}

func writeMQTTSubscription(t *testing.T, db *MetaDB, m MQTTSubscriptionMutation) MQTTSessionCASResult {
	t.Helper()
	m.Subscription.Revision = m.ExpectedRevision + 1
	b := db.NewBatch()
	defer b.Close()
	result, err := b.MutateMQTTSubscription(7, m)
	require.NoError(t, err)
	require.Equal(t, MQTTSessionCASResult{}, *result)
	require.NoError(t, b.Commit(context.Background()))
	return *result
}

func TestMQTTSubscriptionMutationFencesOwnerAndPreservesSession(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	session, row := mqttSessionFixture(), mqttSubscriptionFixture()
	m := mqttSubscriptionMutation(session, row)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSubscription(t, s.db, m).Status)
	writeMQTTSession(t, s.db, session, 0)
	for _, change := range []func(*MQTTSubscriptionMutation){
		func(m *MQTTSubscriptionMutation) { m.ExpectedRevision++ },
		func(m *MQTTSubscriptionMutation) { m.OwnerGeneration++ },
		func(m *MQTTSubscriptionMutation) { m.OwnerNodeID++ },
		func(m *MQTTSubscriptionMutation) { m.OwnerBootID = "restart" },
		func(m *MQTTSubscriptionMutation) { m.ConnectionID++ },
		func(m *MQTTSubscriptionMutation) { m.Subscription.SessionGeneration++ },
		func(m *MQTTSubscriptionMutation) { m.Subscription.Generation++ },
	} {
		bad := m
		change(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSubscription(t, s.db, bad).Status)
	}
	require.Equal(t, MQTTSessionCASResult{Status: MQTTSessionCASApplied, CurrentRevision: 2}, writeMQTTSubscription(t, s.db, m))
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTSubscription(t, s.db, m).Status)
	changed := m
	changed.Subscription.GrantedQoS = 0
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSubscription(t, s.db, changed).Status)
	got, found, err := s.db.HashSlot(7).GetMQTTSubscription(ctx, row.Namespace, row.ClientID, 1, row.Topic)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, row, got)
	gotSession, _, err := s.db.HashSlot(7).GetMQTTSession(ctx, session.Namespace, session.ClientID)
	require.NoError(t, err)
	session.Revision = 2
	require.Equal(t, session, gotSession, "subscription mutation must preserve owner, quotas and backlog")
	session.OwnerGeneration, session.OwnerNodeID, session.OwnerBootID, session.ConnectionID, session.Revision = 2, 3, "boot-b", 19, 3
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, session, 2).Status)
	m.ExpectedRevision = 3 // A late connection cannot use a freshly read revision to bypass its stale owner fence.
	m.Subscription.Stage, m.Subscription.RecoveryAtMS = MQTTSubscriptionActive, 0
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSubscription(t, s.db, m).Status)
	m = mqttSubscriptionMutation(session, m.Subscription)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, m).Status)
}

func TestMQTTSubscriptionStorageFormatAndBounds(t *testing.T) {
	row := mqttSubscriptionFixture()
	for _, change := range []func(*MQTTSubscription){
		func(r *MQTTSubscription) { r.Namespace = "" },
		func(r *MQTTSubscription) { r.ClientID = strings.Repeat("a", 1025) },
		func(r *MQTTSubscription) { r.SessionGeneration = 0 },
		func(r *MQTTSubscription) { r.Topic = strings.Repeat("a", 2049) },
		func(r *MQTTSubscription) { r.Generation = 0 },
		func(r *MQTTSubscription) { r.Revision = 0 },
		func(r *MQTTSubscription) { r.TargetKind = 3 },
		func(r *MQTTSubscription) { r.TargetID = "" },
		func(r *MQTTSubscription) { r.GrantedQoS = 2 },
		func(r *MQTTSubscription) { r.RetainHandling = 3 },
		func(r *MQTTSubscription) { r.SubscriptionIdentifier = 268435456 },
		func(r *MQTTSubscription) { r.Stage = 0 },
		func(r *MQTTSubscription) { r.OperationID = "" },
		func(r *MQTTSubscription) { r.RecoveryAtMS = 0 },
		func(r *MQTTSubscription) { r.Stage = MQTTSubscriptionActive },
		func(r *MQTTSubscription) { r.UpdatedAtMS = 0 },
	} {
		bad := row
		change(&bad)
		require.ErrorIs(t, ValidateMQTTSubscription(bad), dberrors.ErrInvalidArgument)
	}
	pk := mqttSubscriptionTable.spec.Primary.Key(row)
	key, err := mqttSubscriptionTable.primaryRowKey(7, pk)
	require.NoError(t, err)
	value, err := mqttSubscriptionTable.encodeValue(key, row)
	require.NoError(t, err)
	got, err := mqttSubscriptionTable.decodeValue(key, pk, value)
	require.NoError(t, err)
	require.Equal(t, row, got)
	for i := 0; i < len(value); i++ {
		_, err = mqttSubscriptionTable.decodeValue(key, pk, value[:i])
		require.Error(t, err)
	}
	other := append([]byte(nil), key...)
	other[2] ^= 1
	_, err = mqttSubscriptionTable.decodeValue(other, pk, value)
	require.ErrorIs(t, err, dberrors.ErrChecksumMismatch)
	env, err := rowcodec.Unwrap(key, value)
	require.NoError(t, err)
	for _, bad := range [][]byte{
		rowcodec.Wrap(key, 2, env.Codec, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, rowcodec.CodecRaw, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, 0, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, env.Flags, nil),
		// Column 5 must be a uint64, not a string.
		rowcodec.Wrap(key, 1, env.Codec, env.Flags, []byte{0x51, 0}),
	} {
		_, err = mqttSubscriptionTable.decodeValue(key, pk, bad)
		require.Error(t, err)
	}
	// Column 19, uint8, after final column 18. Future optional fields are skipped.
	future := append(append([]byte(nil), env.Payload...), 0x16, 7)
	got, err = mqttSubscriptionTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, future))
	require.NoError(t, err)
	require.Equal(t, row, got)
	db := openTestMetaStore(t)
	defer db.close(t)
	for _, change := range []func(*MQTTSubscriptionMutation){
		func(m *MQTTSubscriptionMutation) { m.ExpectedRevision = 0 },
		func(m *MQTTSubscriptionMutation) { m.ExpectedRevision = ^uint64(0) },
		func(m *MQTTSubscriptionMutation) { m.OwnerGeneration = 0 },
		func(m *MQTTSubscriptionMutation) { m.OwnerNodeID = 0 },
		func(m *MQTTSubscriptionMutation) { m.OwnerBootID = "" },
		func(m *MQTTSubscriptionMutation) { m.ConnectionID = 0 },
		func(m *MQTTSubscriptionMutation) { m.Subscription.Revision++ },
	} {
		m := mqttSubscriptionMutation(mqttSessionFixture(), row)
		change(&m)
		b := db.db.NewBatch()
		_, err = b.MutateMQTTSubscription(7, m)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
		require.NoError(t, b.Close())
	}
}

func TestMQTTSubscriptionLifecycleKeepsProgressIdentity(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	session, row := mqttSessionFixture(), mqttSubscriptionFixture()
	writeMQTTSession(t, s.db, session, 0)
	apply := func(next MQTTSubscription) {
		t.Helper()
		next.Revision = session.Revision + 1
		require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(session, next)).Status)
		session.Revision++
		row = next
	}
	reject := func(next MQTTSubscription) {
		t.Helper()
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(session, next)).Status)
	}
	skipped := row
	skipped.Stage, skipped.RecoveryAtMS = MQTTSubscriptionActive, 0
	reject(skipped)
	apply(row)
	for _, change := range []func(*MQTTSubscription){
		func(r *MQTTSubscription) { r.Generation++ },
		func(r *MQTTSubscription) { r.TargetID = "another" },
		func(r *MQTTSubscription) { r.TargetKind = MQTTSubscriptionUserInbox },
		func(r *MQTTSubscription) { r.AuthorizationVersion++ },
		func(r *MQTTSubscription) { r.OperationID = "another" },
		func(r *MQTTSubscription) { r.GrantedQoS = 0 },
	} {
		bad := row
		bad.Stage, bad.RecoveryAtMS = MQTTSubscriptionActive, 0
		change(&bad)
		reject(bad)
	}
	active := row
	active.Stage, active.RecoveryAtMS = MQTTSubscriptionActive, 0
	apply(active)
	replacement := row
	replacement.GrantedQoS, replacement.NoLocal, replacement.SubscriptionIdentifier = 0, false, 0
	replacement.RetainAsPublished, replacement.RetainHandling = false, 0
	apply(replacement)
	got, _, err := s.db.HashSlot(7).GetMQTTSubscription(ctx, row.Namespace, row.ClientID, 1, row.Topic)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Generation, "option replacement must retain delivery cursor identity")
	replacement.Revision = 4
	require.Equal(t, replacement, got)
	removed := row
	removed.Stage = MQTTSubscriptionRemoved
	reject(removed)
	removing := row
	removing.Stage, removing.RecoveryAtMS = MQTTSubscriptionRemoving, 2000
	apply(removing)
	reject(active)
	removed = row
	removed.Stage, removed.RecoveryAtMS = MQTTSubscriptionRemoved, 0
	apply(removed)
	fresh := mqttSubscriptionFixture()
	reject(fresh)
	fresh.Generation, fresh.OperationID = session.Revision+1, "subscribe-2"
	apply(fresh)
	require.EqualValues(t, 7, row.Generation)

	// Recovery can finish already-durable work while offline, but not create more.
	session.Revision++
	session.State, session.LeaseUntilMS, session.OfflineExpiresAtMS = MQTTSessionOffline, 0, 5000
	writeMQTTSession(t, s.db, session, session.Revision-1)
	active = row
	active.Stage, active.RecoveryAtMS = MQTTSubscriptionActive, 0
	apply(active)
	replacedOffline := row
	replacedOffline.NoLocal = !row.NoLocal
	reject(replacedOffline)
	newTopic := row
	newTopic.Topic, newTopic.Stage, newTopic.RecoveryAtMS, newTopic.Generation = "other", MQTTSubscriptionPreparing, 1000, session.Revision+1
	reject(newTopic)
	session.Revision++
	session.State, session.OfflineExpiresAtMS, session.TerminationReason = MQTTSessionEnded, 0, MQTTSessionExpired
	writeMQTTSession(t, s.db, session, session.Revision-1)
	reject(active)
	removing = row
	removing.Stage, removing.RecoveryAtMS = MQTTSubscriptionRemoving, 2000
	apply(removing)
	// A new generation can clean up an older subscription, but cannot resurrect it.
	session.Revision++
	session.Generation++
	session.OwnerGeneration++
	session.State, session.LeaseUntilMS, session.TerminationReason = MQTTSessionActive, 9000, 0
	writeMQTTSession(t, s.db, session, session.Revision-1)
	reject(active)
	removed = row
	removed.Stage, removed.RecoveryAtMS = MQTTSubscriptionRemoved, 0
	apply(removed)
}

func TestMQTTSubscriptionBatchOverlayAndRollback(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	session, row := mqttSessionFixture(), mqttSubscriptionFixture()
	b := s.db.NewBatch()
	defer b.Close()
	_, err := b.CompareAndSwapMQTTSession(7, 0, session)
	require.NoError(t, err)
	r1, err := b.MutateMQTTSubscription(7, mqttSubscriptionMutation(session, row))
	require.NoError(t, err)
	session.Revision++
	row.Stage, row.RecoveryAtMS, row.Revision = MQTTSubscriptionActive, 0, 3
	r2, err := b.MutateMQTTSubscription(7, mqttSubscriptionMutation(session, row))
	require.NoError(t, err)
	require.NoError(t, b.SetSlotAppliedIndex(1, 99))
	require.NoError(t, b.Commit(ctx))
	require.Equal(t, MQTTSessionCASApplied, r1.Status)
	require.Equal(t, MQTTSessionCASApplied, r2.Status)
	session.Revision++
	b = s.db.NewBatch()
	defer b.Close()
	next := row
	next.Stage, next.RecoveryAtMS = MQTTSubscriptionRemoving, 3000
	_, err = b.MutateMQTTSubscription(7, mqttSubscriptionMutation(session, next))
	require.NoError(t, err)
	require.NoError(t, b.CreateUser(7, User{UID: "duplicate"}))
	require.NoError(t, b.CreateUser(7, User{UID: "duplicate"}))
	require.NoError(t, b.SetSlotAppliedIndex(1, 100))
	require.ErrorIs(t, b.Commit(ctx), dberrors.ErrAlreadyExists)
	got, _, err := s.db.HashSlot(7).GetMQTTSubscription(ctx, row.Namespace, row.ClientID, 1, row.Topic)
	require.NoError(t, err)
	require.Equal(t, row, got)
	gotSession, _, err := s.db.HashSlot(7).GetMQTTSession(ctx, session.Namespace, session.ClientID)
	require.NoError(t, err)
	require.EqualValues(t, 3, gotSession.Revision)
	index, err := s.db.SlotAppliedIndex(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 99, index)
}

func TestMQTTSubscriptionRecoveryPagesSnapshotAndInspection(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	base := mqttSessionFixture()
	writeMQTTSession(t, s.db, base, 0)
	a := mqttSubscriptionFixture()
	a.Topic = "a"
	b := a
	b.Topic, b.Generation, b.Revision, b.OperationID = "b", 3, 3, "subscribe-b"
	m := mqttSubscriptionMutation(base, a)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, m).Status)
	base.Revision++
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(base, b)).Status)
	// Equal recovery time and topic, differing namespace and session generation.
	otherSession := mqttSessionFixture()
	otherSession.Namespace = "next"
	writeMQTTSession(t, s.db, otherSession, 0)
	other := a
	other.Namespace = "next"
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(otherSession, other)).Status)
	base.Revision, base.Generation, base.OwnerGeneration = 4, 2, 2
	writeMQTTSession(t, s.db, base, 3)
	newer := a
	newer.SessionGeneration, newer.Generation, newer.Revision, newer.OperationID = 2, 5, 5, "subscribe-new"
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(base, newer)).Status)
	shard := s.db.HashSlot(7)
	var after MQTTSubscriptionRecoveryCursor
	var rows []MQTTSubscription
	for range 5 {
		page, next, done, err := shard.ListMQTTSubscriptionRecovery(ctx, after, 1)
		require.NoError(t, err)
		rows = append(rows, page...)
		if done {
			break
		}
		require.NotEqual(t, after, next)
		after = next
	}
	require.Equal(t, []MQTTSubscription{a, b, newer, other}, rows)
	page, nextTopic, done, err := shard.ListMQTTSubscriptions(ctx, "main", "client", 1, "", 1)
	require.NoError(t, err)
	require.Equal(t, []MQTTSubscription{a}, page)
	require.False(t, done)
	page, _, done, err = shard.ListMQTTSubscriptions(ctx, "main", "client", 1, nextTopic, 1)
	require.NoError(t, err)
	require.Equal(t, []MQTTSubscription{b}, page)
	require.True(t, done)
	for _, limit := range []int{0, -1, 257} {
		_, _, _, err = shard.ListMQTTSubscriptions(ctx, "main", "client", 1, "", limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
		_, _, _, err = shard.ListMQTTSubscriptionRecovery(ctx, MQTTSubscriptionRecoveryCursor{}, limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	_, _, _, err = shard.ListMQTTSubscriptionRecovery(ctx, MQTTSubscriptionRecoveryCursor{RecoveryAtMS: 1}, 1)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	reader, err := s.db.OpenBackupHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, err)
	defer reader.Close()
	base.Revision = 5
	newer.Stage, newer.RecoveryAtMS = MQTTSubscriptionActive, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(base, newer)).Status)
	page, _, _, err = shard.ListMQTTSubscriptionRecovery(ctx, MQTTSubscriptionRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, []MQTTSubscription{a, b, other}, page, "activation must remove the obsolete recovery index")
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	_, err = VerifyBackupHashSlotSnapshotReader(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)), false))
	restored, _, _, err := target.db.HashSlot(7).ListMQTTSubscriptionRecovery(ctx, MQTTSubscriptionRecoveryCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, rows, restored, "snapshot must preserve its original recovery index and rows")
	inspection, err := InspectScan(ctx, target.db, InspectScanRequest{Table: "mqtt_subscription", HashSlot: 7, HashSlotSet: true, Limit: 10})
	require.NoError(t, err)
	require.Len(t, inspection.Rows, 4)
	require.Equal(t, "a", inspection.Rows[0]["topic"])
	require.EqualValues(t, 2, inspection.Rows[0]["generation"])
}

func TestMQTTSubscriptionUnrelatedSessionWriteIsNotAnExactRetry(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	session, row := mqttSessionFixture(), mqttSubscriptionFixture()
	writeMQTTSession(t, s.db, session, 0)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(session, row)).Status)
	session.Revision = 2
	pending := mqttSubscriptionMutation(session, row)
	session.Revision, session.State, session.LeaseUntilMS, session.TerminationReason = 3, MQTTSessionEnded, 0, MQTTSessionExpired
	writeMQTTSession(t, s.db, session, 2)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSubscription(t, s.db, pending).Status,
		"a different session write advanced the revision; this intent mutation never committed")
}
