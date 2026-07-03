package plugin

import (
	"math"
	"testing"

	pluginevents "github.com/WuKongIM/WuKongIM/internal/contracts/pluginevents"
	"github.com/stretchr/testify/require"
)

func TestMessageBatchFromPersistAfterCommitted(t *testing.T) {
	event := pluginevents.PersistAfterCommitted{
		MessageID:         11,
		MessageSeq:        5,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "sender",
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 1713859200123,
		Payload:           []byte("payload"),
	}
	batch := messageBatchFromPersistAfter(event)
	require.Len(t, batch.Messages, 1)
	msg := batch.Messages[0]
	require.Equal(t, int64(11), msg.MessageId)
	require.Equal(t, uint64(5), msg.MessageSeq)
	require.Equal(t, "client-1", msg.ClientMsgNo)
	require.Equal(t, uint32(1713859200), msg.Timestamp)
	require.Equal(t, "sender", msg.From)
	require.Equal(t, "room", msg.ChannelId)
	require.Equal(t, uint32(2), msg.ChannelType)
	require.Equal(t, []byte("payload"), msg.Payload)

	event.Payload[0] = 'P'
	require.Equal(t, []byte("payload"), msg.Payload)
}

func TestMessageBatchFromPersistAfterCommittedConversions(t *testing.T) {
	tests := []struct {
		name              string
		messageID         uint64
		serverTimestampMS int64
		wantMessageID     int64
		wantTimestamp     uint32
	}{
		{
			name:              "message id max int64",
			messageID:         uint64(math.MaxInt64),
			serverTimestampMS: 1713859200123,
			wantMessageID:     math.MaxInt64,
			wantTimestamp:     1713859200,
		},
		{
			name:              "message id saturates above max int64",
			messageID:         uint64(math.MaxInt64) + 1,
			serverTimestampMS: 1713859200123,
			wantMessageID:     math.MaxInt64,
			wantTimestamp:     1713859200,
		},
		{
			name:              "non-positive timestamp becomes zero",
			messageID:         11,
			serverTimestampMS: 0,
			wantMessageID:     11,
			wantTimestamp:     0,
		},
		{
			name:              "subsecond timestamp truncates to zero",
			messageID:         11,
			serverTimestampMS: 999,
			wantMessageID:     11,
			wantTimestamp:     0,
		},
		{
			name:              "timestamp saturates above max uint32 seconds",
			messageID:         11,
			serverTimestampMS: (int64(math.MaxUint32) + 1) * 1000,
			wantMessageID:     11,
			wantTimestamp:     math.MaxUint32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := pluginevents.PersistAfterCommitted{
				MessageID:         tt.messageID,
				MessageSeq:        5,
				ChannelID:         "room",
				ChannelType:       2,
				FromUID:           "sender",
				ClientMsgNo:       "client-1",
				ServerTimestampMS: tt.serverTimestampMS,
				Payload:           []byte("payload"),
			}

			batch := messageBatchFromPersistAfter(event)
			require.Len(t, batch.Messages, 1)
			msg := batch.Messages[0]
			require.Equal(t, tt.wantMessageID, msg.MessageId)
			require.Equal(t, tt.wantTimestamp, msg.Timestamp)
			require.Equal(t, uint64(5), msg.MessageSeq)
			require.Equal(t, "client-1", msg.ClientMsgNo)
			require.Equal(t, "sender", msg.From)
			require.Equal(t, "room", msg.ChannelId)
			require.Equal(t, uint32(2), msg.ChannelType)
			require.Equal(t, []byte("payload"), msg.Payload)
		})
	}
}
