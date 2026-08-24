package quorumlog

import "testing"

func TestAppendOutcomeIsClosedAndZeroIsInvalid(t *testing.T) {
	if AppendOutcomeUnspecified.Valid() || AppendOutcomeUnspecified.Durable() {
		t.Fatal("zero append outcome must fail closed")
	}
	for _, outcome := range []AppendOutcome{
		AppendOutcomeDurable,
		AppendOutcomeAlreadyDurable,
		AppendOutcomeDefinitelyNotWritten,
		AppendOutcomeConflict,
		AppendOutcomeUnknown,
	} {
		if !outcome.Valid() {
			t.Fatalf("AppendOutcome(%d).Valid() = false", outcome)
		}
	}
	if !AppendOutcomeDurable.Durable() || !AppendOutcomeAlreadyDurable.Durable() || AppendOutcomeUnknown.Durable() {
		t.Fatal("AppendOutcome.Durable() classification is incorrect")
	}
}

func TestSealProposalManifestBindsEveryEntrySemanticField(t *testing.T) {
	manifest := ProposalManifest{
		Version: ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: CommandID{1}, BaseOffset: 0, LastOffset: 2,
	}
	records := []Record{
		{ID: 11, Index: 1, Epoch: 3, Setting: 1, FromUID: "u1", ClientMsgNo: "c1", ServerTimestampMS: 1001, SyncOnce: true, Payload: []byte("one")},
		{ID: 12, Index: 2, Epoch: 3, Setting: 2, FromUID: "u2", ClientMsgNo: "c2", ServerTimestampMS: 1002, Payload: []byte("two")},
	}
	sealed, entries, ok := SealProposalManifest(manifest, records)
	if !ok || len(entries) != 2 || sealed.Digest != entries[1].Digest {
		t.Fatalf("SealProposalManifest() = manifest %+v entries %+v ok %v", sealed, entries, ok)
	}
	if entries[1].PreviousDigest != entries[0].Digest || entries[1].PreviousIndex != entries[0].Index || entries[1].PreviousTerm != entries[0].LeaderTerm {
		t.Fatalf("entry chain = %+v, want second entry linked to first", entries)
	}

	mutations := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "message_id", mutate: func(record *Record) { record.ID++ }},
		{name: "setting", mutate: func(record *Record) { record.Setting++ }},
		{name: "sender", mutate: func(record *Record) { record.FromUID += "-other" }},
		{name: "client_message", mutate: func(record *Record) { record.ClientMsgNo += "-other" }},
		{name: "timestamp", mutate: func(record *Record) { record.ServerTimestampMS++ }},
		{name: "sync_once", mutate: func(record *Record) { record.SyncOnce = !record.SyncOnce }},
		{name: "payload", mutate: func(record *Record) { record.Payload = []byte("other") }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			changed := append([]Record(nil), records...)
			changed[0].Payload = append([]byte(nil), records[0].Payload...)
			test.mutate(&changed[0])
			changedManifest, _, ok := SealProposalManifest(manifest, changed)
			if !ok {
				t.Fatal("SealProposalManifest(changed) failed")
			}
			if changedManifest.Digest == sealed.Digest {
				t.Fatalf("tail digest did not bind changed %s", test.name)
			}
		})
	}
}

func TestDeriveProposalEntriesRejectsAuthorityAndTimestampMismatch(t *testing.T) {
	manifest := ProposalManifest{
		Version: ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: CommandID{1}, BaseOffset: 0, LastOffset: 1,
	}
	for _, record := range []Record{
		{ID: 1, Index: 1, Epoch: 2, ServerTimestampMS: 1000},
		{ID: 1, Index: 1, Epoch: 3, ServerTimestampMS: 0},
		{ID: 1, Index: 1, Epoch: 3, ServerTimestampMS: -1},
	} {
		if _, ok := DeriveProposalEntries(manifest, 1, func(int) Record { return record }); ok {
			t.Fatalf("DeriveProposalEntries(%+v) succeeded, want rejection", record)
		}
	}
}
