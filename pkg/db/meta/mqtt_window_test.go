package meta

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/stretchr/testify/require"
)

func TestMQTTWindowOptionalFieldsPreserveOlderRows(t *testing.T) {
	// Literal value payloads captured before columns 27/28 and 19–24 existed.
	session := mqttSessionFixture()
	pk := mqttSessionTable.spec.Primary.Key(session)
	key, err := mqttSessionTable.primaryRowKey(7, pk)
	require.NoError(t, err)
	payload, err := hex.DecodeString("3105616c69636514011401140114021106626f6f742d61141113904e16011480a30513001601144014808040140114011400140014904e14808080201403160013d00f")
	require.NoError(t, err)
	got, err := mqttSessionTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, payload))
	require.NoError(t, err)
	require.Equal(t, session, got)
	require.Zero(t, got.OutboundInflight)
	require.Zero(t, got.WindowLimit)
	session.PendingMessages, session.PendingBytes, session.OutboundInflight, session.WindowLimit = 2, 120, 2, 64
	value, err := mqttSessionTable.encodeValue(key, session)
	require.NoError(t, err)
	got, err = mqttSessionTable.decodeValue(key, pk, value)
	require.NoError(t, err)
	require.Equal(t, session, got)
	for _, change := range []func(*MQTTSession){
		func(r *MQTTSession) { r.OutboundInflight = 1025 },
		func(r *MQTTSession) { r.WindowLimit = 1025 },
	} {
		bad := session
		change(&bad)
		require.ErrorIs(t, ValidateMQTTSession(bad), dberrors.ErrInvalidArgument)
	}
	cursor := MQTTDeliveryCursor{Key: mqttCursorFixture(), Topic: mqttSubscriptionFixture().Topic, AuthorizationVersion: 9,
		StartAfter: 100, AccountedThrough: 105, WindowThrough: 101, CompletedThrough: 101,
		PendingMessages: 2, PendingBytes: 120, Revision: 8, LastMutationDigest: strings.Repeat("a", 64), UpdatedAtMS: 1000}
	cursorPK := mqttDeliveryCursorTable.spec.Primary.Key(cursor)
	cursorKey, err := mqttDeliveryCursorTable.primaryRowKey(7, cursorPK)
	require.NoError(t, err)
	payload, err = hex.DecodeString("8118776b2f76312f67726f7570732f5a772f6d657373616765731409146414691465146514021478140811406161616161616161616161616161616161616161616161616161616161616161616161616161616161616161616161616161616161616161616161616161616113d00f")
	require.NoError(t, err)
	old, err := mqttDeliveryCursorTable.decodeValue(cursorKey, cursorPK, rowcodec.Wrap(cursorKey, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, payload))
	require.NoError(t, err)
	require.Equal(t, cursor, old)
	cursor.WindowThrough, cursor.InflightCount, cursor.InflightBytes = 103, 1, 60
	cursor.HeadPacketID, cursor.TailPacketID, cursor.LastWindowPacketID, cursor.LastWindowDeliveryOrder = 77, 77, 77, 10
	value, err = mqttDeliveryCursorTable.encodeValue(cursorKey, cursor)
	require.NoError(t, err)
	decoded, err := mqttDeliveryCursorTable.decodeValue(cursorKey, cursorPK, value)
	require.NoError(t, err)
	require.Equal(t, cursor, decoded)
	for _, change := range []func(*MQTTDeliveryCursor){
		func(r *MQTTDeliveryCursor) { r.InflightCount = 3 },
		func(r *MQTTDeliveryCursor) { r.InflightBytes = 121 },
		func(r *MQTTDeliveryCursor) { r.HeadPacketID = 0 },
		func(r *MQTTDeliveryCursor) { r.TailPacketID = 78 },
		func(r *MQTTDeliveryCursor) { r.CompletedThrough = 103 },
		func(r *MQTTDeliveryCursor) { r.WindowThrough = 105 },
		func(r *MQTTDeliveryCursor) { r.LastWindowPacketID = 0 },
	} {
		bad := cursor
		change(&bad)
		require.ErrorIs(t, ValidateMQTTDeliveryCursor(bad), dberrors.ErrInvalidArgument)
	}
}

func prepareMQTTWindow(t *testing.T, db *MetaDB, limit, receive, startID uint16) MQTTDeliveryCursorKey {
	t.Helper()
	session := mqttSessionFixture()
	session.WindowLimit, session.ReceiveMaximum, session.NextPacketID = limit, receive, startID
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, db, session, 0).Status)
	sub := mqttSubscriptionFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, db, mqttSubscriptionMutation(session, sub)).Status)
	session.Revision = 2
	sub.Stage, sub.RecoveryAtMS = MQTTSubscriptionActive, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, db, mqttSubscriptionMutation(session, sub)).Status)
	session.Revision = 3
	m := mqttCursorMutation(session)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, db, m).Status)
	m.ExpectedRevision, m.Op, m.Through, m.AddedMessages, m.AddedBytes = 4, MQTTCursorAccount, 110, 4, 400
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, db, m).Status)
	return m.Key
}

func mqttWindowMutation(t *testing.T, db *MetaDB, op MQTTWindowOp) MQTTWindowMutation {
	t.Helper()
	session, _, err := db.HashSlot(7).GetMQTTSession(context.Background(), "main", "client")
	require.NoError(t, err)
	return MQTTWindowMutation{Key: mqttCursorFixture(), ExpectedRevision: session.Revision, OwnerGeneration: session.OwnerGeneration,
		OwnerNodeID: session.OwnerNodeID, OwnerBootID: session.OwnerBootID, ConnectionID: session.ConnectionID, Op: op, UpdatedAtMS: 2000}
}

func mqttPublication(position uint64) MQTTInflightPublication {
	return MQTTInflightPublication{Position: position, MessageID: 900 + position, MessageSeq: position, ContentVersion: 1,
		ContentHash: strings.Repeat("a", 64), Bytes: 100, SubscriptionIdentifier: 123}
}

func writeMQTTWindow(t *testing.T, db *MetaDB, m MQTTWindowMutation) MQTTWindowResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	result, err := b.MutateMQTTWindow(7, m)
	require.NoError(t, err)
	require.Equal(t, MQTTWindowResult{}, *result)
	require.NoError(t, b.Commit(context.Background()))
	return *result
}

func TestMQTTWindowOutOfOrderACKPreservesGapsAndWindowCredits(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	key := prepareMQTTWindow(t, s.db, 3, 64, 65535)
	var exchanges []MQTTWindowResult
	for _, position := range []uint64{101, 103, 104} {
		m := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
		m.Publication = mqttPublication(position)
		result := writeMQTTWindow(t, s.db, m)
		require.Equal(t, MQTTWindowApplied, result.Status)
		retry := writeMQTTWindow(t, s.db, m)
		require.Equal(t, MQTTWindowUnchanged, retry.Status)
		require.Equal(t, result.PacketID, retry.PacketID)
		require.Equal(t, result.DeliveryOrder, retry.DeliveryOrder)
		changed := m
		changed.Publication.ContentHash = strings.Repeat("b", 64)
		require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, changed).Status)
		exchanges = append(exchanges, result)
	}
	require.Equal(t, []uint16{65535, 1, 2}, []uint16{exchanges[0].PacketID, exchanges[1].PacketID, exchanges[2].PacketID})
	require.EqualValues(t, 1, exchanges[0].DeliveryOrder)
	require.EqualValues(t, 3, exchanges[2].DeliveryOrder)
	full := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	full.Publication = mqttPublication(108)
	require.Equal(t, MQTTWindowFull, writeMQTTWindow(t, s.db, full).Status)
	ack := func(exchange MQTTWindowResult) {
		t.Helper()
		m := mqttWindowMutation(t, s.db, MQTTWindowAck)
		m.PacketID, m.DeliveryOrder = exchange.PacketID, exchange.DeliveryOrder
		result := writeMQTTWindow(t, s.db, m)
		require.Equal(t, MQTTWindowApplied, result.Status)
		retry := writeMQTTWindow(t, s.db, m)
		require.Equal(t, MQTTWindowUnchanged, retry.Status)
		require.Equal(t, exchange.PacketID, retry.PacketID)
		require.Equal(t, exchange.DeliveryOrder, retry.DeliveryOrder)
	}
	ack(exchanges[1])
	row, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, key)
	require.NoError(t, err)
	require.EqualValues(t, 100, row.CompletedThrough)
	require.EqualValues(t, 3, row.PendingMessages)
	require.EqualValues(t, 2, row.InflightCount)
	full = mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	full.Publication = mqttPublication(108)
	fourth := writeMQTTWindow(t, s.db, full)
	require.Equal(t, MQTTWindowApplied, fourth.Status)
	require.EqualValues(t, 4, fourth.DeliveryOrder)
	ack(exchanges[0])
	row, _, err = s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, key)
	require.NoError(t, err)
	require.EqualValues(t, 103, row.CompletedThrough)
	ack(fourth)
	row, _, err = s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, key)
	require.NoError(t, err)
	require.EqualValues(t, 103, row.CompletedThrough)
	ack(exchanges[2])
	row, _, err = s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, key)
	require.NoError(t, err)
	require.EqualValues(t, 108, row.CompletedThrough)
	require.Zero(t, row.PendingMessages)
	require.Zero(t, row.InflightCount)
	require.Zero(t, row.HeadPacketID)
	advance := mqttWindowMutation(t, s.db, MQTTWindowAdvance)
	advance.Through = 110
	require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, advance).Status)
	row, _, err = s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, key)
	require.NoError(t, err)
	require.EqualValues(t, 110, row.CompletedThrough)
	session, _, err := s.db.HashSlot(7).GetMQTTSession(ctx, "main", "client")
	require.NoError(t, err)
	require.Zero(t, session.PendingMessages)
	require.Zero(t, session.PendingBytes)
	require.Zero(t, session.OutboundInflight)
}

func TestMQTTWindowTakeoverFencesACKAndPreservesFrozenPublication(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	prepareMQTTWindow(t, s.db, 64, 2, 1)
	m := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	m.Publication = mqttPublication(101)
	first := writeMQTTWindow(t, s.db, m)
	require.Equal(t, MQTTWindowApplied, first.Status)
	m = mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	m.Publication = mqttPublication(103)
	second := writeMQTTWindow(t, s.db, m)
	require.Equal(t, MQTTWindowApplied, second.Status)
	stale := mqttWindowMutation(t, s.db, MQTTWindowAck)
	stale.PacketID, stale.DeliveryOrder = first.PacketID, first.DeliveryOrder
	before, found, err := s.db.HashSlot(7).GetMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, first.PacketID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, mqttPublication(101), before.Publication)
	session, _, err := s.db.HashSlot(7).GetMQTTSession(ctx, "main", "client")
	require.NoError(t, err)
	session.Revision++
	session.OwnerGeneration++
	session.OwnerNodeID++
	session.OwnerBootID = "new-boot"
	session.ConnectionID++
	session.ReceiveMaximum = 1
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, session, session.Revision-1).Status)
	stale.ExpectedRevision = session.Revision
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, stale).Status)
	wrong := mqttWindowMutation(t, s.db, MQTTWindowAck)
	wrong.PacketID, wrong.DeliveryOrder = first.PacketID, second.DeliveryOrder
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, wrong).Status)
	next := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	next.Publication = mqttPublication(104)
	require.Equal(t, MQTTWindowFull, writeMQTTWindow(t, s.db, next).Status, "reconnect keeps rows but honors the new smaller receive limit")
	sub := mqttSubscriptionFixture()
	sub.Stage, sub.RecoveryAtMS, sub.SubscriptionIdentifier = MQTTSubscriptionActive, 0, 999
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(session, sub)).Status)
	after, _, err := s.db.HashSlot(7).GetMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, first.PacketID)
	require.NoError(t, err)
	require.Equal(t, before, after, "existing publication and subscription identifier remain frozen")
	session.Revision++
	sub.Stage, sub.RecoveryAtMS = MQTTSubscriptionRemoving, 3000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(session, sub)).Status)
	for _, exchange := range []MQTTWindowResult{first, second} {
		ack := mqttWindowMutation(t, s.db, MQTTWindowAck)
		ack.PacketID, ack.DeliveryOrder = exchange.PacketID, exchange.DeliveryOrder
		require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, ack).Status, "ordinary unsubscribe retains already started exchanges")
	}
	next = mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	next.Publication = mqttPublication(104)
	next.Publication.SubscriptionIdentifier = 999
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, next).Status)
}

func TestMQTTInflightFormatAndRequestBounds(t *testing.T) {
	row := MQTTInflight{Key: mqttCursorFixture(), Direction: MQTTOutbound, PacketID: 77, DeliveryOrder: 10,
		Publication: mqttPublication(101), QoS: 1, Stage: MQTTInflightAwaitPUBACK, Topic: mqttSubscriptionFixture().Topic, UpdatedAtMS: 2000}
	for _, change := range []func(*MQTTInflight){
		func(r *MQTTInflight) { r.Direction = 2 }, func(r *MQTTInflight) { r.PacketID = 0 },
		func(r *MQTTInflight) { r.DeliveryOrder = 0 }, func(r *MQTTInflight) { r.Publication.Position = 0 },
		func(r *MQTTInflight) { r.Publication.MessageID = 0 }, func(r *MQTTInflight) { r.Publication.MessageSeq = 0 },
		func(r *MQTTInflight) { r.Publication.ContentVersion = 0 }, func(r *MQTTInflight) { r.Publication.ContentHash = "bad" },
		func(r *MQTTInflight) { r.Publication.SubscriptionIdentifier = 268435456 },
		func(r *MQTTInflight) { r.QoS = 2 }, func(r *MQTTInflight) { r.Stage = 2 }, func(r *MQTTInflight) { r.Topic = "" },
		func(r *MQTTInflight) { r.PrevPacketID = 77 }, func(r *MQTTInflight) { r.NextPacketID = 77 },
		func(r *MQTTInflight) { r.PrevPacketID = 2; r.NextPacketID = 2 }, func(r *MQTTInflight) { r.UpdatedAtMS = 0 },
	} {
		bad := row
		change(&bad)
		require.ErrorIs(t, ValidateMQTTInflight(bad), dberrors.ErrInvalidArgument)
	}
	pk := mqttInflightTable.spec.Primary.Key(row)
	key, err := mqttInflightTable.primaryRowKey(7, pk)
	require.NoError(t, err)
	value, err := mqttInflightTable.encodeValue(key, row)
	require.NoError(t, err)
	got, err := mqttInflightTable.decodeValue(key, pk, value)
	require.NoError(t, err)
	require.Equal(t, row, got)
	for i := 0; i < len(value); i++ {
		_, err = mqttInflightTable.decodeValue(key, pk, value[:i])
		require.Error(t, err)
	}
	other := append([]byte(nil), key...)
	other[2] ^= 1
	_, err = mqttInflightTable.decodeValue(other, pk, value)
	require.ErrorIs(t, err, dberrors.ErrChecksumMismatch)
	env, err := rowcodec.Unwrap(key, value)
	require.NoError(t, err)
	for _, bad := range [][]byte{
		rowcodec.Wrap(key, 2, env.Codec, env.Flags, env.Payload), rowcodec.Wrap(key, 1, rowcodec.CodecRaw, env.Flags, env.Payload),
		rowcodec.Wrap(key, 1, env.Codec, 0, env.Payload), rowcodec.Wrap(key, 1, env.Codec, env.Flags, nil),
	} {
		_, err = mqttInflightTable.decodeValue(key, pk, bad)
		require.Error(t, err)
	}
	future := append(append([]byte(nil), env.Payload...), 0x16, 7) // optional column 24
	got, err = mqttInflightTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, future))
	require.NoError(t, err)
	require.Equal(t, row, got)
	base := MQTTWindowMutation{Key: mqttCursorFixture(), ExpectedRevision: 1, OwnerGeneration: 1, OwnerNodeID: 2, OwnerBootID: "boot-a", ConnectionID: 17,
		Op: MQTTWindowAdmit, Publication: mqttPublication(101), UpdatedAtMS: 2000}
	for _, change := range []func(*MQTTWindowMutation){
		func(m *MQTTWindowMutation) { m.Op = 0 }, func(m *MQTTWindowMutation) { m.ExpectedRevision = 0 },
		func(m *MQTTWindowMutation) { m.ExpectedRevision = ^uint64(0) }, func(m *MQTTWindowMutation) { m.OwnerGeneration = 0 },
		func(m *MQTTWindowMutation) { m.OwnerNodeID = 0 }, func(m *MQTTWindowMutation) { m.OwnerBootID = "" },
		func(m *MQTTWindowMutation) { m.ConnectionID = 0 }, func(m *MQTTWindowMutation) { m.PacketID = 1 },
		func(m *MQTTWindowMutation) { m.ReleasedMessages = 1 }, func(m *MQTTWindowMutation) { m.Through = 105 },
		func(m *MQTTWindowMutation) { m.Op = MQTTWindowAck }, func(m *MQTTWindowMutation) { m.UpdatedAtMS = 0 },
	} {
		bad := base
		change(&bad)
		require.ErrorIs(t, ValidateMQTTWindowMutation(bad), dberrors.ErrInvalidArgument)
	}
}

func TestMQTTWindowAdvanceCannotReleaseOutstandingExchanges(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	key := prepareMQTTWindow(t, s.db, 64, 64, 1)
	m := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	m.Publication = mqttPublication(101)
	first := writeMQTTWindow(t, s.db, m)
	require.Equal(t, MQTTWindowApplied, first.Status)
	advance := mqttWindowMutation(t, s.db, MQTTWindowAdvance)
	advance.Through = 110
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, advance).Status, "unadmitted obligations cannot disappear behind the window frontier")
	advance.ReleasedMessages, advance.ReleasedBytes = 4, 400
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, advance).Status, "the outstanding exchange cannot be released as an unsent range")
	advance.ReleasedMessages, advance.ReleasedBytes = 3, 300
	require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, advance).Status)
	row, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), key)
	require.NoError(t, err)
	require.EqualValues(t, 100, row.CompletedThrough)
	require.EqualValues(t, 1, row.PendingMessages)
	require.EqualValues(t, 110, row.WindowThrough)
	ack := mqttWindowMutation(t, s.db, MQTTWindowAck)
	ack.PacketID, ack.DeliveryOrder = first.PacketID, first.DeliveryOrder
	require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, ack).Status)
	row, _, err = s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), key)
	require.NoError(t, err)
	require.EqualValues(t, 110, row.CompletedThrough)
	require.Zero(t, row.PendingBytes)
}

func TestMQTTWindowBatchVisibilityRejectedAdmissionAndRollback(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	key := prepareMQTTWindow(t, s.db, 64, 64, 1)
	m := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	m.Publication = mqttPublication(110)
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, m).Status, "unadmitted messages cannot fit behind this proposed frontier")
	b := s.db.NewBatch()
	defer b.Close()
	m.Publication = mqttPublication(101)
	first, err := b.MutateMQTTWindow(7, m)
	require.NoError(t, err)
	m.ExpectedRevision++
	m.Publication = mqttPublication(103)
	second, err := b.MutateMQTTWindow(7, m)
	require.NoError(t, err)
	m.ExpectedRevision++
	m.Op = MQTTWindowAck
	m.Publication = MQTTInflightPublication{}
	m.PacketID, m.DeliveryOrder = 1, 1
	ack, err := b.MutateMQTTWindow(7, m)
	require.NoError(t, err)
	require.NoError(t, b.SetSlotAppliedIndex(1, 100))
	require.NoError(t, b.Commit(ctx))
	require.Equal(t, MQTTWindowApplied, first.Status)
	require.EqualValues(t, 1, first.PacketID)
	require.Equal(t, MQTTWindowApplied, second.Status)
	require.EqualValues(t, 2, second.PacketID)
	require.Equal(t, MQTTWindowApplied, ack.Status)
	cursor, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, key)
	require.NoError(t, err)
	require.EqualValues(t, 102, cursor.CompletedThrough)
	entry, _, err := s.db.HashSlot(7).GetMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, 2)
	require.NoError(t, err)
	b = s.db.NewBatch()
	defer b.Close()
	m = mqttWindowMutation(t, s.db, MQTTWindowAck)
	m.PacketID, m.DeliveryOrder = 2, 2
	_, err = b.MutateMQTTWindow(7, m)
	require.NoError(t, err)
	require.NoError(t, b.CreateUser(7, User{UID: "duplicate"}))
	require.NoError(t, b.CreateUser(7, User{UID: "duplicate"}))
	require.NoError(t, b.SetSlotAppliedIndex(1, 101))
	require.ErrorIs(t, b.Commit(ctx), dberrors.ErrAlreadyExists)
	got, found, err := s.db.HashSlot(7).GetMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, entry, got)
	gotCursor, _, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(ctx, key)
	require.NoError(t, err)
	require.Equal(t, cursor, gotCursor)
	index, err := s.db.SlotAppliedIndex(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 100, index)
}

func TestMQTTInflightSendOrderSnapshotAndIndexRemoval(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	prepareMQTTWindow(t, s.db, 64, 64, 65535)
	for _, position := range []uint64{101, 103, 104} {
		m := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
		m.Publication = mqttPublication(position)
		require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, m).Status)
	}
	var after MQTTInflightCursor
	var rows []MQTTInflight
	for range 4 {
		page, next, done, err := s.db.HashSlot(7).ListMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, after, 1)
		require.NoError(t, err)
		rows = append(rows, page...)
		if done {
			break
		}
		require.NotEqual(t, after, next)
		after = next
	}
	require.Len(t, rows, 3)
	require.Equal(t, []uint16{65535, 1, 2}, []uint16{rows[0].PacketID, rows[1].PacketID, rows[2].PacketID})
	for _, limit := range []int{-1, 0, 257} {
		_, _, _, err := s.db.HashSlot(7).ListMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, MQTTInflightCursor{}, limit)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	_, _, _, err := s.db.HashSlot(7).ListMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, MQTTInflightCursor{DeliveryOrder: 1}, 1)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	reader, err := s.db.OpenBackupHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, err)
	defer reader.Close()
	ack := mqttWindowMutation(t, s.db, MQTTWindowAck)
	ack.PacketID, ack.DeliveryOrder = 1, 2
	require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, ack).Status)
	current, _, done, err := s.db.HashSlot(7).ListMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, MQTTInflightCursor{}, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Len(t, current, 2)
	require.EqualValues(t, 2, current[1].PacketID)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	_, err = VerifyBackupHashSlotSnapshotReader(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)), false))
	restored, _, done, err := target.db.HashSlot(7).ListMQTTInflight(ctx, "main", "client", 1, MQTTOutbound, MQTTInflightCursor{}, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, rows, restored)
	aggregate, _, err := target.db.HashSlot(7).GetMQTTSession(ctx, "main", "client")
	require.NoError(t, err)
	require.EqualValues(t, 3, aggregate.OutboundInflight)
	inspection, err := InspectScan(ctx, target.db, InspectScanRequest{Table: "mqtt_inflight", HashSlot: 7, HashSlotSet: true, Limit: 10})
	require.NoError(t, err)
	require.Len(t, inspection.Rows, 3)
	require.Equal(t, strings.Repeat("a", 64), inspection.Rows[0]["content_hash"])
}
