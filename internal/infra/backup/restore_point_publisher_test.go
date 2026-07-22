package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestRestorePointPublisherLoadsEveryPartitionFromBothRepositories(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	manifestStore, err := backupinfra.NewReplicatedManifestStore(primary, secondary)
	require.NoError(t, err)
	job := backupusecase.Job{
		ID:            "backup-12",
		Epoch:         12,
		Kind:          backupartifact.RestorePointIncremental,
		HashSlotCount: 2,
	}
	for hashSlot := uint16(0); hashSlot < 2; hashSlot++ {
		ciphertext := []byte(fmt.Sprintf("ciphertext-%d", hashSlot))
		cipherHash := sha256.Sum256(ciphertext)
		entry := backupartifact.ObjectEntry{
			Key:              fmt.Sprintf("objects/backup-12/%05d/metadata-000000.bin", hashSlot),
			Kind:             backupartifact.ObjectKindMetadata,
			HashSlot:         hashSlot,
			PlaintextSHA256:  strings.Repeat("a", 64),
			CiphertextSHA256: fmt.Sprintf("%x", cipherHash),
			PlaintextBytes:   1,
			CiphertextBytes:  int64(len(ciphertext)),
			Compression:      backupartifact.CompressionZstd,
			Encryption:       backupartifact.EncryptionAES256GCM,
			KMSKeyID:         "kms-backup",
			WrappedKey:       "d3JhcHBlZA==",
			Nonce:            "MDEyMzQ1Njc4OTAx",
		}
		for _, repository := range []backupartifact.Repository{primary, secondary} {
			require.NoError(t, repository.PutImmutable(context.Background(), entry.Key, int64(len(ciphertext)), entry.CiphertextSHA256, bytes.NewReader(ciphertext)))
		}
		committedAt := int64(1710000001000 + int64(hashSlot)*1000)
		if hashSlot == 0 {
			committedAt = 1710000000000
		}
		partition := backupartifact.PartitionManifest{
			Format:      backupartifact.PartitionManifestFormat,
			Version:     backupartifact.PartitionManifestVersion,
			JobID:       job.ID,
			BackupEpoch: job.Epoch,
			Cut:         backupartifact.PartitionCut{HashSlot: hashSlot, RaftIndex: uint64(90 + hashSlot), CommittedAtMillis: committedAt},
			Evidence: backupartifact.PartitionEvidence{
				Version: backupartifact.PartitionEvidenceVersion, MetadataRecords: uint64(hashSlot + 1), MessageRecords: 1, MaxMessageID: uint64(100 + hashSlot),
			},
			Objects: []backupartifact.ObjectEntry{entry},
		}
		body, err := backupartifact.MarshalPartitionManifest(partition)
		require.NoError(t, err)
		hash := sha256.Sum256(body)
		key := fmt.Sprintf("partition-manifests/%s/%05d.json", job.ID, hashSlot)
		require.NoError(t, manifestStore.Put(context.Background(), key, fmt.Sprintf("%x", hash), body))
		job.Partitions = append(job.Partitions, backupusecase.PartitionReport{
			JobID:                 job.ID,
			BackupEpoch:           job.Epoch,
			HashSlot:              hashSlot,
			RaftIndex:             partition.Cut.RaftIndex,
			CommittedAtUnixMillis: partition.Cut.CommittedAtMillis,
			ManifestKey:           key,
			ManifestSHA256:        fmt.Sprintf("%x", hash),
			ObjectCount:           1,
			CiphertextBytes:       uint64(len(ciphertext)),
		})
	}
	seed := sha256.Sum256([]byte("restore-point-publisher-test"))
	signer := testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	publisher, err := backupinfra.NewRestorePointPublisher(backupinfra.RestorePointPublisherOptions{
		Primary:            primary,
		Secondary:          secondary,
		Signer:             signer,
		SigningKeyID:       "signing-key",
		ApplicationVersion: "3.0.0-beta.1",
		RepositoryID:       "repo-prod",
		SourceClusterID:    "cluster-a",
		SourceGeneration:   "generation-1",
		Now:                func() time.Time { return time.UnixMilli(1710000010000).UTC() },
		NewRestorePointID:  func() string { return "restore-point-12" },
	})
	require.NoError(t, err)

	restorePoint, err := publisher.Publish(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, int64(1710000000000), restorePoint.EffectiveAtUnixMillis)
	require.True(t, restorePoint.PrimaryVerified)
	require.True(t, restorePoint.SecondaryVerified)
	loaded, err := backupartifact.LoadRestorePoint(context.Background(), secondary, restorePoint.ID, signer)
	require.NoError(t, err)
	require.Equal(t, uint64(100), loaded.Partitions[0].Evidence.MaxMessageID)
}

type testEd25519Signer struct {
	privateKey ed25519.PrivateKey
}

func (s testEd25519Signer) Sign(_ context.Context, keyID string, message []byte) (backupartifact.ManifestSignature, error) {
	return backupartifact.ManifestSignature{Algorithm: "ed25519", KeyID: keyID, Value: ed25519.Sign(s.privateKey, message)}, nil
}

func (s testEd25519Signer) Verify(_ context.Context, signature backupartifact.ManifestSignature, message []byte) error {
	if signature.Algorithm != "ed25519" || !ed25519.Verify(s.privateKey.Public().(ed25519.PublicKey), message, signature.Value) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}
