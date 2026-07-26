package backup_test

import (
	"context"
	"strings"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/require"
)

func TestDurableCheckpointRestoreTargetStagesFinalSnapshotAndResumesReceipt(
	t *testing.T,
) {
	ctx := context.Background()
	distributor := &recordingCheckpointSnapshotDistributor{}
	now := time.UnixMilli(1_753_400_300_000).UTC()
	root := t.TempDir()
	target, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir: root, StagingMaxBytes: 64 << 20,
			Distributor: distributor,
			Now:         func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	fence := backupinfra.CheckpointRestoreInstallFence{
		PlanID: "plan-1", CheckpointID: "checkpoint-1",
		CheckpointSHA256: strings.Repeat("a", 64),
		TargetGeneration: "target-generation-1",
		HashSlot:         0, TargetSlotID: 7, ReplicaCount: 3,
		LeaderNodeID: 1, LeaderTerm: 2, ConfigEpoch: 3, Attempt: 1,
		InvalidateTokens: true,
	}
	session, err := target.BeginCheckpointRestore(ctx, fence, 32<<20)
	require.NoError(t, err)
	index := session.(backupinfra.CheckpointRestoreEvidenceSession).
		RestoreEvidenceIndex()
	accumulator := backupartifact.NewRestoreEvidenceAccumulatorWithIndex(0, index)

	command := fsm.EncodeUpsertUserCommand(metadb.User{
		UID: "u1", Token: "secret", DeviceFlag: 1, DeviceLevel: 1,
	})
	metadataBody, err := backupartifact.MarshalMetadataLogRecord(
		backupartifact.MetadataLogRecord{
			HashSlot: 0, RaftIndex: 1, RaftTerm: 1,
			CommittedAtUnixMillis: now.Add(-time.Second).UnixMilli(),
			Command:               command,
		},
	)
	require.NoError(t, err)
	metadata, err := accumulator.AddMetadata(metadataBody)
	require.NoError(t, err)
	require.NoError(t, session.ApplyMetadata(ctx, metadata))

	boundary := backupartifact.ChannelBoundary{
		ChannelID: "room", ChannelType: 1, Epoch: 1, HW: 1,
	}
	require.NoError(t, accumulator.MergeBoundary(boundary))
	require.NoError(t, session.ApplyMessageBoundary(ctx, boundary))
	messageBody, err := backupartifact.MarshalMessageLogRecord(
		backupartifact.MessageLogRecord{
			Kind:     backupartifact.MessageLogRecordMessage,
			HashSlot: 0, ChannelID: "room", ChannelType: 1,
			Epoch: 1, HW: 1, MessageSeq: 1, MessageID: 10,
			ServerTimestampMS: now.Add(-time.Second).UnixMilli(),
			Payload:           []byte("hello"),
		},
	)
	require.NoError(t, err)
	message, err := accumulator.AddMessage(messageBody)
	require.NoError(t, err)
	require.NoError(t, session.ApplyMessage(ctx, message))
	evidence, err := accumulator.Finish()
	require.NoError(t, err)

	result, err := session.Finalize(ctx, evidence, 1234)
	require.NoError(t, err)
	require.Equal(t, uint32(3), result.ConvergedReplicas)
	require.Equal(t, result.MetadataSHA256, distributor.snapshot.Metadata.SHA256)
	require.FileExists(t, distributor.snapshot.Metadata.Path)
	require.Len(t, distributor.snapshot.Messages, 1)
	require.FileExists(t, distributor.snapshot.Messages[0].Path)

	reopened, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir: root, StagingMaxBytes: 64 << 20,
			Distributor: distributor,
			Now:         func() time.Time { return now.Add(time.Hour) },
		},
	)
	require.NoError(t, err)
	resume, found, err := reopened.ResumeCheckpointRestore(ctx, fence)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(1234), resume.DownloadedBytes)
	require.Equal(t, now.UnixMilli(), resume.InstalledAtUnixMillis)
	require.Equal(t, evidence, resume.Evidence)
}

func TestDurableCheckpointRestoreTargetAbortDiscardsPartialAttempt(t *testing.T) {
	target, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir: t.TempDir(), StagingMaxBytes: 64 << 20,
			Distributor: &recordingCheckpointSnapshotDistributor{},
		},
	)
	require.NoError(t, err)
	fence := backupinfra.CheckpointRestoreInstallFence{
		PlanID: "plan-abort", CheckpointID: "checkpoint-abort",
		CheckpointSHA256: strings.Repeat("b", 64),
		TargetGeneration: "target-generation-1",
		HashSlot:         1, TargetSlotID: 8, ReplicaCount: 1,
		LeaderNodeID: 1, LeaderTerm: 2, ConfigEpoch: 3, Attempt: 1,
	}
	session, err := target.BeginCheckpointRestore(
		context.Background(), fence, 32<<20,
	)
	require.NoError(t, err)
	require.NoError(t, session.Abort(context.Background()))
	_, found, err := target.ResumeCheckpointRestore(context.Background(), fence)
	require.NoError(t, err)
	require.False(t, found)
}

func TestDurableCheckpointRestoreTargetAllowsBoundedConcurrentSlotSessions(
	t *testing.T,
) {
	target, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir: t.TempDir(), StagingMaxBytes: 64 << 20,
			Distributor: &recordingCheckpointSnapshotDistributor{},
		},
	)
	require.NoError(t, err)
	firstFence := backupinfra.CheckpointRestoreInstallFence{
		PlanID: "concurrent-plan", CheckpointID: "checkpoint",
		CheckpointSHA256: strings.Repeat("a", 64),
		TargetGeneration: "target-generation",
		HashSlot:         0, TargetSlotID: 7, ReplicaCount: 3,
		LeaderNodeID: 1, LeaderTerm: 2, ConfigEpoch: 3, Attempt: 1,
	}
	first, err := target.BeginCheckpointRestore(
		context.Background(), firstFence, 16<<20,
	)
	require.NoError(t, err)
	secondFence := firstFence
	secondFence.HashSlot = 1
	secondFence.TargetSlotID = 8
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	second, err := target.BeginCheckpointRestore(
		ctx, secondFence, 16<<20,
	)
	require.NoError(t, err)
	require.NoError(t, second.Abort(context.Background()))
	require.NoError(t, first.Abort(context.Background()))
}

func TestDurableCheckpointRestoreTargetSerializesSameAttemptSession(
	t *testing.T,
) {
	target, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir: t.TempDir(), StagingMaxBytes: 64 << 20,
			Distributor: &recordingCheckpointSnapshotDistributor{},
		},
	)
	require.NoError(t, err)
	fence := backupinfra.CheckpointRestoreInstallFence{
		PlanID: "same-attempt-plan", CheckpointID: "checkpoint",
		CheckpointSHA256: strings.Repeat("a", 64),
		TargetGeneration: "target-generation",
		HashSlot:         0, TargetSlotID: 7, ReplicaCount: 3,
		LeaderNodeID: 1, LeaderTerm: 2, ConfigEpoch: 3, Attempt: 1,
	}
	first, err := target.BeginCheckpointRestore(
		context.Background(), fence, 16<<20,
	)
	require.NoError(t, err)
	type beginResult struct {
		session backupinfra.CheckpointRestoreSession
		err     error
	}
	secondResult := make(chan beginResult, 1)
	go func() {
		session, err := target.BeginCheckpointRestore(
			context.Background(), fence, 16<<20,
		)
		secondResult <- beginResult{session: session, err: err}
	}()
	select {
	case result := <-secondResult:
		if result.session != nil {
			_ = result.session.Abort(context.Background())
		}
		t.Fatalf("same attempt entered before Abort: %v", result.err)
	case <-time.After(20 * time.Millisecond):
	}
	require.NoError(t, first.Abort(context.Background()))
	select {
	case result := <-secondResult:
		require.NoError(t, result.err)
		require.NotNil(t, result.session)
		require.NoError(t, result.session.Abort(context.Background()))
	case <-time.After(time.Second):
		t.Fatal("same attempt did not resume after Abort")
	}
}

func TestDurableCheckpointRestoreTargetAbortAfterSettledBuildReclaimsQuota(
	t *testing.T,
) {
	target, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir: t.TempDir(), StagingMaxBytes: 64 << 20,
			Distributor: invalidCheckpointSnapshotDistributor{},
		},
	)
	require.NoError(t, err)
	fence := backupinfra.CheckpointRestoreInstallFence{
		PlanID: "settled-abort-plan", CheckpointID: "checkpoint",
		CheckpointSHA256: strings.Repeat("a", 64),
		TargetGeneration: "target-generation",
		HashSlot:         0, TargetSlotID: 7, ReplicaCount: 3,
		LeaderNodeID: 1, LeaderTerm: 2, ConfigEpoch: 3, Attempt: 1,
	}
	session, err := target.BeginCheckpointRestore(
		context.Background(), fence, 32<<20,
	)
	require.NoError(t, err)
	index := session.(backupinfra.CheckpointRestoreEvidenceSession).
		RestoreEvidenceIndex()
	accumulator := backupartifact.NewRestoreEvidenceAccumulatorWithIndex(
		fence.HashSlot, index,
	)
	evidence, err := accumulator.Finish()
	require.NoError(t, err)
	_, err = session.Finalize(context.Background(), evidence, 0)
	require.Error(t, err)
	require.NoError(t, session.Abort(context.Background()))

	retry, err := target.BeginCheckpointRestore(
		context.Background(), fence, 64<<20,
	)
	require.NoError(t, err)
	require.NoError(t, retry.Abort(context.Background()))
}

type recordingCheckpointSnapshotDistributor struct {
	snapshot backupinfra.CheckpointRestoreSnapshot
}

type invalidCheckpointSnapshotDistributor struct{}

func (invalidCheckpointSnapshotDistributor) DistributeCheckpointRestoreSnapshot(
	context.Context,
	backupinfra.CheckpointRestoreInstallFence,
	backupinfra.CheckpointRestoreSnapshot,
) (backupinfra.CheckpointRestoreReplicaResult, error) {
	return backupinfra.CheckpointRestoreReplicaResult{}, nil
}

func (d *recordingCheckpointSnapshotDistributor) DistributeCheckpointRestoreSnapshot(
	_ context.Context,
	fence backupinfra.CheckpointRestoreInstallFence,
	snapshot backupinfra.CheckpointRestoreSnapshot,
) (backupinfra.CheckpointRestoreReplicaResult, error) {
	d.snapshot = snapshot
	var replicated uint64
	for _, file := range append(
		[]backupinfra.CheckpointRestoreSnapshotFile{snapshot.Metadata, snapshot.Erasures},
		snapshot.Messages...,
	) {
		replicated += uint64(file.Size)
	}
	return backupinfra.CheckpointRestoreReplicaResult{
		ReplicaCount:      fence.ReplicaCount,
		ConvergedReplicas: fence.ReplicaCount,
		ReplicatedBytes:   replicated,
		MetadataSHA256:    snapshot.Metadata.SHA256,
	}, nil
}
