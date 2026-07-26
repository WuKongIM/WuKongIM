package backup_test

import (
	"strings"
	"testing"

	backup "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestRestoreEvidenceAccumulatorComputesTypedEvidence(t *testing.T) {
	accumulator := backup.NewRestoreEvidenceAccumulator(7)
	metadata, err := backup.MarshalMetadataLogRecord(backup.MetadataLogRecord{
		HashSlot: 7, RaftIndex: 3, RaftTerm: 2,
		CommittedAtUnixMillis: 1710000000000, Command: []byte("metadata"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accumulator.AddMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	for _, record := range []backup.MessageLogRecord{
		{
			Kind: backup.MessageLogRecordMessage, HashSlot: 7,
			ChannelID: "z-room", ChannelType: 2, Epoch: 1, HW: 2,
			MessageSeq: 1, MessageID: 8, ServerTimestampMS: 1710000000000,
			Payload: []byte("one"),
		},
		{
			Kind: backup.MessageLogRecordMessage, HashSlot: 7,
			ChannelID: "a-room", ChannelType: 1, Epoch: 2, LogStartOffset: 3, HW: 5,
			MessageSeq: 5, MessageID: 11, ServerTimestampMS: 1710000001000,
			Payload: []byte("two"),
		},
		{
			Kind: backup.MessageLogRecordBoundary, HashSlot: 7,
			ChannelID: "z-room", ChannelType: 2, Epoch: 1, LogStartOffset: 1, HW: 2,
		},
	} {
		body, err := backup.MarshalMessageLogRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := accumulator.AddMessage(body); err != nil {
			t.Fatal(err)
		}
	}
	evidence, err := accumulator.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Version != backup.RestoreEvidenceVersion ||
		evidence.MetadataRecords != 1 || evidence.MessageRecords != 2 ||
		evidence.MessageBoundaryRecords != 1 || evidence.ChannelBoundaryCount != 2 ||
		evidence.MaxMessageID != 11 || evidence.PlainBytes == 0 ||
		len(evidence.ContentSHA256) != 64 || len(evidence.MessageMerkleSHA256) != 64 {
		t.Fatalf("unexpected evidence: %#v", evidence)
	}
}

func TestRestoreEvidenceAccumulatorRejectsSlotAndBoundaryRegression(t *testing.T) {
	accumulator := backup.NewRestoreEvidenceAccumulator(7)
	wrongSlot, err := backup.MarshalMetadataLogRecord(backup.MetadataLogRecord{
		HashSlot: 8, RaftIndex: 1, RaftTerm: 1,
		CommittedAtUnixMillis: 1710000000000, Command: []byte("metadata"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accumulator.AddMetadata(wrongSlot); err == nil {
		t.Fatal("AddMetadata(wrong slot) error = nil")
	}
	if err := accumulator.MergeBoundary(backup.ChannelBoundary{
		ChannelID: "room", ChannelType: 2, Epoch: 3, LogStartOffset: 4, HW: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.MergeBoundary(backup.ChannelBoundary{
		ChannelID: "room", ChannelType: 2, Epoch: 3, LogStartOffset: 3, HW: 9,
	}); err == nil {
		t.Fatal("MergeBoundary(regression) error = nil")
	}
}

func TestRestoreEvidenceAccumulatorMerkleRootIsOrderSensitive(t *testing.T) {
	makeRoot := func(payloads ...string) string {
		t.Helper()
		accumulator := backup.NewRestoreEvidenceAccumulator(1)
		for index, payload := range payloads {
			body, err := backup.MarshalMessageLogRecord(backup.MessageLogRecord{
				Kind: backup.MessageLogRecordMessage, HashSlot: 1,
				ChannelID: "room", ChannelType: 2, Epoch: 1, HW: uint64(len(payloads)),
				MessageSeq: uint64(index + 1), MessageID: uint64(index + 10),
				ServerTimestampMS: 1710000000000, Payload: []byte(payload),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := accumulator.AddMessage(body); err != nil {
				t.Fatal(err)
			}
		}
		evidence, err := accumulator.Finish()
		if err != nil {
			t.Fatal(err)
		}
		return evidence.MessageMerkleSHA256
	}
	first := makeRoot("one", "two", "three")
	second := makeRoot("three", "two", "one")
	if first == second || first == strings.Repeat("0", 64) {
		t.Fatalf("Merkle roots do not bind order: first=%s second=%s", first, second)
	}
}

func TestRestoreEvidenceAccumulatorRejectsDuplicateAndRegressingOrder(t *testing.T) {
	metadata := backup.NewRestoreEvidenceAccumulator(1)
	for _, index := range []uint64{2, 1} {
		body, err := backup.MarshalMetadataLogRecord(backup.MetadataLogRecord{
			HashSlot: 1, RaftIndex: index, RaftTerm: 2,
			CommittedAtUnixMillis: 1710000000000,
			Command:               []byte("metadata"),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = metadata.AddMetadata(body)
		if index == 2 && err != nil {
			t.Fatal(err)
		}
		if index == 1 && err == nil {
			t.Fatal("AddMetadata(regressing index) error = nil")
		}
	}

	messages := backup.NewRestoreEvidenceAccumulator(1)
	for attempt, sequence := range []uint64{2, 2, 1} {
		body, err := backup.MarshalMessageLogRecord(backup.MessageLogRecord{
			Kind: backup.MessageLogRecordMessage, HashSlot: 1,
			ChannelID: "room", ChannelType: 2, Epoch: 1, HW: 2,
			MessageSeq: sequence, MessageID: uint64(attempt + 10),
			ServerTimestampMS: 1710000000000, Payload: []byte("payload"),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = messages.AddMessage(body)
		if attempt == 0 && err != nil {
			t.Fatal(err)
		}
		if attempt > 0 && err == nil {
			t.Fatalf("AddMessage(sequence %d after 2) error = nil", sequence)
		}
	}
}
