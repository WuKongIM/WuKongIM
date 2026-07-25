package backup_test

import (
	"testing"

	backup "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestMessageLogRecordRoundTrip(t *testing.T) {
	input := backup.MessageLogRecord{
		Kind: backup.MessageLogRecordMessage, HashSlot: 17,
		ChannelID: "room-a", ChannelType: 2, Epoch: 7,
		LogStartOffset: 3, HW: 9, MessageSeq: 8, MessageID: 99,
		Setting: 3, FromUID: "u1", ClientMsgNo: "c1",
		ServerTimestampMS: 1_753_400_100_000, SyncOnce: true, Payload: []byte("hello"),
	}
	body, err := backup.MarshalMessageLogRecord(input)
	require.NoError(t, err)
	output, err := backup.LoadMessageLogRecord(body)
	require.NoError(t, err)
	require.Equal(t, input, output)
}

func TestMessageLogRecordBoundaryRejectsPayload(t *testing.T) {
	_, err := backup.MarshalMessageLogRecord(backup.MessageLogRecord{
		Kind: backup.MessageLogRecordBoundary, HashSlot: 1,
		ChannelID: "room-a", ChannelType: 2, Epoch: 1, HW: 1,
		Payload: []byte("forbidden"),
	})
	require.Error(t, err)
}
