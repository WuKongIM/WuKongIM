package backup_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"

	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestWorkerPublishesPartitionManifestAfterBothStreamsReplicate(t *testing.T) {
	source := &fakePartitionSource{session: &fakePartitionSession{
		cut:      backupartifact.PartitionCut{HashSlot: 4, RaftIndex: 77, CommittedAtMillis: 1710000000000},
		metadata: "metadata-stream",
		messages: "message-stream",
	}}
	replicator := &fakeStreamReplicator{}
	manifests := &recordingPartitionManifestStore{}
	worker, err := backupruntime.NewWorker(backupruntime.WorkerOptions{Source: source, Replicator: replicator, Manifests: manifests})
	require.NoError(t, err)

	report, err := worker.Capture(context.Background(), backupruntime.CaptureRequest{
		JobID:             "backup-11",
		BackupEpoch:       11,
		HashSlot:          4,
		ConfigFingerprint: strings.Repeat("a", 64),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(77), report.RaftIndex)
	require.Equal(t, int64(1710000000000), report.CommittedAtUnixMillis)
	require.Equal(t, uint64(2), report.ObjectCount)
	require.Equal(t, "partition-manifests/backup-11/00004.json", report.ManifestKey)
	require.Len(t, manifests.bodies, 1)

	manifest, err := backupartifact.LoadPartitionManifest(manifests.bodies[0])
	require.NoError(t, err)
	require.Equal(t, uint16(4), manifest.Cut.HashSlot)
	require.Equal(t, backupartifact.ObjectKindMessages, manifest.Objects[0].Kind)
	require.Equal(t, backupartifact.ObjectKindMetadata, manifest.Objects[1].Kind)
	hash := sha256.Sum256(manifests.bodies[0])
	require.Equal(t, fmt.Sprintf("%x", hash), report.ManifestSHA256)
}

func TestDistributedWorkerCombinesDirectMessageShardReferences(t *testing.T) {
	plan := &fakeDistributedPlan{
		cut:      backupartifact.PartitionCut{HashSlot: 9, RaftIndex: 88, CommittedAtMillis: 1710000005000},
		metadata: "metadata-nine",
		shards: []backupruntime.MessageShard{
			{ID: "n1-0000", NodeID: 1, Channels: []backupruntime.ChannelFence{{ChannelID: "a", ChannelType: 2, LeaderNodeID: 1, ChannelEpoch: 1, LeaderEpoch: 1, MinISR: 1}}},
			{ID: "n2-0000", NodeID: 2, Channels: []backupruntime.ChannelFence{{ChannelID: "b", ChannelType: 2, LeaderNodeID: 2, ChannelEpoch: 1, LeaderEpoch: 1, MinISR: 1}}},
		},
	}
	messages := &fakeMessageShardCapturer{}
	manifests := &recordingPartitionManifestStore{}
	worker, err := backupruntime.NewDistributedWorker(backupruntime.DistributedWorkerOptions{
		Planner: &fakeDistributedPlanner{plan: plan}, Messages: messages,
		Replicator: &fakeStreamReplicator{}, Manifests: manifests,
	})
	require.NoError(t, err)
	report, err := worker.Capture(context.Background(), backupruntime.CaptureRequest{JobID: "backup-dist", BackupEpoch: 7, HashSlot: 9, ConfigFingerprint: strings.Repeat("c", 64)})
	require.NoError(t, err)
	require.Equal(t, uint64(4), report.ObjectCount)
	require.Equal(t, []string{"n1-0000", "n2-0000"}, messages.ids)
	manifest, err := backupartifact.LoadPartitionManifest(manifests.bodies[0])
	require.NoError(t, err)
	require.Len(t, manifest.Objects, 4)
	require.True(t, plan.closed)
}

type fakePartitionSource struct {
	session backupruntime.PartitionSession
}

func (s *fakePartitionSource) OpenPartition(context.Context, backupruntime.CaptureRequest) (backupruntime.PartitionSession, error) {
	return s.session, nil
}

type fakePartitionSession struct {
	cut      backupartifact.PartitionCut
	metadata string
	messages string
}

func (s *fakePartitionSession) Cut() backupartifact.PartitionCut { return s.cut }
func (s *fakePartitionSession) OpenMetadata(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s.metadata)), nil
}
func (s *fakePartitionSession) OpenMessages(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s.messages)), nil
}
func (s *fakePartitionSession) Close() error { return nil }

type fakeStreamReplicator struct{}

func (r *fakeStreamReplicator) Replicate(_ context.Context, descriptor backupruntime.StreamDescriptor, plaintext io.Reader) ([]backupartifact.ObjectEntry, error) {
	body, err := io.ReadAll(plaintext)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(body)
	stream := string(descriptor.Kind)
	if descriptor.ShardID != "" {
		stream += "-" + descriptor.ShardID
	}
	return []backupartifact.ObjectEntry{{
		Key:              fmt.Sprintf("objects/%s/%05d/%s-000000.bin", descriptor.JobID, descriptor.HashSlot, stream),
		Kind:             descriptor.Kind,
		HashSlot:         descriptor.HashSlot,
		PlaintextSHA256:  fmt.Sprintf("%x", hash),
		CiphertextSHA256: strings.Repeat("b", 64),
		PlaintextBytes:   int64(len(body)),
		CiphertextBytes:  int64(len(body)) + 16,
		Compression:      backupartifact.CompressionZstd,
		Encryption:       backupartifact.EncryptionAES256GCM,
		KMSKeyID:         "kms-backup",
		WrappedKey:       "d3JhcHBlZA==",
		Nonce:            "MDEyMzQ1Njc4OTAx",
	}}, nil
}

type fakeDistributedPlanner struct{ plan backupruntime.PartitionPlan }

func (p *fakeDistributedPlanner) OpenPlan(context.Context, backupruntime.CaptureRequest) (backupruntime.PartitionPlan, error) {
	return p.plan, nil
}

type fakeDistributedPlan struct {
	cut      backupartifact.PartitionCut
	metadata string
	shards   []backupruntime.MessageShard
	closed   bool
}

func (p *fakeDistributedPlan) Cut() backupartifact.PartitionCut { return p.cut }
func (p *fakeDistributedPlan) OpenMetadata(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(p.metadata)), nil
}
func (p *fakeDistributedPlan) MessageShards() []backupruntime.MessageShard { return p.shards }
func (p *fakeDistributedPlan) Base() *backupartifact.PartitionReference    { return nil }
func (p *fakeDistributedPlan) Close() error                                { p.closed = true; return nil }

type fakeMessageShardCapturer struct{ ids []string }

func (c *fakeMessageShardCapturer) CaptureMessageShard(_ context.Context, request backupruntime.CaptureRequest, shard backupruntime.MessageShard) (backupruntime.MessageShardCapture, error) {
	c.ids = append(c.ids, shard.ID)
	body := []byte(shard.ID)
	hash := sha256.Sum256(body)
	objects := []backupartifact.ObjectEntry{{
		Key: fmt.Sprintf("objects/%s/%05d/messages-%s-000000.bin", request.JobID, request.HashSlot, shard.ID), Kind: backupartifact.ObjectKindMessages, HashSlot: request.HashSlot,
		PlaintextSHA256: fmt.Sprintf("%x", hash), CiphertextSHA256: strings.Repeat("d", 64), PlaintextBytes: int64(len(body)), CiphertextBytes: int64(len(body)) + 16,
		Compression: backupartifact.CompressionZstd, Encryption: backupartifact.EncryptionAES256GCM, KMSKeyID: "kms-backup", WrappedKey: "d3JhcHBlZA==", Nonce: "MDEyMzQ1Njc4OTAx",
	}}
	boundaries := make([]backupartifact.ChannelBoundary, len(shard.Channels))
	for index, channel := range shard.Channels {
		boundaries[index] = backupartifact.ChannelBoundary{ChannelID: channel.ChannelID, ChannelType: channel.ChannelType, Epoch: channel.ChannelEpoch}
	}
	return backupruntime.MessageShardCapture{Objects: objects, Boundaries: boundaries}, nil
}

type recordingPartitionManifestStore struct {
	keys   []string
	bodies [][]byte
}

func (s *recordingPartitionManifestStore) Put(_ context.Context, key, _ string, body []byte) error {
	s.keys = append(s.keys, key)
	s.bodies = append(s.bodies, append([]byte(nil), body...))
	return nil
}
