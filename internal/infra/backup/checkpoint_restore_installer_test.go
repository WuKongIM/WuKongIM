package backup_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestCheckpointSlotInstallerImportsOnceOnLeaderAndFinalizesReplicaConvergence(t *testing.T) {
	ctx := context.Background()
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	keys := &countingRestoreKeyManager{mask: 0x4a}
	segmentCodec := backupartifact.NewSegmentCodec(
		keys, bytes.NewReader(bytes.Repeat([]byte{0x41}, 512)),
	)
	signer := newCatalogTestSigner()
	segments, err := backupartifact.NewReplicatedSegmentStore(
		primary, secondary, segmentCodec, signer, "signing-key",
	)
	require.NoError(t, err)
	metadataRecord, err := backupartifact.MarshalMetadataLogRecord(
		backupartifact.MetadataLogRecord{
			HashSlot: 0, RaftIndex: 1, RaftTerm: 1,
			CommittedAtUnixMillis: 1_753_400_200_000,
			Command:               []byte("metadata-command"),
		},
	)
	require.NoError(t, err)
	messageRecord, err := backupartifact.MarshalMessageLogRecord(
		backupartifact.MessageLogRecord{
			Kind: backupartifact.MessageLogRecordMessage, HashSlot: 0,
			ChannelID: "room", ChannelType: 2, Epoch: 1, HW: 1,
			MessageSeq: 1, MessageID: 91,
			ServerTimestampMS: 1_753_400_200_000, Payload: []byte("hello"),
		},
	)
	require.NoError(t, err)
	boundary := backupartifact.ChannelBoundary{
		ChannelID: "room", ChannelType: 2, Epoch: 1, HW: 1,
	}
	metadataBody, err := backupartifact.MarshalSegmentBatch(
		backupartifact.SegmentBatch{
			HashSlot: 0, Stream: backupartifact.SegmentStreamMetadata,
			Generation: "slot-generation-1", Sequence: 1,
			NextCursor: "1", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_200_000,
			Records:               [][]byte{metadataRecord},
		},
	)
	require.NoError(t, err)
	messageBody, err := backupartifact.MarshalSegmentBatch(
		backupartifact.SegmentBatch{
			HashSlot: 0, Stream: backupartifact.SegmentStreamMessages,
			Generation: "slot-generation-1", Sequence: 1,
			NextCursor: "message-1", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_200_000,
			Records:               [][]byte{messageRecord}, MessageCursors: []backupartifact.ChannelBoundary{boundary},
		},
	)
	require.NoError(t, err)
	cursorBody, err := backupartifact.MarshalMessageCursorBatch(
		backupartifact.MessageCursorBatch{
			HashSlot: 0, Generation: "slot-generation-1", Sequence: 1,
			NextCursor: "message-1", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_200_000,
			Boundaries:            []backupartifact.ChannelBoundary{boundary},
		},
	)
	require.NoError(t, err)
	metadataRef := commitRestoreSegment(
		t, segments, backupartifact.SegmentStreamMetadata, metadataBody, 1,
	)
	messageRef := commitRestoreSegment(
		t, segments, backupartifact.SegmentStreamMessages, messageBody, 1,
	)
	cursorRef := commitRestoreSegment(
		t, segments, backupartifact.SegmentStreamMessageCursor, cursorBody, 1,
	)
	checkpoint := backupartifact.Checkpoint{
		Format:  backupartifact.CheckpointFormat,
		Version: backupartifact.CheckpointVersion,
		ID:      "checkpoint-import", RepositoryID: "repository-prod",
		SourceClusterID:       "cluster-source",
		SourceGeneration:      "source-generation-1",
		HashSlotCount:         1,
		CreatedAtUnixMillis:   1_753_400_201_000,
		EffectiveAtUnixMillis: 1_753_400_200_000,
		Slots: []backupartifact.CheckpointSlot{{
			HashSlot: 0, Generation: "slot-generation-1",
			Metadata: backupartifact.CheckpointStream{
				Sequence: 1, Head: &metadataRef, SourceHighWatermark: 1,
				WatermarkAtUnixMillis: 1_753_400_200_000,
			},
			Messages: backupartifact.CheckpointStream{
				Sequence: 1, Head: &messageRef, CursorHead: &cursorRef,
				SourceHighWatermark:   1,
				WatermarkAtUnixMillis: 1_753_400_200_000,
			},
			WatermarkAtUnixMillis: 1_753_400_200_000,
		}},
	}
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(
		primary, secondary, signer, "signing-key",
	)
	require.NoError(t, err)
	commit, err := catalog.Publish(ctx, checkpoint, nil)
	require.NoError(t, err)
	proof, loaded, err := catalog.ResolveCheckpointForRestore(
		ctx, primary, commit.Head, checkpoint.ID, false,
	)
	require.NoError(t, err)
	require.Equal(t, checkpoint.ID, loaded.ID)

	target := &recordingCheckpointRestoreTarget{
		session: &recordingCheckpointRestoreSession{
			replicas: backupinfra.CheckpointRestoreReplicaResult{
				ReplicaCount: 3, ConvergedReplicas: 2, ReplicatedBytes: 777,
				MetadataSHA256: strings.Repeat("a", 64),
			},
		},
	}
	var activeProgress []backupusecase.RestorePartition
	installer, err := backupinfra.NewCheckpointSlotInstaller(
		backupinfra.CheckpointSlotInstallerOptions{
			Primary: primary, Secondary: secondary,
			Catalog: catalog, Segments: segments, Signer: signer,
			Codec: backupartifact.NewObjectCodec(
				keys, bytes.NewReader(bytes.Repeat([]byte{0x51}, 128)),
			),
			RepositoryID: "repository-prod",
			Baseline:     noCheckpointBaseline{}, Target: target,
			StagingDir: t.TempDir(), StagingMaxBytes: 64 << 20,
			MemoryMaxBytes: 64 << 20,
			Progress: func(
				_ context.Context,
				_ string,
				progress backupusecase.RestorePartition,
			) error {
				activeProgress = append(activeProgress, progress)
				return nil
			},
			Now: func() time.Time {
				return time.UnixMilli(1_753_400_210_000).UTC()
			},
		},
	)
	require.NoError(t, err)
	plan := backupusecase.RestorePlan{
		ID: "plan-1", CheckpointID: checkpoint.ID,
		CheckpointSHA256: commit.Checkpoint.SHA256, CatalogProof: &proof,
		CheckpointVersion:               checkpoint.Version,
		CheckpointCreatedAtUnixMillis:   checkpoint.CreatedAtUnixMillis,
		CheckpointEffectiveAtUnixMillis: checkpoint.EffectiveAtUnixMillis,
		Repository:                      "primary", SourceClusterID: checkpoint.SourceClusterID,
		SourceGeneration: checkpoint.SourceGeneration,
		TargetClusterID:  "cluster-target",
		TargetGeneration: "target-generation-2", HashSlotCount: 1,
		ErasureLedgerVersion: backupartifact.ErasureLedgerSnapshotVersion,
		ErasureLedgerSHA256:  backupartifact.EmptyErasureLedgerSnapshotSHA256,
		Status:               backupusecase.RestoreStatusInstalling,
		Partitions: []backupusecase.RestorePartition{{
			HashSlot: 0, Status: backupcontract.RestorePartitionInstalling,
			TargetSlotID: 7, LeaderNodeID: 2, LeaderTerm: 9, ConfigEpoch: 4,
			InstallAttempt: 1, ReplicaCount: 3,
			StartedAtUnixMillis: 1_753_400_205_000,
		}},
	}
	report, err := installer.InstallPartition(ctx, plan, 0)
	require.NoError(t, err)
	require.Equal(t, backupcontract.RestorePartitionConverging, report.Status)
	require.Equal(t, uint64(1), report.MetadataRecordCount)
	require.Equal(t, uint64(1), report.MessageCount)
	require.Equal(t, uint64(91), report.MaxMessageID)
	require.Equal(t, uint32(2), report.ConvergedReplicas)
	require.Equal(t, uint64(777), report.ReplicatedBytes)
	require.NotEmpty(t, activeProgress)
	require.Equal(
		t, backupcontract.RestorePartitionInstalling,
		activeProgress[0].Status,
	)
	require.Greater(t, activeProgress[len(activeProgress)-1].DownloadedBytes, uint64(0))
	require.Len(t, report.ContentSHA256, 64)
	require.Len(t, report.MessageMerkleSHA256, 64)
	require.Equal(t, 1, target.session.metadata)
	require.Equal(t, 1, target.session.messages)
	require.True(t, target.session.finalized)
	require.False(t, target.session.aborted)
	require.Equal(t, 3, keys.unwraps, "metadata, message, and cursor payloads decrypt exactly once")
	target.found = true
	target.session.replicas.ConvergedReplicas = 3
	target.resume = backupinfra.CheckpointRestoreResume{
		Evidence:              target.session.evidence,
		DownloadedBytes:       report.DownloadedBytes,
		InstalledAtUnixMillis: report.InstalledAtUnixMillis,
		Replicas:              target.session.replicas,
	}
	resumePlan := plan
	resumePlan.Partitions = append(
		[]backupusecase.RestorePartition(nil), plan.Partitions...,
	)
	resumePlan.Partitions[0] = report
	resumed, err := installer.InstallPartition(ctx, resumePlan, 0)
	require.NoError(t, err)
	require.Equal(t, report.ContentSHA256, resumed.ContentSHA256)
	require.Equal(t, backupcontract.RestorePartitionConverged, resumed.Status)
	require.Equal(t, 3, keys.unwraps, "durable Leader resume must not read repositories or call KMS again")

	failingSession := &recordingCheckpointRestoreSession{
		finalErr: errors.New("snapshot replication failed"),
	}
	var durableDownloadedBytes uint64
	failingInstaller, err := backupinfra.NewCheckpointSlotInstaller(
		backupinfra.CheckpointSlotInstallerOptions{
			Primary: primary, Secondary: secondary,
			Catalog: catalog, Segments: segments, Signer: signer,
			Codec: backupartifact.NewObjectCodec(
				keys, bytes.NewReader(bytes.Repeat([]byte{0x52}, 128)),
			),
			RepositoryID: "repository-prod",
			Baseline:     noCheckpointBaseline{},
			Target:       &recordingCheckpointRestoreTarget{session: failingSession},
			StagingDir:   t.TempDir(), StagingMaxBytes: 64 << 20,
			MemoryMaxBytes: 64 << 20,
			Progress: func(
				_ context.Context,
				_ string,
				progress backupusecase.RestorePartition,
			) error {
				if progress.DownloadedBytes < durableDownloadedBytes {
					return errors.New("download progress regressed")
				}
				durableDownloadedBytes = progress.DownloadedBytes
				return nil
			},
		},
	)
	require.NoError(t, err)
	_, err = failingInstaller.InstallPartition(ctx, plan, 0)
	require.ErrorContains(t, err, "snapshot replication failed")
	require.True(t, failingSession.aborted)
	require.False(t, failingSession.finalized)
	require.Positive(t, durableDownloadedBytes)

	retryPlan := plan
	retryPlan.Partitions = append(
		[]backupusecase.RestorePartition(nil), plan.Partitions...,
	)
	retryPlan.Partitions[0].DownloadedBytes = durableDownloadedBytes
	failingSession.finalErr = nil
	failingSession.aborted = false
	failingSession.replicas = backupinfra.CheckpointRestoreReplicaResult{
		ReplicaCount: 3, ConvergedReplicas: 3,
		MetadataSHA256: strings.Repeat("c", 64),
	}
	retried, err := failingInstaller.InstallPartition(ctx, retryPlan, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(
		t, retried.DownloadedBytes, durableDownloadedBytes,
	)
}

func commitRestoreSegment(
	t *testing.T,
	store *backupartifact.ReplicatedSegmentStore,
	stream backupartifact.SegmentStream,
	body []byte,
	recordCount uint64,
) backupartifact.SegmentReference {
	t.Helper()
	var previous *backupartifact.SegmentReference
	var checkpoint bool
	var sourceHighWatermark uint64
	var watermarkAtUnixMillis int64
	switch stream {
	case backupartifact.SegmentStreamMetadata,
		backupartifact.SegmentStreamMessages:
		info, inspectErr := backupartifact.InspectSegmentBatch(body)
		require.NoError(t, inspectErr)
		previous = info.Previous
		sourceHighWatermark = info.SourceHighWatermark
		watermarkAtUnixMillis = info.WatermarkAtUnixMillis
	case backupartifact.SegmentStreamMessageCursor,
		backupartifact.SegmentStreamMessageBaselineCursor:
		cursor, inspectErr := backupartifact.LoadMessageCursorBatch(body)
		require.NoError(t, inspectErr)
		previous = cursor.Previous
		checkpoint = cursor.Checkpoint
		sourceHighWatermark = cursor.SourceHighWatermark
		watermarkAtUnixMillis = cursor.WatermarkAtUnixMillis
	}
	reference, err := store.Commit(
		context.Background(),
		backupartifact.SegmentDescriptor{
			Logical: backupartifact.SegmentLogicalDescriptor{
				RepositoryID:     "repository-prod",
				SourceClusterID:  "cluster-source",
				SourceGeneration: "source-generation-1",
				Generation:       "slot-generation-1", HashSlot: 0,
				Stream: stream, Sequence: 1, RecordCount: recordCount,
			},
			Previous: previous, Checkpoint: checkpoint,
			SourceHighWatermark:   sourceHighWatermark,
			WatermarkAtUnixMillis: watermarkAtUnixMillis,
			KMSKeyID:              "kms-key",
		},
		body,
	)
	require.NoError(t, err)
	return reference
}

type countingRestoreKeyManager struct {
	mask    byte
	unwraps int
}

func (m *countingRestoreKeyManager) GenerateDataKey(
	context.Context,
	string,
) (backupartifact.DataKey, error) {
	plaintext := bytes.Repeat([]byte{0x61}, 32)
	return backupartifact.DataKey{
		Plaintext: plaintext, Wrapped: xorTestBytes(plaintext, m.mask),
	}, nil
}

func (m *countingRestoreKeyManager) UnwrapDataKey(
	_ context.Context,
	_ string,
	wrapped []byte,
) ([]byte, error) {
	m.unwraps++
	return xorTestBytes(wrapped, m.mask), nil
}

type noCheckpointBaseline struct{}

func (noCheckpointBaseline) ReplayCheckpointBaseline(
	context.Context,
	backupartifact.Repository,
	backupartifact.CheckpointSlot,
	backupinfra.CheckpointRestoreRecordSink,
) (uint64, error) {
	return 0, nil
}

type recordingCheckpointRestoreTarget struct {
	session *recordingCheckpointRestoreSession
	fence   backupinfra.CheckpointRestoreInstallFence
	resume  backupinfra.CheckpointRestoreResume
	found   bool
}

func (t *recordingCheckpointRestoreTarget) ResumeCheckpointRestore(
	_ context.Context,
	fence backupinfra.CheckpointRestoreInstallFence,
) (backupinfra.CheckpointRestoreResume, bool, error) {
	t.fence = fence
	return t.resume, t.found, nil
}

func (t *recordingCheckpointRestoreTarget) BeginCheckpointRestore(
	_ context.Context,
	fence backupinfra.CheckpointRestoreInstallFence,
	_ uint64,
) (backupinfra.CheckpointRestoreSession, error) {
	t.fence = fence
	return t.session, nil
}

type recordingCheckpointRestoreSession struct {
	metadata  int
	messages  int
	finalized bool
	aborted   bool
	replicas  backupinfra.CheckpointRestoreReplicaResult
	finalErr  error
	evidence  backupartifact.RestoreEvidence
}

func (s *recordingCheckpointRestoreSession) ApplyMetadata(
	context.Context,
	backupartifact.MetadataLogRecord,
) error {
	s.metadata++
	return nil
}

func (s *recordingCheckpointRestoreSession) ApplyMetadataSnapshot(
	context.Context,
	[]byte,
	[]byte,
) error {
	s.metadata++
	return nil
}

func (s *recordingCheckpointRestoreSession) ApplyMessage(
	context.Context,
	backupartifact.MessageLogRecord,
) error {
	s.messages++
	return nil
}

func (s *recordingCheckpointRestoreSession) ApplyMessageBoundary(
	context.Context,
	backupartifact.ChannelBoundary,
) error {
	return nil
}

func (s *recordingCheckpointRestoreSession) StagePermanentErasure(
	context.Context,
	backupinfra.PermanentErasureBoundary,
) error {
	return nil
}

func (s *recordingCheckpointRestoreSession) Finalize(
	_ context.Context,
	evidence backupartifact.RestoreEvidence,
	_ uint64,
) (backupinfra.CheckpointRestoreReplicaResult, error) {
	if s.finalErr != nil {
		return backupinfra.CheckpointRestoreReplicaResult{}, s.finalErr
	}
	s.evidence = evidence
	s.finalized = true
	return s.replicas, nil
}

func (s *recordingCheckpointRestoreSession) Abort(context.Context) error {
	s.aborted = true
	return nil
}
