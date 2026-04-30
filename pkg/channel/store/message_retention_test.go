package store

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"

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

func channelKeysToStrings(keys []channel.ChannelKey) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, string(key))
	}
	return out
}
