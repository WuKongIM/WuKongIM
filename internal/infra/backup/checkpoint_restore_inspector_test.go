package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestCheckpointRestoreInspectorPinsCatalogProofAndAuditsCurrentGraph(
	t *testing.T,
) {
	fixture := newCheckpointRestoreInspectorFixture(t)
	first := fixture.publish(
		t, "checkpoint-restore-1", 1_753_400_200_000, nil,
	)
	second := fixture.publish(
		t, "checkpoint-restore-2", 1_753_400_300_000, &first.Head,
	)
	inspector := fixture.inspector(t)

	inspection, err := inspector.Inspect(
		context.Background(),
		backupusecase.RestorePlanRequest{
			RestorePointID: "checkpoint-restore-1",
			Repository:     "primary", CatalogHead: &second.Head,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "checkpoint-restore-1", inspection.RestorePointID)
	require.NotNil(t, inspection.CatalogProof)
	require.Equal(t, second.Head, inspection.CatalogProof.Head)
	require.Equal(t, first.Head, inspection.CatalogProof.EntryPage)
	require.Equal(t, first.Checkpoint, inspection.CatalogProof.Checkpoint)
	require.Equal(t, backupartifact.CheckpointVersion, inspection.CheckpointVersion)
	require.Equal(t, backupartifact.EmptyErasureLedgerSnapshotSHA256,
		inspection.ErasureLedgerSHA256)
	revalidated, err := fixture.catalog.LoadCheckpointProofCopy(
		context.Background(), fixture.primary, *inspection.CatalogProof,
	)
	require.NoError(t, err)
	require.Equal(t, inspection.RestorePointID, revalidated.ID)

	latest, err := inspector.Inspect(
		context.Background(),
		backupusecase.RestorePlanRequest{
			LatestVerified: true, Repository: "secondary",
			CatalogHead: &second.Head,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "checkpoint-restore-2", latest.RestorePointID)
	require.Equal(t, second.Head, latest.CatalogProof.EntryPage)
	require.Zero(t, fixture.keys.unwraps,
		"admission must reserve payload download and KMS for the Slot Leader")
}

func TestCheckpointRestoreInspectorRejectsIncompleteRepositoryGraph(
	t *testing.T,
) {
	fixture := newCheckpointRestoreInspectorFixture(t)
	commit := fixture.publish(
		t, "checkpoint-incomplete", 1_753_400_200_000, nil,
	)
	require.NoError(t, os.Remove(filepath.Join(
		fixture.secondaryRoot,
		filepath.FromSlash(fixture.metadata.CommitKey),
	)))
	_, err := fixture.inspector(t).Inspect(
		context.Background(),
		backupusecase.RestorePlanRequest{
			RestorePointID: "checkpoint-incomplete",
			Repository:     "primary", CatalogHead: &commit.Head,
		},
	)
	require.ErrorIs(t, err, backupartifact.ErrRepositoryIncomplete)
}

func TestCheckpointRestoreInspectorRejectsIncompleteBaselineGraph(
	t *testing.T,
) {
	fixture := newCheckpointRestoreInspectorFixture(t)
	commit := fixture.publish(
		t, "checkpoint-baseline-incomplete", 1_753_400_200_000, nil,
	)
	require.NoError(t, os.Remove(filepath.Join(
		fixture.secondaryRoot,
		filepath.FromSlash(fixture.baselineObjectKey),
	)))
	_, err := fixture.inspector(t).Inspect(
		context.Background(),
		backupusecase.RestorePlanRequest{
			RestorePointID: "checkpoint-baseline-incomplete",
			Repository:     "primary", CatalogHead: &commit.Head,
		},
	)
	require.ErrorIs(t, err, backupartifact.ErrRepositoryIncomplete)
}

func TestMaterializedCheckpointBaselineReplayerAcceptsDedicatedCursorStream(
	t *testing.T,
) {
	fixture := newCheckpointRestoreInspectorFixture(t)
	replayer, err := backupinfra.NewMaterializedCheckpointBaselineReplayer(
		backupinfra.MaterializedCheckpointBaselineReplayerOptions{
			Codec: backupartifact.NewObjectCodec(
				fixture.keys,
				bytes.NewReader(bytes.Repeat([]byte{0x34}, 64)),
			),
			Segments: fixture.segments,
		},
	)
	require.NoError(t, err)
	_, err = backupinfra.NewCheckpointSlotInstaller(
		backupinfra.CheckpointSlotInstallerOptions{
			Primary: fixture.primary, Secondary: fixture.secondary,
			Catalog: fixture.catalog, Segments: fixture.segments,
			Signer: fixture.signer,
			Codec: backupartifact.NewObjectCodec(
				fixture.keys,
				bytes.NewReader(bytes.Repeat([]byte{0x35}, 64)),
			),
			RepositoryID:    "repository-prod",
			Baseline:        replayer,
			Target:          &recordingCheckpointRestoreTarget{},
			StagingDir:      t.TempDir(),
			StagingMaxBytes: 64 << 20,
			MemoryMaxBytes:  64 << 20,
			Progress: func(
				context.Context,
				string,
				backupusecase.RestorePartition,
			) error {
				return nil
			},
		},
	)
	require.NoError(t, err)
	sink := &recordingCheckpointRestoreSink{}
	downloaded, err := replayer.ReplayCheckpointBaseline(
		context.Background(),
		fixture.primary,
		backupartifact.CheckpointSlot{
			HashSlot: 0, Generation: "slot-generation-1",
			Baseline: &fixture.baseline,
		},
		sink,
	)
	require.NoError(t, err)
	require.Positive(t, downloaded)
	require.Positive(t, sink.metadataSnapshots)
	require.Equal(t, uint64(1), sink.boundaries)
}

type checkpointRestoreInspectorFixture struct {
	primary, secondary *backupinfra.FileRepository
	secondaryRoot      string
	keys               *countingRestoreKeyManager
	signer             testEd25519Signer
	segments           *backupartifact.ReplicatedSegmentStore
	catalog            *backupinfra.ReplicatedCheckpointCatalog
	metadata           backupartifact.SegmentReference
	messages           backupartifact.SegmentReference
	cursor             backupartifact.SegmentReference
	baseline           backupartifact.CheckpointBaseline
	baselineObjectKey  string
}

func newCheckpointRestoreInspectorFixture(
	t *testing.T,
) *checkpointRestoreInspectorFixture {
	t.Helper()
	primary, err := backupinfra.NewFileRepository(
		"primary", t.TempDir(),
	)
	require.NoError(t, err)
	secondaryRoot := t.TempDir()
	secondary, err := backupinfra.NewFileRepository(
		"secondary", secondaryRoot,
	)
	require.NoError(t, err)
	keys := &countingRestoreKeyManager{mask: 0x5a}
	signer := newCatalogTestSigner()
	segments, err := backupartifact.NewReplicatedSegmentStore(
		primary, secondary,
		backupartifact.NewSegmentCodec(
			keys, bytes.NewReader(bytes.Repeat([]byte{0x31}, 512)),
		),
		signer, "signing-key",
	)
	require.NoError(t, err)
	metadataRecord, err := backupartifact.MarshalMetadataLogRecord(
		backupartifact.MetadataLogRecord{
			HashSlot: 0, RaftIndex: 1, RaftTerm: 1,
			CommittedAtUnixMillis: 1_753_400_199_000,
			Command:               []byte("portable-metadata"),
		},
	)
	require.NoError(t, err)
	messageRecord, err := backupartifact.MarshalMessageLogRecord(
		backupartifact.MessageLogRecord{
			Kind:     backupartifact.MessageLogRecordMessage,
			HashSlot: 0, ChannelID: "room", ChannelType: 1,
			Epoch: 1, HW: 1, MessageSeq: 1, MessageID: 9,
			ServerTimestampMS: 1_753_400_199_000,
			Payload:           []byte("hello"),
		},
	)
	require.NoError(t, err)
	boundary := backupartifact.ChannelBoundary{
		ChannelID: "room", ChannelType: 1, Epoch: 1, HW: 1,
	}
	metadataBody, err := backupartifact.MarshalSegmentBatch(
		backupartifact.SegmentBatch{
			HashSlot: 0, Stream: backupartifact.SegmentStreamMetadata,
			Generation: "slot-generation-1", Sequence: 1,
			NextCursor: "metadata-1", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_199_000,
			Records:               [][]byte{metadataRecord},
		},
	)
	require.NoError(t, err)
	messageBody, err := backupartifact.MarshalSegmentBatch(
		backupartifact.SegmentBatch{
			HashSlot: 0, Stream: backupartifact.SegmentStreamMessages,
			Generation: "slot-generation-1", Sequence: 1,
			NextCursor: "message-1", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_199_000,
			Records:               [][]byte{messageRecord},
			MessageCursors:        []backupartifact.ChannelBoundary{boundary},
		},
	)
	require.NoError(t, err)
	cursorBody, err := backupartifact.MarshalMessageCursorBatch(
		backupartifact.MessageCursorBatch{
			HashSlot: 0, Generation: "slot-generation-1", Sequence: 1,
			NextCursor: "message-1", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_199_000,
			Boundaries:            []backupartifact.ChannelBoundary{boundary},
		},
	)
	require.NoError(t, err)
	metadata := commitRestoreSegment(
		t, segments, backupartifact.SegmentStreamMetadata,
		metadataBody, 1,
	)
	messages := commitRestoreSegment(
		t, segments, backupartifact.SegmentStreamMessages,
		messageBody, 1,
	)
	cursor := commitRestoreSegment(
		t, segments, backupartifact.SegmentStreamMessageCursor,
		cursorBody, 1,
	)
	baselineCursorBody, err := backupartifact.MarshalMessageCursorBatch(
		backupartifact.MessageCursorBatch{
			HashSlot: 0, Generation: "slot-generation-1", Sequence: 1,
			NextCursor: "baseline-message-1", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_198_000,
			Boundaries:            []backupartifact.ChannelBoundary{boundary},
			Checkpoint:            true,
		},
	)
	require.NoError(t, err)
	baselineCursor := commitRestoreSegment(
		t, segments, backupartifact.SegmentStreamMessageBaselineCursor,
		baselineCursorBody, 1,
	)
	const baselineObjectKey = "objects/slot-generation-1/00000/metadata-000000.bin"
	baselineMetadata := checkpointRestoreBaselineMetadataSnapshot(t)
	baselineObject, err := backupartifact.NewObjectCodec(
		keys, bytes.NewReader(bytes.Repeat([]byte{0x32}, 64)),
	).Seal(context.Background(), backupartifact.ObjectDescriptor{
		Key: baselineObjectKey, Kind: backupartifact.ObjectKindMetadata,
		HashSlot: 0, KMSKeyID: "kms-key",
	}, baselineMetadata)
	require.NoError(t, err)
	require.NoError(t, backupartifact.NewReplicatedPublisher(
		primary, secondary,
	).ReplicateObject(context.Background(), baselineObject))
	partition := backupartifact.PartitionManifest{
		Format:  backupartifact.PartitionManifestFormat,
		Version: backupartifact.PartitionManifestVersion,
		JobID:   "slot-generation-1", BackupEpoch: 1,
		Cut: backupartifact.PartitionCut{
			HashSlot: 0, PhysicalSlotID: 1, RaftIndex: 1,
			CommittedAtMillis: 1_753_400_198_000,
		},
		BaselineCursor: &baselineCursor,
		Evidence: backupartifact.PartitionEvidence{
			Version: backupartifact.PartitionEvidenceVersion,
		},
		Objects: []backupartifact.ObjectEntry{baselineObject.Entry},
	}
	partitionBody, err := backupartifact.MarshalPartitionManifest(partition)
	require.NoError(t, err)
	partitionHash := sha256.Sum256(partitionBody)
	partitionSHA256 := hex.EncodeToString(partitionHash[:])
	const partitionKey = "partition-manifests/slot-generation-1/00000.json"
	for _, repository := range []backupartifact.Repository{
		primary, secondary,
	} {
		require.NoError(t, repository.PutImmutable(
			context.Background(), partitionKey,
			int64(len(partitionBody)), partitionSHA256,
			bytes.NewReader(partitionBody),
		))
	}
	baseline := backupartifact.CheckpointBaseline{
		Partition: backupartifact.PartitionReference{
			HashSlot: 0, Key: partitionKey, SHA256: partitionSHA256,
			Bytes: int64(len(partitionBody)), ObjectCount: 1,
			CiphertextBytes: uint64(baselineObject.Entry.CiphertextBytes),
			Evidence:        partition.Evidence,
		},
		MessageCursor: baselineCursor,
	}
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(
		primary, secondary, signer, "signing-key",
	)
	require.NoError(t, err)
	return &checkpointRestoreInspectorFixture{
		primary: primary, secondary: secondary,
		secondaryRoot: secondaryRoot, keys: keys, signer: signer,
		segments: segments, catalog: catalog,
		metadata: metadata, messages: messages, cursor: cursor,
		baseline: baseline, baselineObjectKey: baselineObjectKey,
	}
}

func (f *checkpointRestoreInspectorFixture) publish(
	t *testing.T,
	id string,
	createdAt int64,
	previous *backupartifact.CatalogPageReference,
) backupinfra.CheckpointCatalogCommit {
	t.Helper()
	const streamWatermarkAtUnixMillis = int64(1_753_400_199_000)
	checkpoint := backupartifact.Checkpoint{
		Format:  backupartifact.CheckpointFormat,
		Version: backupartifact.CheckpointVersion,
		ID:      id, RepositoryID: "repository-prod",
		SourceClusterID:  "cluster-source",
		SourceGeneration: "source-generation-1",
		HashSlotCount:    1, CreatedAtUnixMillis: createdAt,
		EffectiveAtUnixMillis: streamWatermarkAtUnixMillis,
		Slots: []backupartifact.CheckpointSlot{{
			HashSlot: 0, Generation: "slot-generation-1",
			Baseline: &f.baseline,
			Metadata: backupartifact.CheckpointStream{
				Sequence: 1, Head: &f.metadata,
				SourceHighWatermark:   1,
				WatermarkAtUnixMillis: streamWatermarkAtUnixMillis,
			},
			Messages: backupartifact.CheckpointStream{
				Sequence: 1, Head: &f.messages, CursorHead: &f.cursor,
				SourceHighWatermark:   1,
				WatermarkAtUnixMillis: streamWatermarkAtUnixMillis,
			},
			WatermarkAtUnixMillis: streamWatermarkAtUnixMillis,
		}},
	}
	commit, err := f.catalog.Publish(
		context.Background(), checkpoint, previous,
	)
	require.NoError(t, err)
	return commit
}

func (f *checkpointRestoreInspectorFixture) inspector(
	t *testing.T,
) *backupinfra.CheckpointRestoreInspector {
	t.Helper()
	auditor, err := backupinfra.NewCheckpointRestoreGraphAuditor(f.segments)
	require.NoError(t, err)
	inspector, err := backupinfra.NewCheckpointRestoreInspector(
		backupinfra.CheckpointRestoreInspectorOptions{
			Primary: f.primary, Secondary: f.secondary,
			Signer: f.signer,
			Codec: backupartifact.NewObjectCodec(
				f.keys, bytes.NewReader(bytes.Repeat([]byte{0x33}, 128)),
			),
			RepositoryID: "repository-prod",
			Target: staticRestoreTarget{
				state: backupinfra.RestoreTargetState{
					ClusterID:     "cluster-target",
					Generation:    "target-generation-2",
					HashSlotCount: 1, Empty: true,
				},
			},
			Catalog: f.catalog, Auditor: auditor,
		},
	)
	require.NoError(t, err)
	return inspector
}

func checkpointRestoreBaselineMetadataSnapshot(t *testing.T) []byte {
	t.Helper()
	database, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, database.ForHashSlot(0).CreateUser(
		context.Background(),
		metadb.User{UID: "baseline-user", Token: "baseline-token"},
	))
	reader, err := database.OpenBackupHashSlotSnapshot(
		context.Background(), []uint16{0},
	)
	require.NoError(t, err)
	body, readErr := io.ReadAll(reader)
	closeReaderErr := reader.Close()
	closeDatabaseErr := database.Close()
	require.NoError(t, readErr)
	require.NoError(t, closeReaderErr)
	require.NoError(t, closeDatabaseErr)
	return body
}

type recordingCheckpointRestoreSink struct {
	metadataSnapshots int
	boundaries        uint64
}

func (s *recordingCheckpointRestoreSink) MetadataSnapshot(
	[]byte,
	[]byte,
) error {
	s.metadataSnapshots++
	return nil
}

func (*recordingCheckpointRestoreSink) Metadata([]byte) error {
	return nil
}

func (*recordingCheckpointRestoreSink) Message([]byte) error {
	return nil
}

func (s *recordingCheckpointRestoreSink) Boundary(
	backupartifact.ChannelBoundary,
) error {
	s.boundaries++
	return nil
}
