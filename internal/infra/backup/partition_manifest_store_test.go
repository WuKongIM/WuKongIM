package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestReplicatedManifestStoreRepairsPartialPublish(t *testing.T) {
	ctx := context.Background()
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	objectBody := []byte("ciphertext")
	objectHash := sha256.Sum256(objectBody)
	objectChecksum := fmt.Sprintf("%x", objectHash)
	entry := backupartifact.ObjectEntry{
		Key: "objects/backup-partial-attempt/00004/metadata-000000.bin", Kind: backupartifact.ObjectKindMetadata, HashSlot: 4,
		PlaintextSHA256: fmt.Sprintf("%064x", 1), CiphertextSHA256: objectChecksum, PlaintextBytes: 3, CiphertextBytes: int64(len(objectBody)),
		Compression: backupartifact.CompressionZstd, Encryption: backupartifact.EncryptionAES256GCM,
		KMSKeyID: "kms-backup", WrappedKey: "d3JhcHBlZA==", Nonce: "MDEyMzQ1Njc4OTAx",
	}
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		require.NoError(t, repository.PutImmutable(ctx, entry.Key, int64(len(objectBody)), objectChecksum, bytes.NewReader(objectBody)))
	}
	manifest := backupartifact.PartitionManifest{
		Format: backupartifact.PartitionManifestFormat, Version: backupartifact.PartitionManifestVersion,
		JobID: "backup-partial", BackupEpoch: 7,
		Cut:      backupartifact.PartitionCut{HashSlot: 4, RaftIndex: 9, CommittedAtMillis: 1710000000000},
		Evidence: backupartifact.PartitionEvidence{Version: backupartifact.PartitionEvidenceVersion, MetadataRecords: 1},
		Objects:  []backupartifact.ObjectEntry{entry},
	}
	body, err := backupartifact.MarshalPartitionManifest(manifest)
	require.NoError(t, err)
	hash := sha256.Sum256(body)
	checksum := fmt.Sprintf("%x", hash)
	key := "partition-manifests/backup-partial/00004.json"
	require.NoError(t, secondary.PutImmutable(ctx, key, int64(len(body)), checksum, bytes.NewReader(body)))
	store, err := backupinfra.NewReplicatedManifestStore(primary, secondary)
	require.NoError(t, err)
	loaded, loadedChecksum, err := store.Load(ctx, key)
	require.NoError(t, err)
	require.Equal(t, body, loaded)
	require.Equal(t, checksum, loadedChecksum)
	stored, err := primary.Stat(ctx, key)
	require.NoError(t, err)
	require.Equal(t, checksum, stored.SHA256)
}
