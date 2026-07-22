package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestRestoreInspectorLoadsSelectedRepositoryAndPreservesExactEstimates(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	signer := restoreInspectorSigner()
	publishDirectRestorePoint(t, primary, secondary, signer, "restore-1", 1710000000000, 37)
	target := staticRestoreTarget{state: backupinfra.RestoreTargetState{
		ClusterID: "cluster-b", Generation: "generation-2", HashSlotCount: 1, Empty: true,
	}}
	inspector, err := backupinfra.NewRestoreInspector(backupinfra.RestoreInspectorOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: restoreInspectorCodec(), RepositoryID: "repo-prod", Target: target,
	})
	require.NoError(t, err)

	inspection, err := inspector.Inspect(context.Background(), backupusecase.RestorePlanRequest{
		RestorePointID: "restore-1", Repository: "secondary",
	})
	require.NoError(t, err)
	require.Equal(t, "restore-1", inspection.RestorePointID)
	require.Equal(t, "cluster-a", inspection.SourceClusterID)
	require.Equal(t, "cluster-b", inspection.TargetClusterID)
	require.True(t, inspection.TargetEmpty)
	require.NotNil(t, inspection.EstimatedPlainBytes)
	require.NotNil(t, inspection.EstimatedCipherBytes)
	require.Equal(t, uint64(37), *inspection.EstimatedPlainBytes)
	require.Equal(t, uint64(len("ciphertext-restore-1")), *inspection.EstimatedCipherBytes)
	require.Len(t, inspection.ManifestSHA256, 64)
	require.Equal(t, backupartifact.ErasureLedgerSnapshotVersion, inspection.ErasureLedgerVersion)
	require.Zero(t, inspection.ErasureLedgerBoundary)
	require.Equal(t, backupartifact.EmptyErasureLedgerSnapshotSHA256, inspection.ErasureLedgerSHA256)
}

func TestRestoreInspectorLatestVerifiedRequiresIdenticalCompleteCopies(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	temporary, err := backupinfra.NewFileRepository("temporary", t.TempDir())
	require.NoError(t, err)
	signer := restoreInspectorSigner()
	publishDirectRestorePoint(t, primary, secondary, signer, "restore-common", 1710000000000, 11)
	publishDirectRestorePoint(t, primary, temporary, signer, "restore-primary-only", 1710000010000, 12)
	inspector, err := backupinfra.NewRestoreInspector(backupinfra.RestoreInspectorOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: restoreInspectorCodec(), RepositoryID: "repo-prod",
		Target: staticRestoreTarget{state: backupinfra.RestoreTargetState{ClusterID: "cluster-b", Generation: "generation-2", HashSlotCount: 1, Empty: true}},
	})
	require.NoError(t, err)

	inspection, err := inspector.Inspect(context.Background(), backupusecase.RestorePlanRequest{
		LatestVerified: true, Repository: "primary",
	})
	require.NoError(t, err)
	require.Equal(t, "restore-common", inspection.RestorePointID)
}

func TestRestoreInspectorRejectsNonEmptyOrIdentityCompatibleTarget(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	signer := restoreInspectorSigner()
	publishDirectRestorePoint(t, primary, secondary, signer, "restore-unsafe", 1710000000000, 1)

	for name, state := range map[string]backupinfra.RestoreTargetState{
		"non-empty":       {ClusterID: "cluster-b", Generation: "generation-2", HashSlotCount: 1, Empty: false},
		"same-cluster":    {ClusterID: "cluster-a", Generation: "generation-2", HashSlotCount: 1, Empty: true},
		"same-generation": {ClusterID: "cluster-b", Generation: "generation-1", HashSlotCount: 1, Empty: true},
		"slot-mismatch":   {ClusterID: "cluster-b", Generation: "generation-2", HashSlotCount: 2, Empty: true},
	} {
		t.Run(name, func(t *testing.T) {
			inspector, err := backupinfra.NewRestoreInspector(backupinfra.RestoreInspectorOptions{
				Primary: primary, Secondary: secondary, Signer: signer, Codec: restoreInspectorCodec(), RepositoryID: "repo-prod", Target: staticRestoreTarget{state: state},
			})
			require.NoError(t, err)
			_, err = inspector.Inspect(context.Background(), backupusecase.RestorePlanRequest{RestorePointID: "restore-unsafe", Repository: "primary"})
			require.Error(t, err)
		})
	}
}

type staticRestoreTarget struct {
	state backupinfra.RestoreTargetState
	err   error
}

func (t staticRestoreTarget) InspectRestoreTarget(context.Context) (backupinfra.RestoreTargetState, error) {
	return t.state, t.err
}

func restoreInspectorSigner() testEd25519Signer {
	seed := sha256.Sum256([]byte("restore-inspector-test"))
	return testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
}

func restoreInspectorCodec() *backupartifact.ObjectCodec {
	return backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x11}, 64)))
}

func publishDirectRestorePoint(t *testing.T, primary, secondary backupartifact.Repository, signer backupartifact.ManifestSigner, restorePointID string, createdAt int64, plaintextBytes int64) {
	t.Helper()
	body := []byte("ciphertext-" + restorePointID)
	cipherHash := sha256.Sum256(body)
	entry := backupartifact.ObjectEntry{
		Key: "objects/" + restorePointID + "/00000/metadata-000000.bin", Kind: backupartifact.ObjectKindMetadata,
		HashSlot: 0, PlaintextSHA256: hex.EncodeToString(make([]byte, sha256.Size)), CiphertextSHA256: hex.EncodeToString(cipherHash[:]),
		PlaintextBytes: plaintextBytes, CiphertextBytes: int64(len(body)), Compression: backupartifact.CompressionZstd,
		Encryption: backupartifact.EncryptionAES256GCM, KMSKeyID: "kms-backup", WrappedKey: "d3JhcHBlZA==", Nonce: "MDEyMzQ1Njc4OTAx",
	}
	manifest := backupartifact.Manifest{
		Format: backupartifact.ManifestFormat, Version: backupartifact.ManifestVersion, ApplicationVersion: "test",
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1",
		RestorePointID: restorePointID, BackupEpoch: uint64(createdAt), Kind: backupartifact.RestorePointMaterializedFull,
		HashSlotCount: 1, CreatedAtUnixMillis: createdAt, EffectiveAtMillis: createdAt,
		Cuts: []backupartifact.PartitionCut{{HashSlot: 0, RaftIndex: 1, CommittedAtMillis: createdAt}}, Objects: []backupartifact.ObjectEntry{entry},
	}
	_, err := backupartifact.NewReplicatedPublisher(primary, secondary).Publish(context.Background(), manifest, []backupartifact.SealedObject{{Entry: entry, Ciphertext: bytes.Clone(body)}}, signer, "signing-key")
	require.NoError(t, err)
}
