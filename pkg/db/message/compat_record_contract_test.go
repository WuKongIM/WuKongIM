package message

import (
	"errors"
	"reflect"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestCompatibilityRecordsPreserveProposalIdentitySemantics(t *testing.T) {
	rows := []messageRow{
		{
			MessageSeq: 21, MessageID: 101, FramerFlags: 4 | 2, Setting: 3, StreamFlag: 1,
			MsgKey: "key-1", Expire: 60, ClientSeq: 11, ClientMsgNo: "client-1",
			StreamNo: "stream-1", StreamID: 71, Timestamp: 123, ServerTimestampMS: 1_700_000_000_101,
			ChannelID: "room", ChannelType: 2, Topic: "topic-1", FromUID: "alice",
			Payload: []byte("one"),
		},
		{
			MessageSeq: 22, MessageID: 102, FramerFlags: 8, Setting: 5, StreamFlag: 2,
			MsgKey: "key-2", Expire: 120, ClientSeq: 12, ClientMsgNo: "client-2",
			StreamNo: "stream-2", StreamID: 72, Timestamp: 124, ServerTimestampMS: 1_700_000_000_102,
			ChannelID: "room", ChannelType: 2, Topic: "topic-2", FromUID: "bob",
			Payload: []byte("two"), PayloadHash: hashPayload([]byte("two")),
		},
	}
	records, err := recordsFromRows(rows)
	if err != nil {
		t.Fatalf("records from rows: %v", err)
	}
	for index := range records {
		records[index].Epoch = 9
	}

	decoded, err := compatibilityRowsFromRecords(21, records)
	if err != nil {
		t.Fatalf("compatibility rows from records: %v", err)
	}
	for index := range rows {
		assertCompatibilityIdentityFields(t, decoded[index], rows[index])
	}
	if decoded[0].PayloadHash != hashPayload(rows[0].Payload) {
		t.Fatalf("derived payload hash = %d, want %d", decoded[0].PayloadHash, hashPayload(rows[0].Payload))
	}

	manifest := DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 9, LeaderTerm: 11, FenceVersion: 13,
		CommandID: quorumlog.CommandID{1}, BaseOffset: 20, LastOffset: 22,
		PreviousTerm: 7, PreviousIndex: 20, PreviousDigest: quorumlog.EntryDigest{2},
	}
	gotEntries, ok := deriveDurableProposalEntries(manifest, records, decoded)
	if !ok {
		t.Fatal("derive durable proposal entries failed")
	}
	wantRecords := make([]quorumlog.Record, len(decoded))
	for index, row := range decoded {
		wantRecords[index] = quorumlog.Record{
			ID: row.MessageID, Index: row.MessageSeq, Epoch: records[index].Epoch,
			Setting: row.Setting, FromUID: row.FromUID, ClientMsgNo: row.ClientMsgNo,
			ServerTimestampMS: row.ServerTimestampMS, SyncOnce: row.FramerFlags&4 != 0,
			Payload: row.Payload,
		}
	}
	_, wantEntries, ok := quorumlog.SealProposalManifest(manifest, wantRecords)
	if !ok {
		t.Fatal("seal independent proposal manifest failed")
	}
	if !reflect.DeepEqual(gotEntries, wantEntries) {
		t.Fatalf("derived entries = %+v, want %+v", gotEntries, wantEntries)
	}
}

func TestCompatibilityRowsRejectContradictoryRecordEnvelope(t *testing.T) {
	valid, err := compatibilityRecordFromRow(messageRow{MessageID: 101, Payload: []byte("payload")})
	if err != nil {
		t.Fatalf("build valid record: %v", err)
	}

	tests := []struct {
		name      string
		startSeq  uint64
		record    channel.Record
		wantError error
	}{
		{name: "zero start offset", startSeq: 0, record: valid, wantError: channel.ErrInvalidArgument},
		{name: "noncontiguous index", startSeq: 5, record: withRecordIndex(valid, 6), wantError: channel.ErrCorruptState},
		{name: "message id disagrees with payload", startSeq: 5, record: withRecordID(valid, 102), wantError: channel.ErrCorruptState},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := compatibilityRowsFromRecords(tc.startSeq, []channel.Record{tc.record}); !errors.Is(err, tc.wantError) {
				t.Fatalf("error = %v, want %v", err, tc.wantError)
			}
		})
	}

	rows, err := compatibilityRowsFromRecords(0, nil)
	if err != nil || rows != nil {
		t.Fatalf("empty conversion = %#v, %v; want nil, nil", rows, err)
	}
}

func TestDefaultMissingServerTimestampUsesOneBatchTimestamp(t *testing.T) {
	const batchTimestampMS = int64(1_700_000_000_500)
	rows := []messageRow{
		{MessageID: 101, MessageSeq: 1, ServerTimestampMS: 0},
		{MessageID: 102, MessageSeq: 2, ServerTimestampMS: batchTimestampMS - 1},
		{MessageID: 103, MessageSeq: 3, ServerTimestampMS: 0},
	}

	defaultMissingServerTimestampMS(rows, batchTimestampMS)

	if rows[0].ServerTimestampMS != batchTimestampMS || rows[2].ServerTimestampMS != batchTimestampMS {
		t.Fatalf("missing timestamps = %d, %d; want %d", rows[0].ServerTimestampMS, rows[2].ServerTimestampMS, batchTimestampMS)
	}
	if rows[1].ServerTimestampMS != batchTimestampMS-1 {
		t.Fatalf("existing timestamp = %d, want preserved %d", rows[1].ServerTimestampMS, batchTimestampMS-1)
	}

	manifest := DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: quorumlog.CommandID{1}, BaseOffset: 0, LastOffset: 3,
	}
	records := []channel.Record{{Epoch: 3}, {Epoch: 3}, {Epoch: 3}}
	if entries, ok := deriveDurableProposalEntries(manifest, records, rows); !ok || len(entries) != len(rows) {
		t.Fatalf("derive entries after timestamp default = %d, %v", len(entries), ok)
	}
}

func TestDeriveDurableProposalEntriesRejectsMismatchedRecordAndRowCounts(t *testing.T) {
	manifest := DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: quorumlog.CommandID{1}, BaseOffset: 0, LastOffset: 1,
	}
	if entries, ok := deriveDurableProposalEntries(manifest, []channel.Record{{Epoch: 3}}, nil); ok || entries != nil {
		t.Fatalf("derive mismatched entries = %#v, %v; want nil, false", entries, ok)
	}
}

func assertCompatibilityIdentityFields(t *testing.T, got messageRow, want messageRow) {
	t.Helper()
	if got.MessageSeq != want.MessageSeq || got.MessageID != want.MessageID || got.FramerFlags != want.FramerFlags ||
		got.Setting != want.Setting || got.StreamFlag != want.StreamFlag || got.MsgKey != want.MsgKey ||
		got.Expire != want.Expire || got.ClientSeq != want.ClientSeq || got.ClientMsgNo != want.ClientMsgNo ||
		got.StreamNo != want.StreamNo || got.StreamID != want.StreamID || got.Timestamp != want.Timestamp ||
		got.ServerTimestampMS != want.ServerTimestampMS || got.ChannelID != want.ChannelID ||
		got.ChannelType != want.ChannelType || got.Topic != want.Topic || got.FromUID != want.FromUID ||
		!reflect.DeepEqual(got.Payload, want.Payload) {
		t.Fatalf("decoded row = %#v, want identity fields from %#v", got, want)
	}
}

func withRecordIndex(record channel.Record, index uint64) channel.Record {
	record.Index = index
	return record
}

func withRecordID(record channel.Record, id uint64) channel.Record {
	record.ID = id
	return record
}
