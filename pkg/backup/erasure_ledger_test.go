package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestErasureLedgerArtifactsRoundTripAndRejectTampering(t *testing.T) {
	t.Parallel()

	seed := sha256.Sum256([]byte("erasure-ledger-signing-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	eventID := backup.ComputeErasureEventID("repo-prod", "cluster-source", "generation-7", "channel-a", 2, 41)
	event := backup.ErasureLedgerEvent{
		Format: backup.ErasureLedgerEventFormat, Version: backup.ErasureLedgerEventVersion,
		RepositoryID: "repo-prod", SourceClusterID: "cluster-source", SourceGeneration: "generation-7",
		EventID: eventID, HashSlot: 3, ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41,
		RequestedAtUnixMillis: 1_753_056_420_000,
	}
	eventBody, err := backup.MarshalErasureLedgerEvent(event)
	if err != nil {
		t.Fatalf("MarshalErasureLedgerEvent() error = %v", err)
	}
	loadedEvent, err := backup.LoadErasureLedgerEvent(eventBody)
	if err != nil {
		t.Fatalf("LoadErasureLedgerEvent() error = %v", err)
	}
	if loadedEvent != event {
		t.Fatalf("LoadErasureLedgerEvent() = %#v, want %#v", loadedEvent, event)
	}

	object := testSealedObject("objects/erasure-ledger/"+eventID+"/attempt-1.wkb", event.HashSlot, []byte("ciphertext"))
	object.Entry.Kind = backup.ObjectKindErasureLedger
	record := backup.ErasureLedgerRecord{
		Format: backup.ErasureLedgerRecordFormat, Version: backup.ErasureLedgerRecordVersion,
		RepositoryID: event.RepositoryID, SourceClusterID: event.SourceClusterID, SourceGeneration: event.SourceGeneration,
		EventID: event.EventID, HashSlot: event.HashSlot, CreatedAtUnixMillis: event.RequestedAtUnixMillis, Object: object.Entry,
	}
	signedRecord, err := backup.SignErasureLedgerRecord(context.Background(), record, signer, "signing-v1")
	if err != nil {
		t.Fatalf("SignErasureLedgerRecord() error = %v", err)
	}
	recordBody, err := backup.MarshalErasureLedgerRecord(signedRecord)
	if err != nil {
		t.Fatalf("MarshalErasureLedgerRecord() error = %v", err)
	}
	loadedRecord, err := backup.LoadErasureLedgerRecord(context.Background(), recordBody, signer)
	if err != nil {
		t.Fatalf("LoadErasureLedgerRecord() error = %v", err)
	}
	if loadedRecord.EventID != eventID || loadedRecord.Object != object.Entry {
		t.Fatalf("LoadErasureLedgerRecord() = %#v, want event/object binding", loadedRecord)
	}

	recordHash := sha256.Sum256(recordBody)
	commit := backup.ErasureLedgerCommit{
		Format: backup.ErasureLedgerCommitFormat, Version: backup.ErasureLedgerCommitVersion,
		RepositoryID: event.RepositoryID, SourceClusterID: event.SourceClusterID, SourceGeneration: event.SourceGeneration,
		Sequence: 7, EventID: eventID, RecordKey: backup.ErasureLedgerRecordKey(event.HashSlot, eventID),
		RecordSHA256: stringLowerHex(recordHash[:]), CreatedAtUnixMillis: event.RequestedAtUnixMillis,
		PrimaryRepository: "primary", SecondaryRepository: "secondary",
	}
	signedCommit, err := backup.SignErasureLedgerCommit(context.Background(), commit, signer, "signing-v1")
	if err != nil {
		t.Fatalf("SignErasureLedgerCommit() error = %v", err)
	}
	commitBody, err := backup.MarshalErasureLedgerCommit(signedCommit)
	if err != nil {
		t.Fatalf("MarshalErasureLedgerCommit() error = %v", err)
	}
	loadedCommit, err := backup.LoadErasureLedgerCommit(context.Background(), commitBody, signer)
	if err != nil {
		t.Fatalf("LoadErasureLedgerCommit() error = %v", err)
	}
	if loadedCommit.Sequence != 7 || loadedCommit.RecordSHA256 != commit.RecordSHA256 {
		t.Fatalf("LoadErasureLedgerCommit() = %#v, want exact committed record", loadedCommit)
	}

	tampered := bytes.Replace(commitBody, []byte(`"sequence":7`), []byte(`"sequence":8`), 1)
	if _, err := backup.LoadErasureLedgerCommit(context.Background(), tampered, signer); err == nil {
		t.Fatal("LoadErasureLedgerCommit() tampered error = nil")
	}
	unknown := bytes.Replace(recordBody, []byte(`"format":`), []byte(`"unknown":true,"format":`), 1)
	if _, err := backup.LoadErasureLedgerRecord(context.Background(), unknown, signer); err == nil {
		t.Fatal("LoadErasureLedgerRecord() unknown-field error = nil")
	}
}

func TestErasureLedgerEventRejectsMismatchedDeterministicIdentity(t *testing.T) {
	t.Parallel()

	event := backup.ErasureLedgerEvent{
		Format: backup.ErasureLedgerEventFormat, Version: backup.ErasureLedgerEventVersion,
		RepositoryID: "repo-prod", SourceClusterID: "cluster-source", SourceGeneration: "generation-7",
		EventID: stringLowerHex(make([]byte, sha256.Size)), HashSlot: 3,
		ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41, RequestedAtUnixMillis: 1_753_056_420_000,
	}
	if _, err := backup.MarshalErasureLedgerEvent(event); err == nil {
		t.Fatal("MarshalErasureLedgerEvent() error = nil, want mismatched event id rejection")
	}
}

func stringLowerHex(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for index, item := range value {
		out[index*2] = digits[item>>4]
		out[index*2+1] = digits[item&0x0f]
	}
	return string(out)
}
