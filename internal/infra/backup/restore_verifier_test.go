package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/stretchr/testify/require"
)

func TestClusterRestoreVerifierChecksEveryCurrentNodeAgainstAuthenticatedIndex(t *testing.T) {
	ctx := context.Background()
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	codec := backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x4c}, bytes.NewReader(bytes.Repeat([]byte{0x38}, 1024)))
	replicator, err := backupinfra.NewChunkReplicator(backupinfra.ChunkReplicatorOptions{
		Codec: codec, Publisher: backupartifact.NewReplicatedPublisher(primary, secondary), KMSKeyID: "kms-backup", ChunkBytes: 8,
	})
	require.NoError(t, err)
	indexBody, err := backupartifact.MarshalChannelIndex(0, []backupartifact.ChannelBoundary{{
		ChannelID: "room", ChannelType: 2, Epoch: 3, LogStartOffset: 1, HW: 8,
	}})
	require.NoError(t, err)
	objects, err := replicator.Replicate(ctx, backupinfra.StreamDescriptor{JobID: "verify-job", HashSlot: 0, Kind: backupartifact.ObjectKindChannelIndex}, bytes.NewReader(indexBody))
	require.NoError(t, err)
	metadataObjects, err := replicator.Replicate(ctx, backupinfra.StreamDescriptor{JobID: "verify-job", HashSlot: 0, Kind: backupartifact.ObjectKindMetadata}, bytes.NewReader([]byte("metadata")))
	require.NoError(t, err)
	objects = append(objects, metadataObjects...)
	sort.Slice(objects, func(left, right int) bool { return objects[left].Key < objects[right].Key })
	partition := backupartifact.PartitionManifest{
		Format: backupartifact.PartitionManifestFormat, Version: backupartifact.PartitionManifestVersion,
		JobID: "verify-job", BackupEpoch: 4, Cut: backupartifact.PartitionCut{HashSlot: 0, RaftIndex: 5, CommittedAtMillis: 1710000000000},
		Evidence: backupartifact.PartitionEvidence{Version: backupartifact.PartitionEvidenceVersion, MetadataRecords: 7, MessageRecords: 3, MaxMessageID: 101},
		Objects:  objects,
	}
	partitionBody, err := backupartifact.MarshalPartitionManifest(partition)
	require.NoError(t, err)
	partitionHash := sha256.Sum256(partitionBody)
	partitionKey := "partition-manifests/verify-job/00000.json"
	manifestStore, err := backupinfra.NewReplicatedManifestStore(primary, secondary)
	require.NoError(t, err)
	require.NoError(t, manifestStore.Put(ctx, partitionKey, hex.EncodeToString(partitionHash[:]), partitionBody))
	var ciphertextBytes uint64
	for _, object := range objects {
		ciphertextBytes += uint64(object.CiphertextBytes)
	}
	reference := backupartifact.PartitionReference{
		HashSlot: 0, Key: partitionKey, SHA256: hex.EncodeToString(partitionHash[:]), Bytes: int64(len(partitionBody)),
		ObjectCount: uint64(len(objects)), CiphertextBytes: ciphertextBytes, Evidence: partition.Evidence,
	}
	seed := sha256.Sum256([]byte("cluster-restore-verifier"))
	signer := testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	manifest := backupartifact.Manifest{
		Format: backupartifact.ManifestFormat, Version: backupartifact.ManifestVersion, ApplicationVersion: "test",
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1",
		RestorePointID: "restore-point", BackupEpoch: 4, Kind: backupartifact.RestorePointIncremental,
		HashSlotCount: 1, CreatedAtUnixMillis: 1710000000000, EffectiveAtMillis: 1710000000000,
		Cuts: []backupartifact.PartitionCut{partition.Cut}, Partitions: []backupartifact.PartitionReference{reference},
	}
	signed, err := backupartifact.NewReplicatedPublisher(primary, secondary).PublishReferences(ctx, manifest, signer, "signing-key")
	require.NoError(t, err)
	manifestBody, err := backupartifact.MarshalManifest(signed)
	require.NoError(t, err)
	manifestHash := sha256.Sum256(manifestBody)
	metadataDigest := strings.Repeat("d", 64)
	node := &fakeRestoreVerificationNode{nodeID: 1, snapshot: control.Snapshot{
		ClusterID: "cluster-b", HashSlots: control.HashSlotTable{Count: 1},
		Nodes: []control.Node{{NodeID: 1, JoinState: control.NodeJoinStateActive}, {NodeID: 2, JoinState: control.NodeJoinStateActive}},
	}}
	remote := &fakeRemoteRestoreVerifier{}
	verifier, err := backupinfra.NewClusterRestoreVerifier(backupinfra.ClusterRestoreVerifierOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: codec, Node: node, Remote: remote, MaxParallel: 1,
	})
	require.NoError(t, err)
	reports, err := verifier.VerifyRestore(ctx, backupusecase.RestorePlan{
		ID: "plan-1", RestorePointID: signed.RestorePointID, ManifestSHA256: hex.EncodeToString(manifestHash[:]), Repository: "primary",
		SourceClusterID: signed.SourceClusterID, SourceGeneration: signed.SourceGeneration,
		TargetClusterID: "cluster-b", HashSlotCount: 1,
		Partitions: []backupusecase.RestorePartition{{
			HashSlot: 0, EvidenceVersion: backupartifact.PartitionEvidenceVersion, Installed: true, PlainBytes: 99, MetadataRecordCount: 7, MessageCount: 3, MaxMessageID: 101, MetadataSHA256: metadataDigest,
		}},
	})
	require.NoError(t, err)
	require.True(t, reports[0].Verified)
	require.Equal(t, uint64(99), reports[0].PlainBytes)
	require.Equal(t, []uint16{0}, node.hashSlots)
	require.Equal(t, []string{metadataDigest}, node.metadataDigests)
	require.Equal(t, []uint64{2}, remote.nodeIDs)
	require.Equal(t, []string{metadataDigest}, remote.metadataDigests)
	require.Equal(t, "room", remote.boundaries[0].ChannelID)

	_, err = verifier.VerifyRestore(ctx, backupusecase.RestorePlan{
		ID: "plan-2", RestorePointID: signed.RestorePointID, ManifestSHA256: hex.EncodeToString(manifestHash[:]), Repository: "primary",
		SourceClusterID: signed.SourceClusterID, SourceGeneration: signed.SourceGeneration,
		TargetClusterID: "cluster-b", HashSlotCount: 1,
		Partitions: []backupusecase.RestorePartition{{
			HashSlot: 0, EvidenceVersion: backupartifact.PartitionEvidenceVersion, Installed: true, MetadataRecordCount: 7, MessageCount: 2, MaxMessageID: 101, MetadataSHA256: metadataDigest,
		}},
	})
	require.Error(t, err)
}

type fakeRestoreVerificationNode struct {
	mu              sync.Mutex
	nodeID          uint64
	snapshot        control.Snapshot
	hashSlots       []uint16
	metadataDigests []string
}

func (f *fakeRestoreVerificationNode) NodeID() uint64 { return f.nodeID }
func (f *fakeRestoreVerificationNode) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeRestoreVerificationNode) VerifyLocalRestorePartition(_ context.Context, hashSlot uint16, metadataSHA256 string, _ []clusterpkg.RestoreVerifyBoundary) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hashSlots = append(f.hashSlots, hashSlot)
	f.metadataDigests = append(f.metadataDigests, metadataSHA256)
	return nil
}

type fakeRemoteRestoreVerifier struct {
	mu              sync.Mutex
	nodeIDs         []uint64
	metadataDigests []string
	boundaries      []clusterpkg.RestoreVerifyBoundary
}

func (f *fakeRemoteRestoreVerifier) VerifyBackupRestorePartition(_ context.Context, nodeID uint64, _ uint16, metadataSHA256 string, boundaries []clusterpkg.RestoreVerifyBoundary) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodeIDs = append(f.nodeIDs, nodeID)
	f.metadataDigests = append(f.metadataDigests, metadataSHA256)
	f.boundaries = append(f.boundaries, boundaries...)
	return nil
}
