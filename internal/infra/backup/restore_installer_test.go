package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"
)

func TestLocalRestoreInstallerReassemblesChunkedPartitionStreams(t *testing.T) {
	ctx := context.Background()
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	codec := backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x41}, 1024)))
	replicator, err := backupinfra.NewChunkReplicator(backupinfra.ChunkReplicatorOptions{
		Codec: codec, Publisher: backupartifact.NewReplicatedPublisher(primary, secondary), KMSKeyID: "kms-backup", ChunkBytes: 4,
	})
	require.NoError(t, err)
	metadataBody := []byte("metadata-stream")
	messageBody := []byte("message-stream")
	metadata, err := replicator.Replicate(ctx, backupinfra.StreamDescriptor{JobID: "restore-job", HashSlot: 0, Kind: backupartifact.ObjectKindMetadata}, bytes.NewReader(metadataBody))
	require.NoError(t, err)
	messages, err := replicator.Replicate(ctx, backupinfra.StreamDescriptor{JobID: "restore-job", HashSlot: 0, Kind: backupartifact.ObjectKindMessages, ShardID: "n1-0000"}, bytes.NewReader(messageBody))
	require.NoError(t, err)
	channelIndexBody, err := backupartifact.MarshalChannelIndex(0, []backupartifact.ChannelBoundary{{
		ChannelID: "room", ChannelType: 2, Epoch: 7, LogStartOffset: 3, HW: 9,
	}})
	require.NoError(t, err)
	channelIndex, err := replicator.Replicate(ctx, backupinfra.StreamDescriptor{JobID: "restore-job", HashSlot: 0, Kind: backupartifact.ObjectKindChannelIndex}, bytes.NewReader(channelIndexBody))
	require.NoError(t, err)
	objects := append(metadata, messages...)
	objects = append(objects, channelIndex...)
	sort.Slice(objects, func(left, right int) bool { return objects[left].Key < objects[right].Key })
	partition := backupartifact.PartitionManifest{
		Format: backupartifact.PartitionManifestFormat, Version: backupartifact.PartitionManifestVersion,
		JobID: "restore-job", BackupEpoch: 9,
		Cut:      backupartifact.PartitionCut{HashSlot: 0, RaftIndex: 7, CommittedAtMillis: 1710000000000},
		Evidence: backupartifact.PartitionEvidence{Version: backupartifact.PartitionEvidenceVersion, MetadataRecords: 3, MessageRecords: 2, MaxMessageID: 19},
		Objects:  objects,
	}
	partitionBody, err := backupartifact.MarshalPartitionManifest(partition)
	require.NoError(t, err)
	partitionHash := sha256.Sum256(partitionBody)
	partitionKey := "partition-manifests/restore-job/00000.json"
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
	seed := sha256.Sum256([]byte("local-restore-installer"))
	signer := testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	manifest := backupartifact.Manifest{
		Format: backupartifact.ManifestFormat, Version: backupartifact.ManifestVersion, ApplicationVersion: "test",
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1",
		RestorePointID: "restore-point", BackupEpoch: 9, Kind: backupartifact.RestorePointMaterializedFull,
		HashSlotCount: 1, CreatedAtUnixMillis: 1710000000000, EffectiveAtMillis: 1710000000000,
		Cuts: []backupartifact.PartitionCut{partition.Cut}, Partitions: []backupartifact.PartitionReference{reference},
	}
	signed, err := backupartifact.NewReplicatedPublisher(primary, secondary).PublishReferences(ctx, manifest, signer, "signing-key")
	require.NoError(t, err)
	manifestBody, err := backupartifact.MarshalManifest(signed)
	require.NoError(t, err)
	manifestHash := sha256.Sum256(manifestBody)
	node := &recordingRestoreInstallNode{}
	installer, err := backupinfra.NewLocalRestoreInstaller(backupinfra.LocalRestoreInstallerOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: codec, Node: node,
		StagingDir: t.TempDir(), StagingMaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	result, err := installer.InstallPartition(ctx, backupusecase.RestorePlan{
		ID: "plan-1", RestorePointID: signed.RestorePointID, ManifestSHA256: hex.EncodeToString(manifestHash[:]), Repository: "secondary",
		SourceClusterID: signed.SourceClusterID, SourceGeneration: signed.SourceGeneration, HashSlotCount: 1,
	}, 0)
	require.NoError(t, err)
	require.True(t, result.Installed)
	require.Equal(t, backupartifact.PartitionEvidenceVersion, result.EvidenceVersion)
	require.Equal(t, strings.Repeat("c", 64), result.MetadataSHA256)
	require.Equal(t, uint64(2), result.MessageCount)
	require.Equal(t, uint64(3), result.MetadataRecordCount)
	require.Equal(t, uint64(19), result.MaxMessageID)
	require.Equal(t, metadataBody, node.metadata)
	require.Equal(t, messageBody, node.messages)
	require.Equal(t, []clusterpkg.RestoreVerifyBoundary{{
		ChannelID: "room", ChannelType: 2, Epoch: 7, LogStartOffset: 3, HW: 9,
	}}, node.boundaries)
}

type recordingRestoreInstallNode struct {
	metadata   []byte
	messages   []byte
	boundaries []clusterpkg.RestoreVerifyBoundary
}

func (n *recordingRestoreInstallNode) InstallRestoreHashSlotMetadata(_ context.Context, _ uint16, reader io.ReadSeeker, size int64, _ bool) (uint64, error) {
	n.metadata = readExactRestoreTestStream(reader, size)
	return 3, nil
}

func (n *recordingRestoreInstallNode) InstallRestoreMessageStream(_ context.Context, reader io.ReadSeeker, size int64) (channelstore.BackupSnapshotStats, error) {
	n.messages = append(n.messages, readExactRestoreTestStream(reader, size)...)
	return channelstore.BackupSnapshotStats{HashSlot: 0, ChannelCount: 1, MessageCount: 2, MaxMessageID: 19}, nil
}

func (n *recordingRestoreInstallNode) InstallRestoreChannelRuntimeMeta(_ context.Context, _ uint16, boundaries []clusterpkg.RestoreVerifyBoundary) error {
	n.boundaries = append(n.boundaries, boundaries...)
	return nil
}

func (n *recordingRestoreInstallNode) RestoreHashSlotMetadataDigest(context.Context, uint16) (string, error) {
	return strings.Repeat("c", 64), nil
}

func readExactRestoreTestStream(reader io.ReadSeeker, size int64) []byte {
	body, _ := io.ReadAll(io.LimitReader(reader, size+1))
	return body
}
