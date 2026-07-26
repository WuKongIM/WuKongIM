package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/require"
)

func TestCheckpointRestoreReplicaDistributorResumesOnPromotedFollower(
	t *testing.T,
) {
	ctx := context.Background()
	sourceRoot := t.TempDir()
	fence, snapshot := buildCheckpointReplicaSnapshot(
		t, sourceRoot,
	)
	require.NoError(t, os.Remove(
		filepath.Join(filepath.Dir(snapshot.Metadata.Path), "receipt.json"),
	))

	route := clusterpkg.Route{
		HashSlot: fence.HashSlot, SlotID: fence.TargetSlotID,
		Leader: fence.LeaderNodeID, LeaderTerm: fence.LeaderTerm,
		ConfigEpoch: fence.ConfigEpoch, Peers: []uint64{1, 2, 3},
	}
	nodes := map[uint64]*checkpointReplicaTestNode{}
	receivers := map[uint64]*backupinfra.CheckpointRestoreReplicaReceiver{}
	roots := map[uint64]string{1: sourceRoot, 2: t.TempDir(), 3: t.TempDir()}
	quotas := map[uint64]*backupinfra.CheckpointRestoreStagingQuota{}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		node := &checkpointReplicaTestNode{id: nodeID, route: route}
		nodes[nodeID] = node
		quota, err := backupinfra.NewCheckpointRestoreStagingQuota(
			roots[nodeID], 64<<20,
		)
		require.NoError(t, err)
		quotas[nodeID] = quota
		receiver, err := backupinfra.NewCheckpointRestoreReplicaReceiver(
			backupinfra.CheckpointRestoreReplicaReceiverOptions{
				Node: node, StagingDir: roots[nodeID],
				StagingMaxBytes: 64 << 20, StagingQuota: quota,
			},
		)
		require.NoError(t, err)
		receivers[nodeID] = receiver
	}
	remote := &checkpointReplicaTestRemote{
		receivers: receivers, failNode: 3, failChunk: 6,
	}
	distributor, err := backupinfra.NewCheckpointRestoreReplicaDistributor(
		backupinfra.CheckpointRestoreReplicaDistributorOptions{
			Node: nodes[1], Local: receivers[1], Remote: remote,
			ChunkBytes: 64,
		},
	)
	require.NoError(t, err)
	first, err := distributor.DistributeCheckpointRestoreSnapshot(
		ctx, fence, snapshot,
	)
	require.NoError(t, err)
	require.Equal(t, uint32(3), first.ConvergedReplicas)
	require.Equal(t, strings.Repeat("d", 64), first.MetadataSHA256)
	require.NotEqual(t, snapshot.Metadata.SHA256, first.MetadataSHA256,
		"semantic metadata includes reconstructed target runtime metadata")
	require.NotEmpty(t, nodes[1].verified)
	require.NotEmpty(t, nodes[2].verified)
	require.Equal(t, uint64(1),
		nodes[1].verified[0].PermanentEraseThroughSeq)
	require.NotEmpty(t, nodes[1].erasures)
	require.NotEmpty(t, nodes[3].verified)

	// A follower is promoted under a new Leader/term/attempt fence. Its durable
	// receipt contains the complete plaintext target snapshot, so convergence
	// resumes without a repository or KMS port.
	nextFence := fence
	nextFence.LeaderNodeID = 2
	nextFence.LeaderTerm++
	nextFence.Attempt++
	nextRoute := route
	nextRoute.Leader = 2
	nextRoute.LeaderTerm = nextFence.LeaderTerm
	for _, node := range nodes {
		node.setRoute(nextRoute)
	}
	nextDistributor, err :=
		backupinfra.NewCheckpointRestoreReplicaDistributor(
			backupinfra.CheckpointRestoreReplicaDistributorOptions{
				Node: nodes[2], Local: receivers[2], Remote: remote,
				ChunkBytes: 64,
			},
		)
	require.NoError(t, err)
	target, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir: roots[2], StagingMaxBytes: 64 << 20,
			StagingQuota: quotas[2], Distributor: nextDistributor,
		},
	)
	require.NoError(t, err)
	resumed, found, err := target.ResumeCheckpointRestore(ctx, nextFence)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint32(3), resumed.Replicas.ConvergedReplicas)
	require.Equal(t, nextFence.ReplicaCount, resumed.Replicas.ReplicaCount)
	require.True(t, remote.resumeObserved)
	require.NotEmpty(t, nodes[3].verified)
}

func TestCheckpointRestoreReplicaDistributorDoesNotConvergeCanceledWaiters(
	t *testing.T,
) {
	root := t.TempDir()
	fence, snapshot := buildCheckpointReplicaSnapshot(t, root)
	peers := make([]uint64, 10)
	for index := range peers {
		peers[index] = uint64(index + 1)
	}
	fence.ReplicaCount = uint32(len(peers))
	node := &checkpointReplicaTestNode{
		id: 1,
		route: clusterpkg.Route{
			HashSlot: fence.HashSlot, SlotID: fence.TargetSlotID,
			Leader: fence.LeaderNodeID, LeaderTerm: fence.LeaderTerm,
			ConfigEpoch: fence.ConfigEpoch, Peers: peers,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	distributor, err := backupinfra.NewCheckpointRestoreReplicaDistributor(
		backupinfra.CheckpointRestoreReplicaDistributorOptions{
			Node: node,
			Local: cancelingCheckpointReplicaInstaller{
				cancel: cancel,
			},
			Remote: canceledCheckpointReplicaRemote{},
		},
	)
	require.NoError(t, err)
	result, err := distributor.DistributeCheckpointRestoreSnapshot(
		ctx, fence, snapshot,
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, uint32(1), result.ConvergedReplicas)
	require.Zero(t, result.ReplicatedBytes)
}

func TestCheckpointRestoreReplicaReceiverRejectsStaleAuthorityAndCorruption(
	t *testing.T,
) {
	ctx := context.Background()
	sourceRoot := t.TempDir()
	fence, snapshot := buildCheckpointReplicaSnapshot(t, sourceRoot)
	require.NoError(t, os.Remove(
		filepath.Join(filepath.Dir(snapshot.Metadata.Path), "receipt.json"),
	))
	node := &checkpointReplicaTestNode{
		id: 1,
		route: clusterpkg.Route{
			HashSlot: fence.HashSlot, SlotID: fence.TargetSlotID,
			Leader: fence.LeaderNodeID, LeaderTerm: fence.LeaderTerm,
			ConfigEpoch: fence.ConfigEpoch, Peers: []uint64{1, 2, 3},
		},
	}
	receiver, err := backupinfra.NewCheckpointRestoreReplicaReceiver(
		backupinfra.CheckpointRestoreReplicaReceiverOptions{
			Node: node, StagingDir: sourceRoot,
			StagingMaxBytes: 32 << 20,
		},
	)
	require.NoError(t, err)
	stale := fence
	stale.LeaderTerm++
	_, err = receiver.InstallCheckpointRestoreSnapshot(
		ctx, stale, snapshot,
	)
	require.Error(t, err)
	require.Empty(t, node.verified)

	file, err := os.OpenFile(
		snapshot.Metadata.Path, os.O_WRONLY, 0,
	)
	require.NoError(t, err)
	_, err = file.WriteAt([]byte{0xff}, 0)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	_, err = receiver.InstallCheckpointRestoreSnapshot(
		ctx, fence, snapshot,
	)
	require.Error(t, err)
	require.Empty(t, node.verified)
}

func TestCheckpointRestoreReplicaReceiverBoundsLeaderLocalSnapshot(
	t *testing.T,
) {
	root := t.TempDir()
	fence, snapshot := buildCheckpointReplicaSnapshot(t, root)
	require.NoError(t, os.Remove(
		filepath.Join(filepath.Dir(snapshot.Metadata.Path), "receipt.json"),
	))
	node := &checkpointReplicaTestNode{
		id: 1,
		route: clusterpkg.Route{
			HashSlot: fence.HashSlot, SlotID: fence.TargetSlotID,
			Leader: fence.LeaderNodeID, LeaderTerm: fence.LeaderTerm,
			ConfigEpoch: fence.ConfigEpoch, Peers: []uint64{1, 2, 3},
		},
	}
	receiver, err := backupinfra.NewCheckpointRestoreReplicaReceiver(
		backupinfra.CheckpointRestoreReplicaReceiverOptions{
			Node: node, StagingDir: root, StagingMaxBytes: 1,
		},
	)
	require.ErrorIs(t, err, backupartifact.ErrInvalidObject)
	require.Nil(t, receiver)
	require.Empty(t, node.verified)
}

func TestCheckpointRestoreReplicaStatusRevalidatesLiveTarget(t *testing.T) {
	t.Run("initial message mismatch", func(t *testing.T) {
		ctx, fence, snapshot, node, receiver, _ :=
			newCheckpointReplicaReceiverFixture(t)
		node.mu.Lock()
		node.messageEvidenceOverride = strings.Repeat("e", 64)
		node.mu.Unlock()
		_, err := receiver.InstallCheckpointRestoreSnapshot(
			ctx, fence, snapshot,
		)
		require.ErrorContains(t, err, "live message content mismatch")
		require.True(t, node.discarded)
		_, statErr := os.Stat(filepath.Join(
			filepath.Dir(snapshot.Metadata.Path), "receipt.json",
		))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})

	t.Run("metadata mismatch", func(t *testing.T) {
		ctx, fence, snapshot, node, receiver, request :=
			newCheckpointReplicaReceiverFixture(t)
		_, err := receiver.InstallCheckpointRestoreSnapshot(
			ctx, fence, snapshot,
		)
		require.NoError(t, err)
		response, err := receiver.HandleCheckpointReplica(ctx, request)
		require.NoError(t, err)
		require.True(t, response.Completed)

		node.setDigest(strings.Repeat("f", 64))
		_, err = receiver.HandleCheckpointReplica(ctx, request)
		require.ErrorContains(t, err, "metadata digest mismatch")
		require.True(t, node.discarded)
		_, statErr := os.Stat(filepath.Join(
			filepath.Dir(snapshot.Metadata.Path), "receipt.json",
		))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})

	t.Run("message mismatch", func(t *testing.T) {
		ctx, fence, snapshot, node, receiver, request :=
			newCheckpointReplicaReceiverFixture(t)
		_, err := receiver.InstallCheckpointRestoreSnapshot(
			ctx, fence, snapshot,
		)
		require.NoError(t, err)
		node.mu.Lock()
		node.messageEvidenceOverride = strings.Repeat("e", 64)
		node.mu.Unlock()
		_, err = receiver.HandleCheckpointReplica(ctx, request)
		require.ErrorContains(t, err, "live message content mismatch")
		require.True(t, node.discarded)
		_, statErr := os.Stat(filepath.Join(
			filepath.Dir(snapshot.Metadata.Path), "receipt.json",
		))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})

	t.Run("missing cleanup index", func(t *testing.T) {
		ctx, fence, snapshot, node, receiver, request :=
			newCheckpointReplicaReceiverFixture(t)
		_, err := receiver.InstallCheckpointRestoreSnapshot(
			ctx, fence, snapshot,
		)
		require.NoError(t, err)
		require.NoError(t, os.RemoveAll(filepath.Join(
			filepath.Dir(snapshot.Metadata.Path), "replica-boundaries",
		)))
		_, err = receiver.HandleCheckpointReplica(ctx, request)
		require.Error(t, err)
		require.True(t, node.discarded)
		_, statErr := os.Stat(filepath.Join(
			filepath.Dir(snapshot.Metadata.Path), "receipt.json",
		))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})

	t.Run("route changes during verification", func(t *testing.T) {
		ctx, fence, snapshot, node, receiver, request :=
			newCheckpointReplicaReceiverFixture(t)
		_, err := receiver.InstallCheckpointRestoreSnapshot(
			ctx, fence, snapshot,
		)
		require.NoError(t, err)
		node.mu.Lock()
		node.changeRouteOnEvidence = true
		node.mu.Unlock()
		_, err = receiver.HandleCheckpointReplica(ctx, request)
		require.ErrorContains(t, err, "Slot fence is stale")
	})

	t.Run("cancellation preserves existing receipt", func(t *testing.T) {
		ctx, fence, snapshot, node, receiver, request :=
			newCheckpointReplicaReceiverFixture(t)
		_, err := receiver.InstallCheckpointRestoreSnapshot(
			ctx, fence, snapshot,
		)
		require.NoError(t, err)
		canceledCtx, cancel := context.WithCancel(context.Background())
		node.mu.Lock()
		node.cancelOnEvidence = cancel
		node.mu.Unlock()
		_, err = receiver.HandleCheckpointReplica(canceledCtx, request)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, node.discarded)
		require.FileExists(t, filepath.Join(
			filepath.Dir(snapshot.Metadata.Path), "receipt.json",
		))
		response, err := receiver.HandleCheckpointReplica(
			context.Background(), request,
		)
		require.NoError(t, err)
		require.True(t, response.Completed)
	})
}

func newCheckpointReplicaReceiverFixture(
	t *testing.T,
) (
	context.Context,
	backupinfra.CheckpointRestoreInstallFence,
	backupinfra.CheckpointRestoreSnapshot,
	*checkpointReplicaTestNode,
	*backupinfra.CheckpointRestoreReplicaReceiver,
	backupcontract.CheckpointReplicaRequest,
) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	fence, snapshot := buildCheckpointReplicaSnapshot(t, root)
	require.NoError(t, os.Remove(
		filepath.Join(filepath.Dir(snapshot.Metadata.Path), "receipt.json"),
	))
	node := &checkpointReplicaTestNode{
		id: 1,
		route: clusterpkg.Route{
			HashSlot: fence.HashSlot, SlotID: fence.TargetSlotID,
			Leader: fence.LeaderNodeID, LeaderTerm: fence.LeaderTerm,
			ConfigEpoch: fence.ConfigEpoch, Peers: []uint64{1, 2, 3},
		},
	}
	receiver, err := backupinfra.NewCheckpointRestoreReplicaReceiver(
		backupinfra.CheckpointRestoreReplicaReceiverOptions{
			Node: node, StagingDir: root, StagingMaxBytes: 32 << 20,
		},
	)
	require.NoError(t, err)
	request := backupcontract.CheckpointReplicaRequest{
		Action: backupcontract.CheckpointReplicaStatus,
		Fence: backupcontract.CheckpointReplicaFence{
			PlanID: fence.PlanID, CheckpointID: fence.CheckpointID,
			CheckpointSHA256: fence.CheckpointSHA256,
			TargetGeneration: fence.TargetGeneration,
			HashSlot:         fence.HashSlot,
			TargetSlotID:     fence.TargetSlotID,
			ReplicaCount:     fence.ReplicaCount,
			LeaderNodeID:     fence.LeaderNodeID,
			LeaderTerm:       fence.LeaderTerm,
			ConfigEpoch:      fence.ConfigEpoch,
			Attempt:          fence.Attempt,
			InvalidateTokens: fence.InvalidateTokens,
		},
	}
	return ctx, fence, snapshot, node, receiver, request
}

func TestCheckpointRestoreReplicaDiscardsPartialLiveInstall(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fence, snapshot := buildCheckpointReplicaSnapshot(t, root)
	require.NoError(t, os.Remove(
		filepath.Join(filepath.Dir(snapshot.Metadata.Path), "receipt.json"),
	))
	node := &checkpointReplicaTestNode{
		id: 1, failMessageInstall: true,
		route: clusterpkg.Route{
			HashSlot: fence.HashSlot, SlotID: fence.TargetSlotID,
			Leader: fence.LeaderNodeID, LeaderTerm: fence.LeaderTerm,
			ConfigEpoch: fence.ConfigEpoch, Peers: []uint64{1, 2, 3},
		},
	}
	receiver, err := backupinfra.NewCheckpointRestoreReplicaReceiver(
		backupinfra.CheckpointRestoreReplicaReceiverOptions{
			Node: node, StagingDir: root, StagingMaxBytes: 32 << 20,
		},
	)
	require.NoError(t, err)
	_, err = receiver.InstallCheckpointRestoreSnapshot(
		ctx, fence, snapshot,
	)
	require.ErrorContains(t, err, "injected message install failure")
	require.True(t, node.discarded)
	require.Empty(t, node.digest)
	require.Empty(t, node.messageSnapshots)
}

func buildCheckpointReplicaSnapshot(
	t *testing.T,
	root string,
) (
	backupinfra.CheckpointRestoreInstallFence,
	backupinfra.CheckpointRestoreSnapshot,
) {
	t.Helper()
	ctx := context.Background()
	distributor := &recordingCheckpointSnapshotDistributor{}
	now := time.UnixMilli(1_753_400_400_000).UTC()
	target, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir: root, StagingMaxBytes: 64 << 20,
			Distributor: distributor,
			Now:         func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	fence := backupinfra.CheckpointRestoreInstallFence{
		PlanID: "replica-plan", CheckpointID: "replica-checkpoint",
		CheckpointSHA256: strings.Repeat("c", 64),
		TargetGeneration: "replica-target-generation",
		HashSlot:         0, TargetSlotID: 9, ReplicaCount: 3,
		LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 11,
		Attempt: 1, InvalidateTokens: true,
	}
	session, err := target.BeginCheckpointRestore(ctx, fence, 32<<20)
	require.NoError(t, err)
	index := session.(backupinfra.CheckpointRestoreEvidenceSession).
		RestoreEvidenceIndex()
	accumulator := backupartifact.NewRestoreEvidenceAccumulatorWithIndex(
		fence.HashSlot, index,
	)
	command := fsm.EncodeUpsertUserCommand(metadb.User{
		UID: "replica-user", Token: "secret",
		DeviceFlag: 1, DeviceLevel: 1,
	})
	metadataBody, err := backupartifact.MarshalMetadataLogRecord(
		backupartifact.MetadataLogRecord{
			HashSlot: fence.HashSlot, RaftIndex: 1, RaftTerm: 1,
			CommittedAtUnixMillis: now.Add(-time.Second).UnixMilli(),
			Command:               command,
		},
	)
	require.NoError(t, err)
	metadata, err := accumulator.AddMetadata(metadataBody)
	require.NoError(t, err)
	require.NoError(t, session.ApplyMetadata(ctx, metadata))

	boundary := backupartifact.ChannelBoundary{
		ChannelID: "replica-room", ChannelType: 1,
		Epoch: 3, HW: 2,
	}
	require.NoError(t, accumulator.MergeBoundary(boundary))
	require.NoError(t, session.ApplyMessageBoundary(ctx, boundary))
	for sequence := uint64(1); sequence <= 2; sequence++ {
		messageBody, err := backupartifact.MarshalMessageLogRecord(
			backupartifact.MessageLogRecord{
				Kind:        backupartifact.MessageLogRecordMessage,
				HashSlot:    fence.HashSlot,
				ChannelID:   boundary.ChannelID,
				ChannelType: boundary.ChannelType,
				Epoch:       boundary.Epoch, HW: boundary.HW,
				MessageSeq: sequence, MessageID: 500 + sequence,
				ServerTimestampMS: now.Add(-time.Second).UnixMilli(),
				Payload:           []byte(strings.Repeat("x", 1024)),
			},
		)
		require.NoError(t, err)
		message, err := accumulator.AddMessage(messageBody)
		require.NoError(t, err)
		require.NoError(t, session.ApplyMessage(ctx, message))
	}
	evidence, err := accumulator.Finish()
	require.NoError(t, err)
	require.NoError(t, session.StagePermanentErasure(
		ctx, backupinfra.PermanentErasureBoundary{
			ChannelID:   boundary.ChannelID,
			ChannelType: boundary.ChannelType,
			ThroughSeq:  1,
		},
	))
	_, err = session.Finalize(ctx, evidence, 4096)
	require.NoError(t, err)
	return fence, distributor.snapshot
}

type checkpointReplicaTestNode struct {
	mu                      sync.Mutex
	id                      uint64
	route                   clusterpkg.Route
	digest                  string
	verified                []clusterpkg.RestoreVerifyBoundary
	erasures                []clusterpkg.RestorePermanentErasure
	messageSnapshots        [][]byte
	messageEvidenceOverride string
	changeRouteOnEvidence   bool
	cancelOnEvidence        context.CancelFunc
	discarded               bool
	failMessageInstall      bool
}

func (n *checkpointReplicaTestNode) NodeID() uint64 { return n.id }

func (n *checkpointReplicaTestNode) RouteHashSlot(
	_ uint16,
) (clusterpkg.Route, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	route := n.route
	route.Peers = append([]uint64(nil), route.Peers...)
	return route, nil
}

func (n *checkpointReplicaTestNode) setRoute(route clusterpkg.Route) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.route = route
}

func (n *checkpointReplicaTestNode) setDigest(digest string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.digest = digest
}

func (n *checkpointReplicaTestNode) InstallRestoreHashSlotMetadata(
	ctx context.Context,
	hashSlot uint16,
	reader io.ReadSeeker,
	size int64,
	_ bool,
) (uint64, error) {
	slots, stats, err := metadb.ReplayBackupHashSlotSnapshot(
		ctx, reader, size,
		func(metadb.BackupSnapshotEntry) error { return nil },
	)
	if err != nil || len(slots) != 1 || slots[0] != hashSlot {
		return 0, errors.Join(err, errors.New("metadata Slot mismatch"))
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return 0, err
	}
	n.mu.Lock()
	n.digest = hex.EncodeToString(hash.Sum(nil))
	n.mu.Unlock()
	return stats.EntryCount, nil
}

func (n *checkpointReplicaTestNode) InstallRestoreMessageStream(
	ctx context.Context,
	reader io.ReadSeeker,
	size int64,
) (channelstore.BackupSnapshotStats, error) {
	stats, err := messagedb.ReplayBackupSnapshotReader(
		ctx, reader, size,
		func(messagedb.BackupSnapshotBoundary) error { return nil },
		func(messagedb.BackupSnapshotRecord) error { return nil },
	)
	if err != nil {
		return stats, err
	}
	n.mu.Lock()
	fail := n.failMessageInstall
	n.mu.Unlock()
	if fail {
		return stats, errors.New("injected message install failure")
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return stats, err
	}
	body, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil || int64(len(body)) != size {
		return stats, errors.Join(err, io.ErrUnexpectedEOF)
	}
	n.mu.Lock()
	n.messageSnapshots = append(n.messageSnapshots, body)
	n.mu.Unlock()
	return stats, nil
}

func (n *checkpointReplicaTestNode) ApplyRestorePermanentErasures(
	_ context.Context,
	_ uint16,
	erasures []clusterpkg.RestorePermanentErasure,
) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.erasures = append(
		n.erasures,
		append([]clusterpkg.RestorePermanentErasure(nil), erasures...)...,
	)
	return nil
}

func (n *checkpointReplicaTestNode) InstallRestoreChannelRuntimeMeta(
	_ context.Context,
	_ uint16,
	boundaries []clusterpkg.RestoreVerifyBoundary,
) error {
	if len(boundaries) > 0 {
		n.setDigest(strings.Repeat("d", 64))
	}
	return nil
}

func (n *checkpointReplicaTestNode) RestoreHashSlotMetadataDigest(
	context.Context,
	uint16,
) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.digest, nil
}

func (n *checkpointReplicaTestNode) VerifyLocalRestorePartition(
	_ context.Context,
	_ uint16,
	digest string,
	boundaries []clusterpkg.RestoreVerifyBoundary,
) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if digest != "" && digest != n.digest {
		return errors.New("metadata digest mismatch")
	}
	n.verified = append(
		n.verified, append([]clusterpkg.RestoreVerifyBoundary(nil), boundaries...)...,
	)
	return nil
}

func (n *checkpointReplicaTestNode) RestoreLiveMessageSnapshotEvidence(
	ctx context.Context,
	_ uint16,
	_ []clusterpkg.RestoreVerifyBoundary,
) (clusterpkg.RestoreMessageSnapshotEvidence, error) {
	n.mu.Lock()
	if len(n.messageSnapshots) == 0 {
		n.mu.Unlock()
		return clusterpkg.RestoreMessageSnapshotEvidence{},
			errors.New("message snapshot missing")
	}
	body := append([]byte(nil), n.messageSnapshots[0]...)
	override := n.messageEvidenceOverride
	changeRoute := n.changeRouteOnEvidence
	cancel := n.cancelOnEvidence
	n.changeRouteOnEvidence = false
	n.cancelOnEvidence = nil
	n.mu.Unlock()
	if cancel != nil {
		cancel()
		return clusterpkg.RestoreMessageSnapshotEvidence{}, ctx.Err()
	}
	stats, err := messagedb.ReplayBackupSnapshotReader(
		ctx, bytes.NewReader(body), int64(len(body)),
		func(messagedb.BackupSnapshotBoundary) error { return nil },
		func(messagedb.BackupSnapshotRecord) error { return nil },
	)
	if err != nil {
		return clusterpkg.RestoreMessageSnapshotEvidence{}, err
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	if override != "" {
		digest = override
	}
	if changeRoute {
		n.mu.Lock()
		n.route.LeaderTerm++
		n.mu.Unlock()
	}
	return clusterpkg.RestoreMessageSnapshotEvidence{
		Size: int64(len(body)), SHA256: digest,
		ChannelCount: stats.ChannelCount,
		MessageCount: stats.MessageCount,
		MaxMessageID: stats.MaxMessageID,
	}, nil
}

func (n *checkpointReplicaTestNode) DiscardLocalRestorePartition(
	_ context.Context,
	_ uint16,
	_ []clusterpkg.RestoreVerifyBoundary,
) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.digest = ""
	n.messageSnapshots = nil
	n.erasures = nil
	n.discarded = true
	return nil
}

type checkpointReplicaTestRemote struct {
	mu             sync.Mutex
	receivers      map[uint64]*backupinfra.CheckpointRestoreReplicaReceiver
	failNode       uint64
	failChunk      int
	chunks         int
	failed         bool
	resumeObserved bool
}

type cancelingCheckpointReplicaInstaller struct {
	cancel context.CancelFunc
}

func (i cancelingCheckpointReplicaInstaller) InstallCheckpointRestoreSnapshot(
	_ context.Context,
	_ backupinfra.CheckpointRestoreInstallFence,
	_ backupinfra.CheckpointRestoreSnapshot,
) (backupcontract.CheckpointReplicaResponse, error) {
	i.cancel()
	return backupcontract.CheckpointReplicaResponse{
		Completed:      true,
		MetadataSHA256: strings.Repeat("d", 64),
	}, nil
}

type canceledCheckpointReplicaRemote struct{}

func (canceledCheckpointReplicaRemote) HandleCheckpointReplica(
	ctx context.Context,
	_ uint64,
	_ backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	return backupcontract.CheckpointReplicaResponse{}, ctx.Err()
}

func (r *checkpointReplicaTestRemote) HandleCheckpointReplica(
	ctx context.Context,
	nodeID uint64,
	request backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	r.mu.Lock()
	if nodeID == r.failNode &&
		request.Action == backupcontract.CheckpointReplicaChunk {
		r.chunks++
		if !r.failed && r.chunks == r.failChunk {
			r.failed = true
			r.mu.Unlock()
			return backupcontract.CheckpointReplicaResponse{},
				errors.New("injected replica transport interruption")
		}
	}
	r.mu.Unlock()
	receiver := r.receivers[nodeID]
	if receiver == nil {
		return backupcontract.CheckpointReplicaResponse{},
			errors.New("missing replica receiver")
	}
	response, err := receiver.HandleCheckpointReplica(ctx, request)
	if err == nil && request.Action == backupcontract.CheckpointReplicaChunk &&
		response.AcceptedOffset >
			request.Offset+int64(len(request.Data)) {
		r.mu.Lock()
		r.resumeObserved = true
		r.mu.Unlock()
	}
	return response, err
}
