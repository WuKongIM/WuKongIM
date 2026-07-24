package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestSegmentCodecKeepsLogicalIdentityAcrossFreshEncryption(t *testing.T) {
	t.Parallel()

	descriptor := backup.SegmentDescriptor{
		Logical: backup.SegmentLogicalDescriptor{
			RepositoryID:     "repo-prod",
			SourceClusterID:  "cluster-source",
			SourceGeneration: "source-generation-7",
			Generation:       "slot-17-generation-3",
			HashSlot:         17,
			Stream:           backup.SegmentStreamMessages,
			Sequence:         9,
			RecordCount:      2,
		},
		KMSKeyID: "kms-prod",
	}
	plaintext := []byte("channel-a:41\nchannel-a:42\n")
	firstCodec := backup.NewSegmentCodec(wrappingKeyManager{wrappingByte: 0xa5}, bytes.NewReader(bytes.Repeat([]byte{0x11}, 64)))
	secondCodec := backup.NewSegmentCodec(wrappingKeyManager{wrappingByte: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x22}, 64)))

	first, err := firstCodec.Seal(context.Background(), descriptor, plaintext)
	if err != nil {
		t.Fatalf("first Seal() error = %v", err)
	}
	second, err := secondCodec.Seal(context.Background(), descriptor, plaintext)
	if err != nil {
		t.Fatalf("second Seal() error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("fresh encryption changed logical segment identity: %q != %q", first.ID, second.ID)
	}
	if first.Payload.Key == second.Payload.Key || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("fresh encryption reused ciphertext identity")
	}

	changed := descriptor
	changed.Logical.Sequence++
	third, err := firstCodec.Seal(context.Background(), changed, plaintext)
	if err != nil {
		t.Fatalf("changed Seal() error = %v", err)
	}
	if third.ID == first.ID {
		t.Fatal("different logical sequence reused segment identity")
	}

	restored, err := firstCodec.Open(context.Background(), first.Header, first.Payload, first.Ciphertext)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(restored, plaintext) {
		t.Fatalf("Open() payload = %q, want %q", restored, plaintext)
	}
}

func TestSegmentCommitStrictlyAuthenticatesCanonicalRecord(t *testing.T) {
	t.Parallel()

	codec := backup.NewSegmentCodec(wrappingKeyManager{wrappingByte: 0xa5}, bytes.NewReader(bytes.Repeat([]byte{0x31}, 64)))
	sealed, err := codec.Seal(context.Background(), backup.SegmentDescriptor{
		Logical: backup.SegmentLogicalDescriptor{
			RepositoryID:     "repo-prod",
			SourceClusterID:  "cluster-source",
			SourceGeneration: "source-generation-7",
			Generation:       "slot-17-generation-3",
			HashSlot:         17,
			Stream:           backup.SegmentStreamMessages,
			Sequence:         9,
			RecordCount:      2,
		},
		KMSKeyID: "kms-prod",
	}, []byte("channel-a:41\nchannel-a:42\n"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	seed := sha256.Sum256([]byte("segment-commit-signing-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	signed, err := backup.SignSegmentCommit(context.Background(), backup.SegmentCommit{
		Format:              backup.SegmentCommitFormat,
		Version:             backup.SegmentCommitVersion,
		SegmentID:           sealed.ID,
		Header:              sealed.Header,
		Payload:             sealed.Payload,
		PrimaryRepository:   "primary",
		SecondaryRepository: "secondary",
	}, signer, "signing-key")
	if err != nil {
		t.Fatalf("SignSegmentCommit() error = %v", err)
	}
	body, err := backup.MarshalSegmentCommit(signed)
	if err != nil {
		t.Fatalf("MarshalSegmentCommit() error = %v", err)
	}
	loaded, err := backup.LoadSegmentCommit(context.Background(), body, signer)
	if err != nil {
		t.Fatalf("LoadSegmentCommit() error = %v", err)
	}
	if loaded.SegmentID != sealed.ID || loaded.Payload != sealed.Payload || loaded.Signature == nil {
		t.Fatalf("LoadSegmentCommit() = %#v, want signed segment %q", loaded, sealed.ID)
	}

	withUnknownField := bytes.Replace(body, []byte(`"signature":`), []byte(`"unknown":true,"signature":`), 1)
	if _, err := backup.LoadSegmentCommit(context.Background(), withUnknownField, signer); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("LoadSegmentCommit(unknown field) error = %v, want %v", err, backup.ErrInvalidObject)
	}
	tampered := bytes.Replace(body, []byte(`"primary_repository":"primary"`), []byte(`"primary_repository":"tampered"`), 1)
	if _, err := backup.LoadSegmentCommit(context.Background(), tampered, signer); !errors.Is(err, backup.ErrInvalidSignature) {
		t.Fatalf("LoadSegmentCommit(tampered) error = %v, want %v", err, backup.ErrInvalidSignature)
	}
	oversized := append(append([]byte(nil), body...), bytes.Repeat([]byte(" "), 64<<10)...)
	if _, err := backup.LoadSegmentCommit(context.Background(), oversized, signer); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("LoadSegmentCommit(oversized) error = %v, want %v", err, backup.ErrInvalidObject)
	}
}

func TestSegmentCodecRejectsTamperedAndUnboundedInput(t *testing.T) {
	t.Parallel()

	codec := backup.NewSegmentCodec(wrappingKeyManager{wrappingByte: 0xa5}, bytes.NewReader(bytes.Repeat([]byte{0x71}, 64)))
	sealed, err := codec.Seal(context.Background(), backup.SegmentDescriptor{
		Logical: backup.SegmentLogicalDescriptor{
			RepositoryID:     "repo-prod",
			SourceClusterID:  "cluster-source",
			SourceGeneration: "source-generation-7",
			Generation:       "slot-17-generation-3",
			HashSlot:         17,
			Stream:           backup.SegmentStreamMessages,
			Sequence:         9,
			RecordCount:      2,
		},
		KMSKeyID: "kms-prod",
	}, []byte("channel-a:41\nchannel-a:42\n"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	corruptCiphertext := append([]byte(nil), sealed.Ciphertext...)
	corruptCiphertext[len(corruptCiphertext)-1] ^= 0xff
	if _, err := codec.Open(context.Background(), sealed.Header, sealed.Payload, corruptCiphertext); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("Open(corrupt ciphertext) error = %v, want %v", err, backup.ErrObjectCorrupt)
	}
	tamperedHeader := sealed.Header
	tamperedHeader.Logical.RecordCount++
	if _, err := codec.Open(context.Background(), tamperedHeader, sealed.Payload, sealed.Ciphertext); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("Open(tampered header) error = %v, want %v", err, backup.ErrInvalidObject)
	}
	unboundedHeader := sealed.Header
	unboundedHeader.PlaintextBytes = 1 << 40
	if _, err := codec.Open(context.Background(), unboundedHeader, sealed.Payload, sealed.Ciphertext); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("Open(unbounded header) error = %v, want %v", err, backup.ErrInvalidObject)
	}
}
