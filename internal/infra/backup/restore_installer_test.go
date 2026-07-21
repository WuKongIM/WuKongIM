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
	objects := append(metadata, messages...)
	sort.Slice(objects, func(left, right int) bool { return objects[left].Key < objects[right].Key })
	partition := backupartifact.PartitionManifest{
		Format: backupartifact.PartitionManifestFormat, Version: backupartifact.PartitionManifestVersion,
		JobID: "restore-job", BackupEpoch: 9,
		Cut: backupartifact.PartitionCut{HashSlot: 0, RaftIndex: 7, CommittedAtMillis: 1710000000000}, Objects: objects,
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
		ObjectCount: uint64(len(objects)), CiphertextBytes: ciphertextBytes,
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
	require.Equal(t, strings.Repeat("c", 64), result.MetadataSHA256)
	require.Equal(t, uint64(2), result.MessageCount)
	require.Equal(t, metadataBody, node.metadata)
	require.Equal(t, messageBody, node.messages)
}

type recordingRestoreInstallNode struct {
	metadata []byte
	messages []byte
}

func (n *recordingRestoreInstallNode) InstallRestoreHashSlotMetadata(_ context.Context, _ uint16, reader io.ReadSeeker, size int64, _ bool) error {
	n.metadata = readExactRestoreTestStream(reader, size)
	return nil
}

func (n *recordingRestoreInstallNode) InstallRestoreMessageStream(_ context.Context, reader io.ReadSeeker, size int64) (channelstore.BackupSnapshotStats, error) {
	n.messages = append(n.messages, readExactRestoreTestStream(reader, size)...)
	return channelstore.BackupSnapshotStats{HashSlot: 0, ChannelCount: 1, MessageCount: 2}, nil
}

func (n *recordingRestoreInstallNode) RestoreHashSlotMetadataDigest(context.Context, uint16) (string, error) {
	return strings.Repeat("c", 64), nil
}

func readExactRestoreTestStream(reader io.ReadSeeker, size int64) []byte {
	body, _ := io.ReadAll(io.LimitReader(reader, size+1))
	return body
}
