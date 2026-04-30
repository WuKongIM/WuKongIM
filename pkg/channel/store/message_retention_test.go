package store

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
	"github.com/stretchr/testify/require"
)

const (
	testRetentionStateVersion  byte   = 1
	testRetentionStateSystemID uint16 = 5
)

type testRetentionState struct {
	LocalRetentionThroughSeq    uint64
	PhysicalRetentionThroughSeq uint64
	RetainedMaxSeq              uint64
}

func TestChannelStoreScanExpiredMessagePrefixStopsAtFirstUnexpired(t *testing.T) {
	st := newTestChannelStore(t)
	appendRetentionMessages(t, st, []retentionTestMessage{
		{MessageID: 11, ClientMsgNo: "expired-1", Timestamp: 90},
		{MessageID: 12, ClientMsgNo: "expired-2", Timestamp: 100},
		{MessageID: 13, ClientMsgNo: "fresh-3", Timestamp: 101},
	})

	result, err := st.ScanExpiredMessagePrefix(0, time.Unix(100, 0), 10)
	require.NoError(t, err)
	require.Equal(t, RetentionScanResult{FromSeq: 1, ThroughSeq: 2, Count: 2}, result)
}

func TestChannelStoreScanExpiredMessagePrefixStopsAtZeroTimestamp(t *testing.T) {
	st := newTestChannelStore(t)
	appendRetentionMessages(t, st, []retentionTestMessage{
		{MessageID: 21, ClientMsgNo: "expired-1", Timestamp: 90},
		{MessageID: 22, ClientMsgNo: "zero-2", Timestamp: 0},
		{MessageID: 23, ClientMsgNo: "expired-3", Timestamp: 80},
	})

	result, err := st.ScanExpiredMessagePrefix(1, time.Unix(100, 0), 10)
	require.NoError(t, err)
	require.Equal(t, RetentionScanResult{FromSeq: 1, ThroughSeq: 1, Count: 1}, result)
}

func TestChannelStoreScanExpiredMessagePrefixHonorsLimit(t *testing.T) {
	st := newTestChannelStore(t)
	appendRetentionMessages(t, st, []retentionTestMessage{
		{MessageID: 31, ClientMsgNo: "expired-1", Timestamp: 90},
		{MessageID: 32, ClientMsgNo: "expired-2", Timestamp: 91},
		{MessageID: 33, ClientMsgNo: "expired-3", Timestamp: 92},
		{MessageID: 34, ClientMsgNo: "expired-4", Timestamp: 93},
	})

	result, err := st.ScanExpiredMessagePrefix(1, time.Unix(100, 0), 2)
	require.NoError(t, err)
	require.Equal(t, RetentionScanResult{FromSeq: 1, ThroughSeq: 2, Count: 2}, result)
}

func TestChannelStoreTrimMessagesThroughDeletesRowsPayloadAndIndexes(t *testing.T) {
	st := newTestChannelStore(t)
	appendRetentionMessages(t, st, []retentionTestMessage{
		{MessageID: 101, ClientMsgNo: "trimmed-client-1", Timestamp: 90},
		{MessageID: 102, ClientMsgNo: "trimmed-client-2", Timestamp: 91},
		{MessageID: 103, ClientMsgNo: "kept-client-3", Timestamp: 92},
	})
	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 2, "committed"))

	require.NoError(t, st.TrimMessagesThrough(context.Background(), 2))

	_, ok, err := st.GetMessageBySeq(1)
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = st.GetMessageByMessageID(101)
	require.NoError(t, err)
	require.False(t, ok)
	_, _, ok, err = st.LookupIdempotency(channel.IdempotencyKey{
		ChannelID:   st.id,
		FromUID:     "u1",
		ClientMsgNo: "trimmed-client-1",
	})
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = getDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, 1, messagePayloadFamilyID))
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = getStoredClientMsgNoIndexSeq(t, st, "trimmed-client-1", 1)
	require.NoError(t, err)
	require.False(t, ok)

	state, err := st.LoadRetentionState()
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(3), st.LEO())
}

func TestChannelStoreTrimMessagesThroughIsIdempotent(t *testing.T) {
	st := newTestChannelStore(t)
	appendRetentionMessages(t, st, []retentionTestMessage{
		{MessageID: 111, ClientMsgNo: "trimmed-client-1", Timestamp: 90},
		{MessageID: 112, ClientMsgNo: "trimmed-client-2", Timestamp: 91},
	})
	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 2, "committed"))

	require.NoError(t, st.TrimMessagesThrough(context.Background(), 2))
	require.NoError(t, st.TrimMessagesThrough(context.Background(), 2))

	_, ok, err := st.GetMessageBySeq(1)
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = st.GetMessageBySeq(2)
	require.NoError(t, err)
	require.False(t, ok)

	state, err := st.LoadRetentionState()
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(2), st.LEO())
}

func TestChannelStoreTrimMessagesThroughDoesNotDeleteAboveBoundary(t *testing.T) {
	st := newTestChannelStore(t)
	appendRetentionMessages(t, st, []retentionTestMessage{
		{MessageID: 121, ClientMsgNo: "trimmed-client-1", Timestamp: 90},
		{MessageID: 122, ClientMsgNo: "trimmed-client-2", Timestamp: 91},
		{MessageID: 123, ClientMsgNo: "kept-client-3", Timestamp: 92},
	})
	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 2, "committed"))

	require.NoError(t, st.TrimMessagesThrough(context.Background(), 2))

	msg, ok, err := st.GetMessageBySeq(3)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(123), msg.MessageID)
	msg, ok, err = st.GetMessageByMessageID(123)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(3), msg.MessageSeq)
	_, _, ok, err = st.LookupIdempotency(channel.IdempotencyKey{
		ChannelID:   st.id,
		FromUID:     "u1",
		ClientMsgNo: "kept-client-3",
	})
	require.NoError(t, err)
	require.True(t, ok)
}

func TestChannelStoreTrimMessagesThroughPreservesLEOWhenAllRowsDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	key, id := testChannelStoreIdentity("trim-preserve-leo")

	engine, err := Open(path)
	require.NoError(t, err)
	st := engine.ForChannel(key, id)
	appendRetentionMessages(t, st, []retentionTestMessage{
		{MessageID: 131, ClientMsgNo: "trimmed-client-1", Timestamp: 90},
		{MessageID: 132, ClientMsgNo: "trimmed-client-2", Timestamp: 91},
		{MessageID: 133, ClientMsgNo: "trimmed-client-3", Timestamp: 92},
	})
	require.Equal(t, uint64(3), st.LEO())
	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 3, "committed"))
	require.NoError(t, st.TrimMessagesThrough(context.Background(), 3))
	require.Equal(t, uint64(3), st.LEO())
	require.NoError(t, engine.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reopened.Close())
	}()
	st = reopened.ForChannel(key, id)
	require.Equal(t, uint64(3), st.LEO())

	state, err := st.LoadRetentionState()
	require.NoError(t, err)
	require.Equal(t, uint64(3), state.PhysicalRetentionThroughSeq)
}

func TestChannelStoreRetentionStatePreservesLEOAfterFullPrefixTrim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	key, id := testChannelStoreIdentity("retained")

	engine, err := Open(path)
	require.NoError(t, err)
	st := engine.ForChannel(key, id)
	mustAppendRecords(t, st, []string{"m1", "m2", "m3"})
	require.Equal(t, uint64(3), st.LEO())

	writeRetentionStateForTest(t, st, testRetentionState{
		LocalRetentionThroughSeq: 3,
		RetainedMaxSeq:           3,
	})
	deleteMessageRowsThroughSeqForTest(t, st, 3)
	require.NoError(t, engine.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reopened.Close())
	}()

	require.Equal(t, uint64(3), reopened.ForChannel(key, id).LEO())
}

func TestEngineListChannelKeysIncludesFullyTrimmedRetainedChannel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	key, id := testChannelStoreIdentity("retained-list")

	engine, err := Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, engine.Close())
	}()

	st := engine.ForChannel(key, id)
	mustAppendRecords(t, st, []string{"m1", "m2", "m3"})
	writeRetentionStateForTest(t, st, testRetentionState{
		LocalRetentionThroughSeq: 3,
		RetainedMaxSeq:           3,
	})
	deleteMessageRowsThroughSeqForTest(t, st, 3)

	keys, err := engine.ListChannelKeys()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{string(key)}, channelKeysToStrings(keys))
}

func TestChannelStoreAdoptRetentionBoundaryRaisesLEOFloorAndCursorWhenLocalLogBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	key, id := testChannelStoreIdentity("retention-adopt")

	engine, err := Open(path)
	require.NoError(t, err)
	st := engine.ForChannel(key, id)

	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 5, "committed"))
	require.NoError(t, engine.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reopened.Close())
	}()
	st = reopened.ForChannel(key, id)

	state, err := st.LoadRetentionState()
	require.NoError(t, err)
	require.Equal(t, uint64(5), state.LocalRetentionThroughSeq)
	require.Zero(t, state.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(5), state.RetainedMaxSeq)
	require.Equal(t, uint64(5), st.LEO())

	seq, ok, err := st.LoadCommittedDispatchCursor("committed")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(5), seq)
}

func TestChannelStoreAdoptRetentionBoundaryDoesNotMoveExistingCursorBackward(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.StoreCommittedDispatchCursor("committed", 9))

	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 5, "committed"))

	state, err := st.LoadRetentionState()
	require.NoError(t, err)
	require.Equal(t, uint64(5), state.LocalRetentionThroughSeq)
	require.Zero(t, state.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(5), state.RetainedMaxSeq)

	seq, ok, err := st.LoadCommittedDispatchCursor("committed")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(9), seq)
}

func TestChannelStoreAdoptRetentionBoundaryFastForwardsCursorToExistingBoundary(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 10, "committed"))
	require.NoError(t, st.StoreCommittedDispatchCursor("lagging", 4))

	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 5, "lagging"))

	state, err := st.LoadRetentionState()
	require.NoError(t, err)
	require.Equal(t, uint64(10), state.LocalRetentionThroughSeq)
	require.Equal(t, uint64(10), state.RetainedMaxSeq)

	seq, ok, err := st.LoadCommittedDispatchCursor("lagging")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(10), seq)
}

func TestChannelStoreRetentionStateTruncateClampsRetainedMaxSeqOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	key, id := testChannelStoreIdentity("retention-truncate")

	engine, err := Open(path)
	require.NoError(t, err)
	st := engine.ForChannel(key, id)
	mustAppendRecords(t, st, []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m10"})
	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 5, "committed"))

	require.NoError(t, st.Truncate(7))
	require.NoError(t, engine.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reopened.Close())
	}()
	st = reopened.ForChannel(key, id)
	require.Equal(t, uint64(7), st.LEO())
	state, err := st.LoadRetentionState()
	require.NoError(t, err)
	require.Equal(t, uint64(5), state.LocalRetentionThroughSeq)
	require.Equal(t, uint64(7), state.RetainedMaxSeq)
}

func TestChannelStoreRetentionStateTruncateRejectsBelowLocalRetention(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 5, "committed"))

	err := st.Truncate(4)
	require.ErrorIs(t, err, channel.ErrCorruptState)

	state, loadErr := st.LoadRetentionState()
	require.NoError(t, loadErr)
	require.Equal(t, uint64(5), state.LocalRetentionThroughSeq)
	require.Equal(t, uint64(5), state.RetainedMaxSeq)
	require.Equal(t, uint64(5), st.LEO())
}

func TestChannelStoreRetentionStateTruncateLogAndHistoryClampsRetainedMaxSeqOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	key, id := testChannelStoreIdentity("retention-truncate-history")

	engine, err := Open(path)
	require.NoError(t, err)
	st := engine.ForChannel(key, id)
	mustAppendRecords(t, st, []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m10"})
	require.NoError(t, st.AdoptRetentionBoundary(context.Background(), 5, "committed"))

	require.NoError(t, st.TruncateLogAndHistory(context.Background(), 7))
	require.NoError(t, engine.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reopened.Close())
	}()
	st = reopened.ForChannel(key, id)
	require.Equal(t, uint64(7), st.LEO())
	state, err := st.LoadRetentionState()
	require.NoError(t, err)
	require.Equal(t, uint64(5), state.LocalRetentionThroughSeq)
	require.Equal(t, uint64(7), state.RetainedMaxSeq)
}

func TestChannelStoreLEOWithErrorRejectsCorruptRetentionState(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.engine.db.Set(encodeRetentionStateKey(st.key), []byte{retentionStateVersion, 1}, pebble.Sync))

	_, err := st.LEOWithError()
	require.ErrorIs(t, err, channel.ErrCorruptValue)
}

func TestDecodeRetentionStateRejectsImpossibleState(t *testing.T) {
	value := encodeRetentionState(retentionState{
		LocalRetentionThroughSeq:    5,
		PhysicalRetentionThroughSeq: 6,
		RetainedMaxSeq:              5,
	})

	_, err := decodeRetentionState(value)
	require.ErrorIs(t, err, channel.ErrCorruptValue)

	value = encodeRetentionState(retentionState{
		LocalRetentionThroughSeq: 5,
		RetainedMaxSeq:           4,
	})
	_, err = decodeRetentionState(value)
	require.ErrorIs(t, err, channel.ErrCorruptValue)

	value = encodeRetentionState(retentionState{
		RetainedMaxSeq: 1,
	})
	_, err = decodeRetentionState(value)
	require.ErrorIs(t, err, channel.ErrCorruptValue)
}

func writeRetentionStateForTest(tb testing.TB, st *ChannelStore, state testRetentionState) {
	tb.Helper()

	value := make([]byte, 0, 1+24)
	value = append(value, testRetentionStateVersion)
	value = binary.BigEndian.AppendUint64(value, state.LocalRetentionThroughSeq)
	value = binary.BigEndian.AppendUint64(value, state.PhysicalRetentionThroughSeq)
	value = binary.BigEndian.AppendUint64(value, state.RetainedMaxSeq)
	key := encodeTableSystemKey(st.key, channelSystemTableID, testRetentionStateSystemID)
	require.NoError(tb, st.engine.db.Set(key, value, pebble.Sync))
}

func deleteMessageRowsThroughSeqForTest(tb testing.TB, st *ChannelStore, throughSeq uint64) {
	tb.Helper()

	batch := st.engine.db.NewBatch()
	defer batch.Close()

	for seq := uint64(1); seq <= throughSeq; seq++ {
		row, ok, err := st.messageTable().getBySeq(seq)
		require.NoError(tb, err)
		if !ok {
			continue
		}
		require.NoError(tb, batch.Delete(encodeTableStateKey(st.key, TableIDMessage, row.MessageSeq, messagePrimaryFamilyID), pebble.NoSync))
		require.NoError(tb, batch.Delete(encodeTableStateKey(st.key, TableIDMessage, row.MessageSeq, messagePayloadFamilyID), pebble.NoSync))
		require.NoError(tb, batch.Delete(encodeMessageIDIndexKey(st.key, row.MessageID), pebble.NoSync))
		if row.ClientMsgNo != "" {
			require.NoError(tb, batch.Delete(encodeMessageClientMsgNoIndexKey(st.key, row.ClientMsgNo, row.MessageSeq), pebble.NoSync))
		}
		if row.FromUID != "" && row.ClientMsgNo != "" {
			require.NoError(tb, batch.Delete(encodeMessageIdempotencyIndexKey(st.key, row.FromUID, row.ClientMsgNo), pebble.NoSync))
		}
	}
	require.NoError(tb, batch.Commit(pebble.Sync))
}

type retentionTestMessage struct {
	MessageID   uint64
	ClientMsgNo string
	Timestamp   int32
}

func appendRetentionMessages(tb testing.TB, st *ChannelStore, messages []retentionTestMessage) {
	tb.Helper()

	records := make([]channel.Record, 0, len(messages))
	for _, msg := range messages {
		encoded := mustEncodeStoreMessage(tb, channel.Message{
			MessageID:   msg.MessageID,
			ClientMsgNo: msg.ClientMsgNo,
			FromUID:     "u1",
			Timestamp:   msg.Timestamp,
			ChannelID:   st.id.ID,
			ChannelType: st.id.Type,
			Payload:     []byte(msg.ClientMsgNo),
		})
		records = append(records, channel.Record{Payload: encoded, SizeBytes: len(encoded)})
	}
	_, err := st.Append(records)
	require.NoError(tb, err)
}

func channelKeysToStrings(keys []channel.ChannelKey) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, string(key))
	}
	return out
}
