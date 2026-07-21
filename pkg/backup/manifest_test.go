package backup_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestSignedManifestRoundTripPreservesLogicalClusterCut(t *testing.T) {
	t.Parallel()

	seed := sha256.Sum256([]byte("manifest-round-trip-test-key"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	signer := ed25519ManifestSigner{privateKey: privateKey}

	manifest := backup.Manifest{
		Format:              backup.ManifestFormat,
		Version:             backup.ManifestVersion,
		ApplicationVersion:  "3.0.0-beta.1",
		RepositoryID:        "repo-prod",
		SourceClusterID:     "cluster-source",
		SourceGeneration:    "generation-7",
		RestorePointID:      "rp-20260721-0005",
		BackupEpoch:         17,
		Kind:                backup.RestorePointIncremental,
		HashSlotCount:       2,
		CreatedAtUnixMillis: 1_753_056_360_000,
		EffectiveAtMillis:   1_753_056_300_000,
		Cuts: []backup.PartitionCut{
			{HashSlot: 0, RaftIndex: 101, CommittedAtMillis: 1_753_056_330_000},
			{HashSlot: 1, RaftIndex: 99, CommittedAtMillis: 1_753_056_300_000},
		},
		Objects: []backup.ObjectEntry{
			{
				Key:              "objects/00/meta-000001.wkb",
				Kind:             backup.ObjectKindMetadata,
				HashSlot:         0,
				PlaintextSHA256:  "5df6e0e2761358c5b2f62f5848f5582f529c3dd7af13afee62d0b3123f3f5e6b",
				CiphertextSHA256: "841b12cbb3456b1f7a08f1c7fbf9d5b5539ad19371aa030190d12f2c83ab8eaf",
				PlaintextBytes:   11,
				CiphertextBytes:  47,
				Compression:      backup.CompressionZstd,
				Encryption:       backup.EncryptionAES256GCM,
				KMSKeyID:         "kms-backup",
				WrappedKey:       "d3JhcHBlZA==",
				Nonce:            "bm9uY2Utbm9uY2U=",
			},
			{
				Key:              "objects/01/meta-000001.wkb",
				Kind:             backup.ObjectKindMetadata,
				HashSlot:         1,
				PlaintextSHA256:  "a2f90c7e90f6f547928be2b196e3e144e0e276c4eb77e5d8b0154d6f6ccbbd8a",
				CiphertextSHA256: "0ce16d30f29914bd63858ed9cf52de7d7dcb6c8e7550e05684279dd821e26c5a",
				PlaintextBytes:   13,
				CiphertextBytes:  49,
				Compression:      backup.CompressionZstd,
				Encryption:       backup.EncryptionAES256GCM,
				KMSKeyID:         "kms-backup",
				WrappedKey:       "d3JhcHBlZA==",
				Nonce:            "bm9uY2Utbm9uY2U=",
			},
		},
		ErasureLedgerBoundary: 4,
	}

	signed, err := backup.SignManifest(context.Background(), manifest, signer, "manifest-signing-key")
	if err != nil {
		t.Fatalf("SignManifest() error = %v", err)
	}
	body, err := backup.MarshalManifest(signed)
	if err != nil {
		t.Fatalf("MarshalManifest() error = %v", err)
	}
	loaded, err := backup.LoadManifest(context.Background(), body, signer)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}

	if loaded.RestorePointID != manifest.RestorePointID || loaded.EffectiveAtMillis != manifest.EffectiveAtMillis {
		t.Fatalf("loaded manifest identity = %q/%d, want %q/%d", loaded.RestorePointID, loaded.EffectiveAtMillis, manifest.RestorePointID, manifest.EffectiveAtMillis)
	}
	if len(loaded.Cuts) != 2 || loaded.Cuts[1].HashSlot != 1 || loaded.Cuts[1].RaftIndex != 99 {
		t.Fatalf("loaded cuts = %#v", loaded.Cuts)
	}
	if loaded.Signature.KeyID != "manifest-signing-key" || loaded.Signature.Algorithm != "ed25519" {
		t.Fatalf("loaded signature = %#v", loaded.Signature)
	}
}

func TestSignManifestRejectsSignerKeyMismatch(t *testing.T) {
	t.Parallel()

	objects := []backup.SealedObject{
		testSealedObject("objects/00/meta.wkb", 0, []byte("slot-zero")),
		testSealedObject("objects/01/meta.wkb", 1, []byte("slot-one")),
	}
	signer := mismatchedKeySigner{}
	_, err := backup.SignManifest(context.Background(), testPublishManifest(objects), signer, "expected-key")
	if err == nil {
		t.Fatal("SignManifest() error = nil, want signer key mismatch rejection")
	}
}

type ed25519ManifestSigner struct {
	privateKey ed25519.PrivateKey
}

type mismatchedKeySigner struct{}

func (mismatchedKeySigner) Sign(_ context.Context, _ string, _ []byte) (backup.ManifestSignature, error) {
	return backup.ManifestSignature{Algorithm: "test", KeyID: "different-key", Value: []byte("signature")}, nil
}

func (mismatchedKeySigner) Verify(_ context.Context, _ backup.ManifestSignature, _ []byte) error {
	return nil
}

func (s ed25519ManifestSigner) Sign(_ context.Context, keyID string, message []byte) (backup.ManifestSignature, error) {
	return backup.ManifestSignature{
		Algorithm: "ed25519",
		KeyID:     keyID,
		Value:     ed25519.Sign(s.privateKey, message),
	}, nil
}

func (s ed25519ManifestSigner) Verify(_ context.Context, signature backup.ManifestSignature, message []byte) error {
	if !ed25519.Verify(s.privateKey.Public().(ed25519.PublicKey), message, signature.Value) {
		return backup.ErrInvalidSignature
	}
	return nil
}
