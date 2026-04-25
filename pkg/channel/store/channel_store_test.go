package store

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestChannelStoreAppendsAndReadsRecords(t *testing.T) {
	st := newTestChannelStore(t)
	payload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   7,
		ClientMsgNo: "c-1",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("a"),
	})
	base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
	require.NoError(t, err)
	require.Equal(t, uint64(0), base)

	records, err := st.Read(0, 1024)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, []byte("a"), mustDecodeStoreMessage(t, records[0].Payload).Payload)
}

func TestChannelStoreExposesStructuredQueryAPIs(t *testing.T) {
	st := newTestChannelStore(t)

	firstPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   31,
		ClientMsgNo: "same",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	secondPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   32,
		ClientMsgNo: "other",
		FromUID:     "u2",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("two"),
	})
	thirdPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   33,
		ClientMsgNo: "same",
		FromUID:     "u3",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("three"),
	})

	_, err := st.Append([]channel.Record{
		{Payload: firstPayload, SizeBytes: len(firstPayload)},
		{Payload: secondPayload, SizeBytes: len(secondPayload)},
		{Payload: thirdPayload, SizeBytes: len(thirdPayload)},
	})
	require.NoError(t, err)

	bySeq, ok, err := callGetMessageBySeq(st, 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(32), bySeq.MessageID)

	byID, ok, err := callGetMessageByMessageID(st, 33)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(3), byID.MessageSeq)

	list, err := callListMessagesBySeq(st, 1, 3, 1024, false)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, []uint64{list[0].MessageSeq, list[1].MessageSeq, list[2].MessageSeq})

	reverse, err := callListMessagesBySeq(st, 3, 2, 1024, true)
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 2}, []uint64{reverse[0].MessageSeq, reverse[1].MessageSeq})

	clientRows, nextBeforeSeq, hasMore, err := callListMessagesByClientMsgNo(st, "same", 0, 1)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Equal(t, uint64(3), nextBeforeSeq)
	require.Len(t, clientRows, 1)
	require.Equal(t, uint64(33), clientRows[0].MessageID)

	idempotency, payloadHash, ok, err := callLookupIdempotency(st, channel.IdempotencyKey{
		ChannelID:   st.id,
		FromUID:     "u3",
		ClientMsgNo: "same",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(33), idempotency.MessageID)
	require.Equal(t, uint64(3), idempotency.MessageSeq)
	require.Equal(t, uint64(2), idempotency.Offset)
	require.Equal(t, hashMessagePayload([]byte("three")), payloadHash)
}

func TestChannelStorePersistsCheckpointAndHistory(t *testing.T) {
	dir := t.TempDir()
	engine, err := Open(dir)
	require.NoError(t, err)

	key := channel.ChannelKey("channel/1/history")
	id := channel.ChannelID{ID: "history", Type: 1}
	st := engine.ForChannel(key, id)

	require.NoError(t, st.StoreCheckpoint(channel.Checkpoint{Epoch: 2, HW: 8}))
	require.NoError(t, st.AppendHistory(channel.EpochPoint{Epoch: 2, StartOffset: 6}))
	require.NoError(t, engine.Close())

	reopened, err := Open(dir)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reopened.Close())
	}()

	reloaded := reopened.ForChannel(key, id)

	cp, err := reloaded.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, uint64(8), cp.HW)

	history, err := reloaded.LoadHistory()
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, channel.EpochPoint{Epoch: 2, StartOffset: 6}, history[0])
}
