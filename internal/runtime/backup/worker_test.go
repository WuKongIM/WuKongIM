package backup_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
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
	require.Equal(t, backupartifact.PartitionEvidence{
		Version: backupartifact.PartitionEvidenceVersion, MetadataRecords: 5, MessageRecords: 2, MaxMessageID: 19,
	}, manifest.Evidence)
	require.Equal(t, uint16(4), manifest.Cut.HashSlot)
	require.Equal(t, backupartifact.ObjectKindMessages, manifest.Objects[0].Kind)
	require.Equal(t, backupartifact.ObjectKindMetadata, manifest.Objects[1].Kind)
	hash := sha256.Sum256(manifests.bodies[0])
	require.Equal(t, fmt.Sprintf("%x", hash), report.ManifestSHA256)
	retried, err := worker.Capture(context.Background(), backupruntime.CaptureRequest{
		JobID: "backup-11", BackupEpoch: 11, HashSlot: 4, ConfigFingerprint: strings.Repeat("a", 64),
	})
	require.NoError(t, err)
	require.Equal(t, report, retried)
	require.Equal(t, 1, source.opens)
	require.Len(t, manifests.bodies, 1)
}

func TestDistributedWorkerCombinesDirectMessageShardReferences(t *testing.T) {
	plan := &fakeDistributedPlan{
		cut: backupartifact.PartitionCut{
			HashSlot: 9, PhysicalSlotID: 10, RaftIndex: 88, CommittedAtMillis: 1710000005000,
		},
		metadata: "metadata-nine",
		base: &backupartifact.PartitionReference{
			HashSlot: 9, Key: "partition-manifests/base/00009.json", SHA256: strings.Repeat("e", 64), Bytes: 10,
			ObjectCount: 1, CiphertextBytes: 10,
			Evidence: backupartifact.PartitionEvidence{Version: backupartifact.PartitionEvidenceVersion, MessageRecords: 10, MaxMessageID: 150},
		},
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
	require.Equal(t, backupartifact.PartitionEvidence{
		Version: backupartifact.PartitionEvidenceVersion, MetadataRecords: 13, MessageRecords: 12, MaxMessageID: 200,
	}, manifest.Evidence)
	require.True(t, plan.closed)
}

func TestDistributedBaselineCapturerCommitsCompleteCursorBeforeImmutableManifest(t *testing.T) {
	events := make([]string, 0, 2)
	plan := &fakeDistributedPlan{
		cut: backupartifact.PartitionCut{
			HashSlot: 9, PhysicalSlotID: 10, RaftIndex: 88, CommittedAtMillis: 1710000005000,
		},
		metadata: "metadata-nine",
		shards: []backupruntime.MessageShard{{
			ID: "n1-0000", NodeID: 1,
			Channels: []backupruntime.ChannelFence{{
				ChannelID: "room-a", ChannelType: 2, LeaderNodeID: 1,
				ChannelEpoch: 7, LeaderEpoch: 3, MinISR: 1,
			}},
		}},
	}
	manifests := &recordingPartitionManifestStore{
		beforePut: func() { events = append(events, "manifest") },
	}
	worker, err := backupruntime.NewDistributedWorker(backupruntime.DistributedWorkerOptions{
		Planner: &fakeDistributedPlanner{plan: plan}, Messages: &fakeMessageShardCapturer{},
		Replicator: &fakeStreamReplicator{}, Manifests: manifests,
	})
	require.NoError(t, err)
	segments := &recordingBaselineSegmentCommitter{
		onCommit: func() { events = append(events, "cursor") },
	}
	capturer, err := backupruntime.NewDistributedBaselineCapturer(backupruntime.MaterializedBaselineOptions{
		Worker: worker, Segments: segments, RepositoryID: "repository-a",
		SourceClusterID: "cluster-a", SourceGeneration: "source-1", KMSKeyID: "kms-backup",
	})
	require.NoError(t, err)
	lease := backupcontract.SlotCaptureLease{
		SlotID: 10, LeaderTerm: 7, ConfigEpoch: 3, HolderNodeID: 1,
		Generation: "slot-generation-1", Sequence: 1, AcquiredAtUnixMillis: 1710000000000,
	}

	pinCut := func(_ context.Context, cut uint64) error {
		require.Equal(t, uint64(88), cut)
		events = append(events, "pin")
		return nil
	}
	baseline, err := capturer.CaptureBaseline(
		context.Background(), 9, "rebase-00009-00000000000000000002", 2, lease, pinCut,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"pin", "cursor", "manifest"}, events)
	require.Equal(t, uint64(88), baseline.Metadata.SourceHighWatermark)
	require.NotNil(t, baseline.Messages.BaselineCursorHead)
	require.Equal(t, backupartifact.SegmentStreamMessageBaselineCursor, segments.descriptor.Logical.Stream)
	require.Equal(t, uint64(1), segments.descriptor.Logical.RecordCount)
	cursor, err := backupartifact.LoadMessageCursorBatch(segments.body)
	require.NoError(t, err)
	require.Equal(t, uint16(9), cursor.HashSlot)
	require.Equal(t, "rebase-00009-00000000000000000002", cursor.Generation)
	require.Equal(t, uint64(1), cursor.Sequence)
	require.True(t, cursor.Checkpoint)
	require.Nil(t, cursor.Previous)
	require.Equal(t, "baseline-88", cursor.NextCursor)
	require.Equal(t, uint64(88), cursor.SourceHighWatermark)
	require.Equal(t, int64(1710000005000), cursor.WatermarkAtUnixMillis)
	require.Equal(t, []backupartifact.ChannelBoundary{{
		ChannelID: "room-a", ChannelType: 2, Epoch: 7,
	}}, cursor.Boundaries)
	manifest, err := backupartifact.LoadPartitionManifest(manifests.bodies[0])
	require.NoError(t, err)
	require.NotNil(t, manifest.BaselineCursor)
	require.Nil(t, manifest.Base)
	require.Equal(t, baseline.Messages.BaselineCursorHead, manifest.BaselineCursor)
	require.Equal(t, baseline.Reference.Partition.Key, manifests.keys[0])

	retried, err := capturer.CaptureBaseline(
		context.Background(), 9, "rebase-00009-00000000000000000002", 2, lease, pinCut,
	)
	require.NoError(t, err)
	require.Equal(t, baseline, retried)
	require.Equal(t, []string{"pin", "cursor", "manifest", "pin"}, events, "retry must load the immutable published baseline")

	remappedLease := lease
	remappedLease.SlotID = 11
	_, err = capturer.CaptureBaseline(
		context.Background(), 9, "rebase-00009-00000000000000000002", 2, remappedLease, pinCut,
	)
	require.ErrorIs(t, err, backupruntime.ErrStaleCapture)
	require.Equal(t, []string{"pin", "cursor", "manifest", "pin"}, events, "stale physical index space must be rejected before pinning")
}

func TestDistributedBaselineCapturerCommitsEmptyCompleteCursor(t *testing.T) {
	plan := &fakeDistributedPlan{
		cut: backupartifact.PartitionCut{
			HashSlot: 12, PhysicalSlotID: 13, RaftIndex: 89,
			CommittedAtMillis: 1710000006000,
		},
		metadata: "metadata-twelve",
	}
	worker, err := backupruntime.NewDistributedWorker(backupruntime.DistributedWorkerOptions{
		Planner:    &fakeDistributedPlanner{plan: plan},
		Messages:   &fakeMessageShardCapturer{},
		Replicator: &fakeStreamReplicator{},
		Manifests:  &recordingPartitionManifestStore{},
	})
	require.NoError(t, err)
	segments := &recordingBaselineSegmentCommitter{}
	capturer, err := backupruntime.NewDistributedBaselineCapturer(backupruntime.MaterializedBaselineOptions{
		Worker: worker, Segments: segments, RepositoryID: "repository-a",
		SourceClusterID: "cluster-a", SourceGeneration: "source-1", KMSKeyID: "kms-backup",
	})
	require.NoError(t, err)
	lease := backupcontract.SlotCaptureLease{
		SlotID: 13, LeaderTerm: 7, ConfigEpoch: 3, HolderNodeID: 1,
		Generation: "slot-generation-1", Sequence: 1,
		AcquiredAtUnixMillis: 1710000000000,
	}

	_, err = capturer.CaptureBaseline(
		context.Background(), 12, "rebase-00012-00000000000000000002",
		2, lease, func(context.Context, uint64) error { return nil },
	)
	require.NoError(t, err)
	cursor, err := backupartifact.LoadMessageCursorBatch(segments.body)
	require.NoError(t, err)
	require.True(t, cursor.Checkpoint)
	require.Empty(t, cursor.Boundaries)
	require.Equal(t, uint64(89), cursor.SourceHighWatermark)
}

type fakePartitionSource struct {
	session backupruntime.PartitionSession
	opens   int
}

func (s *fakePartitionSource) OpenPartition(context.Context, backupruntime.CaptureRequest) (backupruntime.PartitionSession, error) {
	s.opens++
	return s.session, nil
}

type fakePartitionSession struct {
	cut      backupartifact.PartitionCut
	metadata string
	messages string
}

func (s *fakePartitionSession) Evidence() backupartifact.PartitionEvidence {
	return backupartifact.PartitionEvidence{Version: backupartifact.PartitionEvidenceVersion, MetadataRecords: 5, MessageRecords: 2, MaxMessageID: 19}
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
	base     *backupartifact.PartitionReference
	closed   bool
}

func (p *fakeDistributedPlan) Cut() backupartifact.PartitionCut { return p.cut }
func (p *fakeDistributedPlan) OpenMetadata(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(p.metadata)), nil
}
func (p *fakeDistributedPlan) MessageShards() []backupruntime.MessageShard { return p.shards }
func (p *fakeDistributedPlan) MetadataRecordCount() uint64                 { return 13 }
func (p *fakeDistributedPlan) Base() *backupartifact.PartitionReference    { return p.base }
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
	return backupruntime.MessageShardCapture{
		Objects: objects, Boundaries: boundaries, MessageRecords: uint64(len(shard.Channels)), MaxMessageID: uint64(len(c.ids) * 100),
	}, nil
}

type recordingPartitionManifestStore struct {
	keys      []string
	checksums []string
	bodies    [][]byte
	beforePut func()
}

func (s *recordingPartitionManifestStore) Load(_ context.Context, key string) ([]byte, string, error) {
	for index := len(s.keys) - 1; index >= 0; index-- {
		if s.keys[index] == key {
			return append([]byte(nil), s.bodies[index]...), s.checksums[index], nil
		}
	}
	return nil, "", backupartifact.ErrObjectNotFound
}

func (s *recordingPartitionManifestStore) Put(_ context.Context, key, checksum string, body []byte) error {
	if s.beforePut != nil {
		s.beforePut()
	}
	s.keys = append(s.keys, key)
	s.checksums = append(s.checksums, checksum)
	s.bodies = append(s.bodies, append([]byte(nil), body...))
	return nil
}

type recordingBaselineSegmentCommitter struct {
	descriptor backupartifact.SegmentDescriptor
	body       []byte
	onCommit   func()
}

func (c *recordingBaselineSegmentCommitter) Commit(
	_ context.Context,
	descriptor backupartifact.SegmentDescriptor,
	body []byte,
) (backupartifact.SegmentReference, error) {
	if c.onCommit != nil {
		c.onCommit()
	}
	c.descriptor = descriptor
	c.body = append([]byte(nil), body...)
	sum := sha256.Sum256(body)
	id := fmt.Sprintf("%x", sum)
	return backupartifact.SegmentReference{
		SegmentID: id, CommitKey: "segments/" + id + "/commit.json",
		CommitSHA256: strings.Repeat("f", 64), PlaintextBytes: int64(len(body)),
	}, nil
}
