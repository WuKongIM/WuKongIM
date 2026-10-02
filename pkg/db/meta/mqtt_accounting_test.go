package meta

import (
	"context"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/stretchr/testify/require"
)

func prepareMQTTQualified(t *testing.T, db *MetaDB) MQTTDeliveryCursorMutation {
	t.Helper()
	s := prepareMQTTCursorSession(t, db)
	sub := mqttSubscriptionFixture()
	sub.Stage = MQTTSubscriptionActive
	sub.RecoveryAtMS = 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, db, mqttSubscriptionMutation(s, sub)).Status)
	s.Revision = 3
	m := mqttCursorMutation(s)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, db, m).Status)
	m.ExpectedRevision = 4
	m.Op = MQTTCursorAccountQualified
	m.Through = 104
	m.AddedMessages = 2
	m.AddedBytes = 80
	m.Qualified = &MQTTQualifiedAccounting{From: 101, SubscriptionRevision: 3, Items: []MQTTAccountingItem{{Position: 101, Bytes: 30}, {Position: 103, Bytes: 50}}}
	return m
}
func readMQTTAccounting(t *testing.T, db *MetaDB, key MQTTDeliveryCursorKey) MQTTReadResult {
	t.Helper()
	r, e := db.ReadMQTTState(context.Background(), 7, MQTTRead{Kind: MQTTReadAccounting, CursorKey: key})
	require.NoError(t, e)
	return r
}
func TestMQTTQualifiedAccountingOwnsRangesAndExactDebits(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	m := prepareMQTTQualified(t, s.db)
	b := s.db.NewBatch()
	defer b.Close()
	r, e := b.MutateMQTTDeliveryCursor(7, m)
	require.NoError(t, e)
	m.Qualified.Items[0].Bytes = 99 // caller mutation must not alter the staged receipt
	require.NoError(t, b.Commit(context.Background()))
	require.Equal(t, MQTTSessionCASApplied, r.Status)
	m.Qualified.Items[0].Bytes = 30
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTCursor(t, s.db, m).Status)
	first := readMQTTAccounting(t, s.db, m.Key)
	require.EqualValues(t, 1, first.DeliveryCursors[0].AccountingVersion)
	require.EqualValues(t, 101, first.DeliveryCursors[0].AccountingHead)
	require.Equal(t, m.Qualified.Items, first.Accounting.Items)
	// An empty page adds coverage but no receipt; later positive coverage links the tail.
	m.ExpectedRevision = 5
	m.Qualified.From = 105
	m.Through = 108
	m.Qualified.Items = nil
	m.AddedMessages = 0
	m.AddedBytes = 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	m.ExpectedRevision = 6
	m.Qualified.From = 109
	m.Through = 112
	m.Qualified.Items = []MQTTAccountingItem{{Position: 111, Bytes: 70}}
	m.AddedMessages = 1
	m.AddedBytes = 70
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	head := readMQTTAccounting(t, s.db, m.Key)
	require.EqualValues(t, 109, head.Accounting.NextFrom)
	require.EqualValues(t, 3, head.Session.PendingMessages)
	wrong := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	wrong.Publication = mqttPublication(103)
	wrong.Publication.Bytes = 50
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, wrong).Status)
	wrong.Publication = mqttPublication(101)
	wrong.Publication.Bytes = 31
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, wrong).Status)
	wrong.Publication.Bytes = 30
	one := writeMQTTWindow(t, s.db, wrong)
	require.Equal(t, MQTTWindowApplied, one.Status)
	advance := mqttWindowMutation(t, s.db, MQTTWindowAdvance)
	advance.Through = 108
	advance.ReleasedMessages = 1
	advance.ReleasedBytes = 49
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, advance).Status)
	advance.ReleasedBytes = 50
	advance.Through = 111 // crossing a second receipt must yield
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, advance).Status)
	advance.Through = 108
	require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, advance).Status)
	now := readMQTTAccounting(t, s.db, m.Key)
	require.EqualValues(t, 109, now.Accounting.From)
	require.EqualValues(t, 100, now.DeliveryCursors[0].CompletedThrough)
	admission := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
	admission.Publication = mqttPublication(111)
	admission.Publication.Bytes = 70
	two := writeMQTTWindow(t, s.db, admission)
	require.Equal(t, MQTTWindowApplied, two.Status)
	now = readMQTTAccounting(t, s.db, m.Key)
	require.Nil(t, now.Accounting)
	require.Zero(t, now.DeliveryCursors[0].AccountingHead)
	require.Zero(t, now.DeliveryCursors[0].AccountingTail)
	for _, x := range []MQTTWindowResult{two, one} {
		ack := mqttWindowMutation(t, s.db, MQTTWindowAck)
		ack.PacketID = x.PacketID
		ack.DeliveryOrder = x.DeliveryOrder
		require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, ack).Status)
	}
	now = readMQTTAccounting(t, s.db, m.Key)
	require.Zero(t, now.Session.PendingMessages)
	require.Zero(t, now.Session.PendingBytes)
	require.EqualValues(t, 111, now.DeliveryCursors[0].CompletedThrough)
	// Linked auxiliary rows are deleted as they are consumed.
	for _, from := range []uint64{101, 109} {
		key, _ := mqttAccountingKey(7, m.Key, from)
		_, found, e := s.db.get(key)
		require.NoError(t, e)
		require.False(t, found)
	}
}

func TestMQTTQualifiedAccountingFencesAndBounds(t *testing.T) {
	for _, mode := range []string{"owner", "revision", "subscription-revision", "from", "wide", "duplicate", "order", "outside", "count", "bytes", "overflow", "legacy-backlog", "legacy-bypass"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			m := prepareMQTTQualified(t, s.db)
			switch mode {
			case "owner":
				m.OwnerGeneration++
			case "revision":
				m.ExpectedRevision++
			case "subscription-revision":
				m.Qualified.SubscriptionRevision++
			case "from":
				m.Qualified.From++
			case "wide":
				m.Through = 357
			case "duplicate":
				m.Qualified.Items[1].Position = 101
			case "order":
				m.Qualified.Items[0].Position = 104
			case "outside":
				m.Qualified.Items[1].Position = 105
			case "count":
				m.AddedMessages++
			case "bytes":
				m.AddedBytes++
			case "overflow":
				m.Qualified.Items[0].Bytes = math.MaxUint64
			case "legacy-backlog":
				legacy := m
				legacy.Op = MQTTCursorAccount
				legacy.Qualified = nil
				require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, legacy).Status)
				m.ExpectedRevision = 5
				m.Qualified.From = 105
				m.Through = 108
				m.Qualified.Items = []MQTTAccountingItem{{Position: 107, Bytes: 80}}
				m.AddedMessages = 1
			case "legacy-bypass":
				require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
				m.ExpectedRevision = 5
				m.Op = MQTTCursorAccount
				m.Qualified = nil
				m.Through = 108
			}
			b := s.db.NewBatch()
			defer b.Close()
			r, e := b.MutateMQTTDeliveryCursor(7, m)
			if e != nil {
				require.ErrorIs(t, e, dberrors.ErrInvalidArgument)
			} else {
				require.NoError(t, b.Commit(context.Background()))
				require.Equal(t, MQTTSessionCASConflict, r.Status)
			}
		})
	}
}

func TestMQTTQualifiedAccountingMissingWitnessAndAtomicRollback(t *testing.T) {
	for _, mode := range []string{"missing-head", "corrupt-head", "missing-tail", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			m := prepareMQTTQualified(t, s.db)
			if mode == "rollback" {
				b := s.db.NewBatch()
				defer b.Close()
				_, e := b.MutateMQTTDeliveryCursor(7, m)
				require.NoError(t, e)
				b.addOp(7, func(context.Context, *batchCommitState, *engine.Batch) error { return dberrors.ErrInvalidArgument })
				require.Error(t, b.Commit(context.Background()))
				r := readMQTTAccounting(t, s.db, m.Key)
				require.Nil(t, r.Accounting)
				require.Zero(t, r.Session.PendingMessages)
				return
			}
			require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
			key, e := mqttAccountingKey(7, m.Key, 101)
			require.NoError(t, e)
			batch := s.db.engine.NewBatch()
			defer batch.Close()
			if mode == "corrupt-head" {
				require.NoError(t, batch.Set(key, []byte("bad")))
			} else {
				require.NoError(t, batch.Delete(key))
			}
			require.NoError(t, batch.Commit(true))
			if mode == "missing-tail" {
				m.ExpectedRevision = 5
				m.Qualified.From = 105
				m.Through = 108
				m.Qualified.Items = []MQTTAccountingItem{{Position: 107, Bytes: 80}}
				m.AddedMessages = 1
				b := s.db.NewBatch()
				defer b.Close()
				_, e := b.MutateMQTTDeliveryCursor(7, m)
				require.NoError(t, e)
				require.ErrorIs(t, b.Commit(context.Background()), dberrors.ErrCorruptValue)
			} else {
				_, e = s.db.ReadMQTTState(context.Background(), 7, MQTTRead{Kind: MQTTReadAccounting, CursorKey: m.Key})
				require.ErrorIs(t, e, dberrors.ErrCorruptValue)
			}
		})
	}
}

func TestMQTTAccountingRangeCodecIsBoundedAndKeyBound(t *testing.T) {
	key, e := mqttAccountingKey(7, mqttCursorFixture(), 101)
	require.NoError(t, e)
	r := MQTTAccountingRange{Key: mqttCursorFixture(), From: 101, Through: 104, SubscriptionRevision: 3, EvaluatedAtMS: 1000, NextFrom: 109, Items: []MQTTAccountingItem{{Position: 101, Bytes: 0}, {Position: 104, Bytes: 80}}}
	encoded, e := encodeMQTTAccountingRange(key, r)
	require.NoError(t, e)
	got, e := decodeMQTTAccountingRange(key, r.Key, 101, encoded)
	require.NoError(t, e)
	require.Equal(t, r, got)
	for _, mode := range []string{"wrong-key", "version", "truncated", "trailing", "count", "link", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			raw := append([]byte(nil), encoded...)
			decodeKey := key
			switch mode {
			case "wrong-key":
				decodeKey = append([]byte(nil), key...)
				decodeKey[0] ^= 1
			case "version":
				raw[0] = 99
			case "truncated":
				raw = raw[:len(raw)-1]
			case "trailing":
				raw = append(raw, 0)
			case "count":
				raw = make([]byte, 5000)
			case "link":
				bad := r
				bad.NextFrom = 104
				_, e := encodeMQTTAccountingRange(key, bad)
				require.Error(t, e)
				return
			case "overflow":
				bad := r
				bad.Items = []MQTTAccountingItem{{Position: 101, Bytes: math.MaxUint64}, {Position: 104, Bytes: 1}}
				_, e := encodeMQTTAccountingRange(key, bad)
				require.Error(t, e)
				return
			}
			_, e := decodeMQTTAccountingRange(decodeKey, r.Key, 101, raw)
			require.Error(t, e)
		})
	}
}
