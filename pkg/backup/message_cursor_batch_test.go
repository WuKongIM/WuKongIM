package backup_test

import (
	"strings"
	"testing"

	backup "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestMessageCursorBatchRoundTripKeepsPayloadOutOfCursorArtifact(t *testing.T) {
	previousID := strings.Repeat("a", 64)
	body, err := backup.MarshalMessageCursorBatch(backup.MessageCursorBatch{
		HashSlot: 17, Generation: "slot-generation-1", Sequence: 2,
		Previous: &backup.SegmentReference{
			SegmentID: previousID, CommitKey: "segments/" + previousID + "/commit.json",
			CommitSHA256: strings.Repeat("b", 64), PlaintextBytes: 1,
		},
		FromCursor: "channels/a", NextCursor: "channels/z",
		SourceHighWatermark: 9, WatermarkAtUnixMillis: 1_753_400_100_000,
		Boundaries: []backup.ChannelBoundary{
			{ChannelID: "channel-b", ChannelType: 2, Epoch: 3, LogStartOffset: 1, HW: 9},
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 4},
		},
	})
	require.NoError(t, err)
	require.NotContains(t, string(body), "message-payload")

	decoded, err := backup.LoadMessageCursorBatch(body)
	require.NoError(t, err)
	require.Equal(t, uint64(2), decoded.Sequence)
	require.Equal(t, "channel-a", decoded.Boundaries[0].ChannelID)
	require.Equal(t, "channel-b", decoded.Boundaries[1].ChannelID)
	require.Equal(t, previousID, decoded.Previous.SegmentID)
}

func TestMessageCursorBatchRejectsCorruptIndex(t *testing.T) {
	body, err := backup.MarshalMessageCursorBatch(backup.MessageCursorBatch{
		HashSlot: 17, Generation: "slot-generation-1", Sequence: 1,
		NextCursor: "channels/z", SourceHighWatermark: 9,
		WatermarkAtUnixMillis: 1_753_400_100_000,
		Boundaries: []backup.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 9},
		},
	})
	require.NoError(t, err)
	body[len(body)-1] ^= 0xff

	_, err = backup.LoadMessageCursorBatch(body)
	require.Error(t, err)
}
