package store

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestChannelStoreGetMessageBySeqFailsOnOrphanPayloadFamily(t *testing.T) {
	st := newTestChannelStore(t)
	row := messageRowFromChannelMessage(channel.Message{
		MessageID:   41,
		ClientMsgNo: "orphan",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	row.MessageSeq = 1

	_, payloadFamily, err := encodeMessageFamilies(row)
	require.NoError(t, err)
	mustSetDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, row.MessageSeq, messagePayloadFamilyID), payloadFamily)

	_, ok, err := st.GetMessageBySeq(row.MessageSeq)
	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.False(t, ok)
}

func TestChannelStoreListMessagesBySeqFailsOnPayloadHashMismatch(t *testing.T) {
	st := newTestChannelStore(t)
	msg := channel.Message{
		MessageID:   42,
		ClientMsgNo: "hash",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	}
	payload := mustEncodeStoreMessage(t, msg)
	_, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
	require.NoError(t, err)

	tampered := messageRowFromChannelMessage(msg)
	tampered.MessageSeq = 1
	tampered.Payload = []byte("tampered")
	tampered.PayloadHash = hashMessagePayload(msg.Payload)

	_, tamperedPayloadFamily, err := encodeMessageFamilies(tampered)
	require.NoError(t, err)
	mustSetDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, tampered.MessageSeq, messagePayloadFamilyID), tamperedPayloadFamily)

	_, err = st.ListMessagesBySeq(1, 10, 4096, false)
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreTruncateFailsOnOrphanPayloadFamily(t *testing.T) {
	st := newTestChannelStore(t)
	validPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   46,
		ClientMsgNo: "valid",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	_, err := st.Append([]channel.Record{{Payload: validPayload, SizeBytes: len(validPayload)}})
	require.NoError(t, err)

	orphanRow := messageRowFromChannelMessage(channel.Message{
		MessageID:   47,
		ClientMsgNo: "orphan-truncate",
		FromUID:     "u2",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("two"),
	})
	orphanRow.MessageSeq = 2

	_, payloadFamily, err := encodeMessageFamilies(orphanRow)
	require.NoError(t, err)
	mustSetDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, orphanRow.MessageSeq, messagePayloadFamilyID), payloadFamily)

	err = st.Truncate(0)
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreAppendRejectsDuplicateMessageID(t *testing.T) {
	st := newTestChannelStore(t)
	first := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   43,
		ClientMsgNo: "m1",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	second := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   43,
		ClientMsgNo: "m2",
		FromUID:     "u2",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("two"),
	})

	_, err := st.Append([]channel.Record{{Payload: first, SizeBytes: len(first)}})
	require.NoError(t, err)

	_, err = st.Append([]channel.Record{{Payload: second, SizeBytes: len(second)}})
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreAppendRejectsDuplicateIdempotencyKey(t *testing.T) {
	st := newTestChannelStore(t)
	first := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   44,
		ClientMsgNo: "same",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	second := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   45,
		ClientMsgNo: "same",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("two"),
	})

	_, err := st.Append([]channel.Record{{Payload: first, SizeBytes: len(first)}})
	require.NoError(t, err)

	_, err = st.Append([]channel.Record{{Payload: second, SizeBytes: len(second)}})
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreAppendSkipsOptionalIndexesWhenClientMsgNoEmpty(t *testing.T) {
	st := newTestChannelStore(t)
	payload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   51,
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})

	_, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
	require.NoError(t, err)

	msg, ok, err := st.GetMessageByMessageID(51)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(1), msg.MessageSeq)
	require.Empty(t, msg.ClientMsgNo)

	_, ok, err = getStoredClientMsgNoIndexSeq(t, st, "", 1)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = getIndexedIdempotencyHit(t, st, "u1", "")
	require.NoError(t, err)
	require.False(t, ok)
}
