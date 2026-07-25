package backup_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestSegmentBatchRoundTripPreservesMessageCursorIndex(t *testing.T) {
	batch := backup.SegmentBatch{
		HashSlot:              17,
		Stream:                backup.SegmentStreamMessages,
		Generation:            "slot-17-generation-1",
		Sequence:              1,
		FromCursor:            "",
		NextCursor:            "channel-page-2",
		SourceHighWatermark:   91,
		WatermarkAtUnixMillis: 1_753_400_100_000,
		Records:               [][]byte{[]byte("channel-a:41"), []byte("channel-b:9")},
		MessageCursors: []backup.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 41},
			{ChannelID: "channel-b", ChannelType: 2, Epoch: 7, HW: 9},
		},
	}

	body, err := backup.MarshalSegmentBatch(batch)
	if err != nil {
		t.Fatalf("MarshalSegmentBatch() error = %v", err)
	}
	decoded, err := backup.LoadSegmentBatch(body)
	if err != nil {
		t.Fatalf("LoadSegmentBatch() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, batch) {
		t.Fatalf("LoadSegmentBatch() = %#v, want %#v", decoded, batch)
	}
}

func TestLoadSegmentBatchRejectsCorruptRecordPayload(t *testing.T) {
	body, err := backup.MarshalSegmentBatch(backup.SegmentBatch{
		HashSlot: 4, Stream: backup.SegmentStreamMetadata,
		Generation: "slot-4-generation-1", Sequence: 1,
		NextCursor: "metadata-page-1", SourceHighWatermark: 7,
		WatermarkAtUnixMillis: 1_753_400_100_000,
		Records:               [][]byte{[]byte("metadata-record")},
	})
	if err != nil {
		t.Fatalf("MarshalSegmentBatch() error = %v", err)
	}
	body[len(body)-1] ^= 0xff

	if _, err := backup.LoadSegmentBatch(body); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("LoadSegmentBatch(corrupt) error = %v, want %v", err, backup.ErrObjectCorrupt)
	}
}
