package backup_test

import (
	"testing"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestMetadataLogRecordRoundTripPreservesCommittedCommand(t *testing.T) {
	body, err := backupartifact.MarshalMetadataLogRecord(backupartifact.MetadataLogRecord{
		HashSlot: 17, RaftIndex: 42, RaftTerm: 7,
		CommittedAtUnixMillis: 1_753_400_100_000,
		Command:               []byte{1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("MarshalMetadataLogRecord() error = %v", err)
	}
	record, err := backupartifact.LoadMetadataLogRecord(body)
	if err != nil {
		t.Fatalf("LoadMetadataLogRecord() error = %v", err)
	}
	if record.HashSlot != 17 || record.RaftIndex != 42 || record.RaftTerm != 7 ||
		record.CommittedAtUnixMillis != 1_753_400_100_000 ||
		string(record.Command) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("record = %#v", record)
	}
}

func TestMetadataLogRecordRejectsTrailingOrEmptyCommand(t *testing.T) {
	if _, err := backupartifact.MarshalMetadataLogRecord(backupartifact.MetadataLogRecord{
		HashSlot: 17, RaftIndex: 42, RaftTerm: 7,
		CommittedAtUnixMillis: 1_753_400_100_000,
	}); err == nil {
		t.Fatal("MarshalMetadataLogRecord(empty command) error = nil")
	}
	body, err := backupartifact.MarshalMetadataLogRecord(backupartifact.MetadataLogRecord{
		HashSlot: 17, RaftIndex: 42, RaftTerm: 7,
		CommittedAtUnixMillis: 1_753_400_100_000,
		Command:               []byte{1},
	})
	if err != nil {
		t.Fatalf("MarshalMetadataLogRecord() error = %v", err)
	}
	body = append(body, 0)
	if _, err := backupartifact.LoadMetadataLogRecord(body); err == nil {
		t.Fatal("LoadMetadataLogRecord(trailing data) error = nil")
	}
}
