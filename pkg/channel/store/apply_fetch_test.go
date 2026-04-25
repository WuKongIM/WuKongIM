package store

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestChannelStoreApplyFetchPersistsCommittedIdempotencyForExistingAndNewRecords(t *testing.T) {
	id := channel.ChannelID{ID: "c1", Type: 1}
	st := openTestChannelStore(t, channel.ChannelKey("channel/1/c1"), id)

	_, err := st.Append([]channel.Record{{
		Payload:   mustEncodeApplyFetchMessagePayload(t, 11, "u1", "m1", "one"),
		SizeBytes: 1,
	}})
	require.NoError(t, err)

	_, err = st.StoreApplyFetch(channel.ApplyFetchStoreRequest{
		Records: []channel.Record{{
			Payload:   mustEncodeApplyFetchMessagePayload(t, 12, "u1", "m2", "two"),
			SizeBytes: 1,
		}},
		Checkpoint: &channel.Checkpoint{Epoch: 3, HW: 2},
	})
	require.NoError(t, err)

	entry, ok, err := st.GetIdempotency(channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "u1",
		ClientMsgNo: "m1",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(11), entry.MessageID)
	require.Equal(t, uint64(1), entry.MessageSeq)
	require.Equal(t, uint64(0), entry.Offset)

	current, ok, err := st.GetIdempotency(channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "u1",
		ClientMsgNo: "m2",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(12), current.MessageID)
	require.Equal(t, uint64(2), current.MessageSeq)
	require.Equal(t, uint64(1), current.Offset)
}

func TestStoreApplyFetchKeepsStructuredIdempotencyAcrossExplicitPreviousCommitHW(t *testing.T) {
	id := channel.ChannelID{ID: "c1", Type: 1}
	st := openTestChannelStore(t, channel.ChannelKey("channel/1/c1"), id)

	_, err := st.Append([]channel.Record{{
		Payload:   mustEncodeApplyFetchMessagePayload(t, 11, "u1", "m1", "one"),
		SizeBytes: 1,
	}})
	require.NoError(t, err)

	_, err = st.StoreApplyFetch(channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 1,
		Records: []channel.Record{{
			Payload:   mustEncodeApplyFetchMessagePayload(t, 12, "u1", "m2", "two"),
			SizeBytes: 1,
		}},
		Checkpoint: &channel.Checkpoint{Epoch: 3, HW: 2},
	})
	require.NoError(t, err)

	entry, ok, err := st.GetIdempotency(channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "u1",
		ClientMsgNo: "m1",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(11), entry.MessageID)
	require.Equal(t, uint64(1), entry.MessageSeq)
	require.Equal(t, uint64(0), entry.Offset)

	current, ok, err := st.GetIdempotency(channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "u1",
		ClientMsgNo: "m2",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(12), current.MessageID)
	require.Equal(t, uint64(2), current.MessageSeq)
	require.Equal(t, uint64(1), current.Offset)
}

func TestStoreApplyFetchPreservesLeaderSequenceAfterTruncate(t *testing.T) {
	id := channel.ChannelID{ID: "c1", Type: 1}
	st := openTestChannelStore(t, channel.ChannelKey("channel/1/c1"), id)

	initial := []channel.Record{
		{Payload: mustEncodeStoreMessage(t, channel.Message{MessageID: 21, ClientMsgNo: "m1", FromUID: "u1", ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("one")}), SizeBytes: 1},
		{Payload: mustEncodeStoreMessage(t, channel.Message{MessageID: 22, ClientMsgNo: "m2", FromUID: "u1", ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("two")}), SizeBytes: 1},
		{Payload: mustEncodeStoreMessage(t, channel.Message{MessageID: 23, ClientMsgNo: "m3", FromUID: "u1", ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("three")}), SizeBytes: 1},
	}
	_, err := st.Append(initial)
	require.NoError(t, err)

	require.NoError(t, st.Truncate(1))

	nextLEO, err := st.StoreApplyFetch(channel.ApplyFetchStoreRequest{
		Records: []channel.Record{
			{Payload: mustEncodeStoreMessage(t, channel.Message{MessageID: 24, ClientMsgNo: "m4", FromUID: "u1", ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("four")}), SizeBytes: 1},
			{Payload: mustEncodeStoreMessage(t, channel.Message{MessageID: 25, ClientMsgNo: "m5", FromUID: "u1", ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("five")}), SizeBytes: 1},
		},
		PreviousCommittedHW: 1,
		Checkpoint:          &channel.Checkpoint{Epoch: 4, HW: 3},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(3), nextLEO)

	second, ok, err := st.GetMessageBySeq(2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(24), second.MessageID)

	third, ok, err := st.GetMessageBySeq(3)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(25), third.MessageID)
}

func mustEncodeApplyFetchMessagePayload(t *testing.T, messageID uint64, fromUID, clientMsgNo, body string) []byte {
	t.Helper()

	var payload bytes.Buffer
	require.NoError(t, payload.WriteByte(channel.DurableMessageCodecVersion))
	require.NoError(t, binary.Write(&payload, binary.BigEndian, messageID))
	payload.Write(make([]byte, channel.DurableMessageHeaderSize-9)) // header fields not used by StoreApplyFetch parsing.
	writeSizedBytes(t, &payload, nil)                               // msgKey
	writeSizedBytes(t, &payload, []byte(clientMsgNo))               // clientMsgNo
	writeSizedBytes(t, &payload, nil)                               // streamNo
	writeSizedBytes(t, &payload, nil)                               // channelID
	writeSizedBytes(t, &payload, nil)                               // topic
	writeSizedBytes(t, &payload, []byte(fromUID))                   // fromUID
	writeSizedBytes(t, &payload, []byte(body))                      // payload
	return payload.Bytes()
}

func writeSizedBytes(t *testing.T, buf *bytes.Buffer, value []byte) {
	t.Helper()
	require.NoError(t, binary.Write(buf, binary.BigEndian, uint32(len(value))))
	_, err := buf.Write(value)
	require.NoError(t, err)
}
