package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestDistributedRestoreStagesAndVerifiesEveryCurrentReplica(t *testing.T) {
	state := restoreControllerState()
	cluster := &restoreClusterStub{nodeID: 9, state: state}
	remote := &restoreRemoteStub{logicalBytes: 123}
	executor := newDistributedRestoreExecutor(t, cluster, remote)
	job := backupcontract.RestoreJob{
		ID: "restore-1", BackupID: "backup-1",
		Slots: []backupcontract.RestoreSlotProgress{{
			HashSlot: 0, Status: backupcontract.RestoreSlotStatusStaged,
			Attempt: 1, ReplicaNodeIDs: []uint64{1, 2}, LogicalBytes: 123,
		}},
	}

	previous, err := executor.EnterMaintenance(context.Background(), job)
	if err != nil {
		t.Fatalf("EnterMaintenance(): %v", err)
	}
	if previous != "controller-revision-42" {
		t.Fatalf("previous activation = %q", previous)
	}
	assertRestoreCalls(t, remote.takeCalls(), []restoreCall{
		{nodeID: 1, action: backupcontract.RestoreNodeActionPrepare},
		{nodeID: 2, action: backupcontract.RestoreNodeActionPrepare},
	})

	result, err := executor.StageSlot(context.Background(), job, 0, 1)
	if err != nil {
		t.Fatalf("StageSlot(): %v", err)
	}
	if result.LogicalBytes != 123 ||
		len(result.ReplicaNodeIDs) != 2 ||
		result.ReplicaNodeIDs[0] != 1 ||
		result.ReplicaNodeIDs[1] != 2 {
		t.Fatalf("stage result = %#v", result)
	}
	assertRestoreCalls(t, remote.takeCalls(), []restoreCall{
		{nodeID: 1, action: backupcontract.RestoreNodeActionStage},
		{nodeID: 2, action: backupcontract.RestoreNodeActionStage},
	})

	if err := executor.VerifySlot(context.Background(), job, 0, 1); err != nil {
		t.Fatalf("VerifySlot(): %v", err)
	}
	assertRestoreCalls(t, remote.takeCalls(), []restoreCall{
		{nodeID: 1, action: backupcontract.RestoreNodeActionVerify},
		{nodeID: 2, action: backupcontract.RestoreNodeActionVerify},
	})
}

func TestDistributedRestoreRefusesSwitchAfterTopologyChanges(t *testing.T) {
	state := restoreControllerState()
	cluster := &restoreClusterStub{nodeID: 9, state: state}
	remote := &restoreRemoteStub{logicalBytes: 123}
	executor := newDistributedRestoreExecutor(t, cluster, remote)
	job := backupcontract.RestoreJob{
		ID: "restore-1", BackupID: "backup-1",
		Slots: []backupcontract.RestoreSlotProgress{{
			HashSlot: 0, Status: backupcontract.RestoreSlotStatusVerified,
			Attempt: 1, ReplicaNodeIDs: []uint64{1, 3}, LogicalBytes: 123,
		}},
	}

	if err := executor.ActivateRestore(context.Background(), job); err == nil {
		t.Fatal("ActivateRestore() error = nil")
	}
	if calls := remote.takeCalls(); len(calls) != 0 {
		t.Fatalf("remote calls = %#v", calls)
	}
}

func TestDistributedRestorePreflightChecksEveryActiveNodeBeforeAdmission(t *testing.T) {
	state := restoreControllerState()
	state.ClusterID = "cluster-1"
	state.Config.HashSlotCount = backupcontract.HashSlotCount
	state.ScheduledBackup.ActiveRestore = nil
	for index := range state.Nodes {
		if state.Nodes[index].JoinState == controller.NodeJoinStateActive {
			state.Nodes[index].Status = controller.NodeStatusAlive
			state.NodeHealthReports = append(
				state.NodeHealthReports,
				controller.NodeHealthReport{
					NodeID:                  state.Nodes[index].NodeID,
					Status:                  controller.NodeStatusAlive,
					RuntimeReady:            true,
					ObservedControlRevision: state.Revision,
					ReportedAtUnixMilli:     time.Now().UTC().UnixMilli(),
				},
			)
		}
	}
	cluster := &restoreClusterStub{nodeID: 9, state: state}
	remote := &restoreRemoteStub{availableBytes: 10 << 30}
	executor := newDistributedRestoreExecutor(t, cluster, remote)
	err := executor.Check(
		context.Background(),
		backupcontract.RestoreJob{
			ID: "restore-preflight", BackupID: "backup-1",
			TargetActivation: "activation-preflight",
		},
		backupcontract.Plan{
			Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		},
		backupartifact.ArchiveManifest{
			SourceClusterID: "cluster-1", LogicalBytes: 1 << 20,
		},
	)
	if err != nil {
		t.Fatalf("Check(): %v", err)
	}
	assertRestoreCalls(t, remote.takeCalls(), []restoreCall{
		{nodeID: 1, action: backupcontract.RestoreNodeActionPreflight},
		{nodeID: 2, action: backupcontract.RestoreNodeActionPreflight},
	})

	state.NodeHealthReports = state.NodeHealthReports[:1]
	cluster.state = state
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := executor.Check(
		ctx,
		backupcontract.RestoreJob{
			ID: "restore-preflight", BackupID: "backup-1",
			TargetActivation: "activation-preflight",
		},
		backupcontract.Plan{
			Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		},
		backupartifact.ArchiveManifest{
			SourceClusterID: "cluster-1", LogicalBytes: 1 << 20,
		},
	); err == nil {
		t.Fatal("Check(missing health report) error = nil")
	}
}

func TestDistributedRestorePreflightWaitsForHealthRevisionConvergence(t *testing.T) {
	converged := restoreControllerState()
	converged.ClusterID = "cluster-1"
	converged.Config.HashSlotCount = backupcontract.HashSlotCount
	converged.ScheduledBackup.ActiveRestore = nil
	for index := range converged.Nodes {
		if converged.Nodes[index].JoinState != controller.NodeJoinStateActive {
			continue
		}
		converged.Nodes[index].Status = controller.NodeStatusAlive
		converged.NodeHealthReports = append(
			converged.NodeHealthReports,
			controller.NodeHealthReport{
				NodeID:                  converged.Nodes[index].NodeID,
				Status:                  controller.NodeStatusAlive,
				RuntimeReady:            true,
				ObservedControlRevision: converged.Revision,
				ReportedAtUnixMilli:     time.Now().UTC().UnixMilli(),
			},
		)
	}
	stale := converged.Clone()
	for index := range stale.NodeHealthReports {
		stale.NodeHealthReports[index].ObservedControlRevision--
	}
	cluster := &restoreClusterStub{
		nodeID: 9,
		state:  converged,
		states: []controller.ClusterState{stale, converged},
	}
	remote := &restoreRemoteStub{availableBytes: 10 << 30}
	executor := newDistributedRestoreExecutor(t, cluster, remote)

	err := executor.Check(
		context.Background(),
		backupcontract.RestoreJob{
			ID: "restore-preflight", BackupID: "backup-1",
			TargetActivation: "activation-preflight",
		},
		backupcontract.Plan{
			Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		},
		backupartifact.ArchiveManifest{
			SourceClusterID: "cluster-1", LogicalBytes: 1 << 20,
		},
	)
	if err != nil {
		t.Fatalf("Check(): %v", err)
	}
	if cluster.stateCalls < 2 {
		t.Fatalf("LocalState() calls = %d, want at least 2", cluster.stateCalls)
	}
}

func newDistributedRestoreExecutor(
	t *testing.T,
	cluster *restoreClusterStub,
	remote *restoreRemoteStub,
) *backupinfra.DistributedRestoreExecutor {
	t.Helper()
	cipher, err := backupinfra.NewCredentialCipher(
		"manager-secret-for-restore-tests", "cluster-1",
	)
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	provider, err := backupinfra.NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	store, err := provider.Open(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
	)
	if err != nil {
		t.Fatalf("Open(repository): %v", err)
	}
	writeRestoreArchiveMetadata(t, store, "backup-1")
	local, err := backupinfra.NewStagedRestoreNodeService(
		restorePartitionNodeStub{}, provider, t.TempDir(),
	)
	if err != nil {
		t.Fatalf("NewStagedRestoreNodeService(): %v", err)
	}
	local.SetMaintenanceQuiescer(func(context.Context) error { return nil })
	local.SetMaintenanceResumer(func(context.Context) error { return nil })
	executor, err := backupinfra.NewDistributedRestoreExecutor(
		cluster, local, remote,
	)
	if err != nil {
		t.Fatalf("NewDistributedRestoreExecutor(): %v", err)
	}
	return executor
}

func writeRestoreArchiveMetadata(
	t *testing.T,
	store backupartifact.ArchiveStore,
	backupID string,
) {
	t.Helper()
	slots := make(
		[]backupartifact.SlotReference,
		backupartifact.DefaultHashSlotCount,
	)
	for hashSlot := range slots {
		sum := sha256.Sum256([]byte(fmt.Sprintf("slot-%d", hashSlot)))
		slots[hashSlot] = backupartifact.SlotReference{
			HashSlot: uint16(hashSlot),
			ManifestKey: fmt.Sprintf(
				"slots/%03d/attempts/00000001/manifest.json", hashSlot,
			),
			ManifestSHA256: hex.EncodeToString(sum[:]),
		}
	}
	manifest := backupartifact.ArchiveManifest{
		Format: backupartifact.ArchiveFormat, Version: backupartifact.ArchiveVersion,
		ID: backupID, Trigger: backupartifact.TriggerManual,
		SourceClusterID: "cluster-1", SourceApplication: "test",
		HashSlotCount:         backupartifact.DefaultHashSlotCount,
		StartedAtUnixMillis:   1_800_000_000_000,
		CompletedAtUnixMillis: 1_800_000_001_000,
		CutStartedUnixMillis:  1_800_000_000_100,
		CutEndedUnixMillis:    1_800_000_000_900,
		Compression:           backupartifact.CompressionZstd,
		Checksum:              backupartifact.ChecksumSHA256, Slots: slots,
	}
	body, err := backupartifact.MarshalArchiveManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalArchiveManifest(): %v", err)
	}
	marker, err := backupartifact.NewCompleteMarker(body)
	if err != nil {
		t.Fatalf("NewCompleteMarker(): %v", err)
	}
	markerBody, err := backupartifact.MarshalCompleteMarker(marker)
	if err != nil {
		t.Fatalf("MarshalCompleteMarker(): %v", err)
	}
	for key, value := range map[string][]byte{
		"backups/" + backupID + "/manifest.json": body,
		"backups/" + backupID + "/COMPLETE":      markerBody,
	} {
		if err := store.Put(context.Background(), backupartifact.PutObject{
			Key: key, Body: bytes.NewReader(value),
			ExpectedBytes: uint64(len(value)),
		}); err != nil {
			t.Fatalf("Put(%s): %v", key, err)
		}
	}
}

func restoreControllerState() controller.ClusterState {
	return controller.ClusterState{
		Revision: 42,
		Nodes: []controller.Node{
			{NodeID: 1, Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive},
			{NodeID: 2, Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive},
			{NodeID: 3, Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateJoining},
		},
		Slots: []controller.SlotAssignment{{
			SlotID: 1, DesiredPeers: []uint64{2, 1}, ConfigEpoch: 7,
		}},
		HashSlots: controller.HashSlotTable{
			SlotCount: backupcontract.HashSlotCount,
			Ranges: []controller.HashSlotRange{{
				From: 0, To: backupcontract.HashSlotCount - 1, SlotID: 1,
			}},
		},
		ScheduledBackup: &controller.ScheduledBackupState{
			Revision: 1,
			Plan: &controller.BackupPlan{
				Revision: 1,
				Store: controller.BackupStoreConfig{
					Kind: controller.BackupStoreKind("file"),
				},
			},
			ActiveRestore: &controller.ScheduledRestoreJob{
				ID: "restore-1", BackupID: "backup-1",
			},
		},
	}
}

type restoreClusterStub struct {
	nodeID     uint64
	state      controller.ClusterState
	states     []controller.ClusterState
	stateCalls int
}

func (s *restoreClusterStub) NodeID() uint64 { return s.nodeID }
func (s *restoreClusterStub) BackupControllerFence(
	context.Context,
) (uint64, uint64, error) {
	return s.nodeID, 7, nil
}

func (s *restoreClusterStub) LocalState(context.Context) (controller.ClusterState, error) {
	s.stateCalls++
	if len(s.states) > 0 {
		state := s.states[0]
		s.states = s.states[1:]
		return state, nil
	}
	return s.state, nil
}

type restoreCall struct {
	nodeID uint64
	action backupcontract.RestoreNodeAction
}

type restoreRemoteStub struct {
	logicalBytes   uint64
	availableBytes uint64
	calls          []restoreCall
}

func (s *restoreRemoteStub) RunBackupRestoreNode(
	_ context.Context,
	nodeID uint64,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error) {
	s.calls = append(s.calls, restoreCall{nodeID: nodeID, action: command.Action})
	return backupcontract.RestoreNodeReceipt{
		LogicalBytes: s.logicalBytes, AvailableBytes: s.availableBytes,
	}, nil
}

func (s *restoreRemoteStub) takeCalls() []restoreCall {
	calls := append([]restoreCall(nil), s.calls...)
	s.calls = nil
	return calls
}

func assertRestoreCalls(t *testing.T, got []restoreCall, want []restoreCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("calls[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

type restorePartitionNodeStub struct{}

func (restorePartitionNodeStub) RestoreMaintenanceReady() bool { return true }
func (restorePartitionNodeStub) NodeID() uint64                { return 9 }
func (restorePartitionNodeStub) BackupControllerFence(
	context.Context,
) (uint64, uint64, error) {
	return 1, 7, nil
}
func (restorePartitionNodeStub) LocalState(
	context.Context,
) (controller.ClusterState, error) {
	return restoreControllerState(), nil
}

func (restorePartitionNodeStub) OpenLocalRestoreMetadataSnapshot(
	context.Context,
	uint16,
) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader([]byte("metadata"))), nil
}

func (restorePartitionNodeStub) OpenLocalRestoreMessageSnapshot(
	context.Context,
	uint16,
) (clusterpkg.BackupMessageSnapshot, error) {
	return clusterpkg.BackupMessageSnapshot{
		Reader: io.NopCloser(bytes.NewReader([]byte("messages"))),
	}, nil
}

func (restorePartitionNodeStub) VerifyLocalRestorePartitionStreams(
	context.Context,
	uint16,
	io.ReadSeeker,
	int64,
	[]clusterpkg.RestoreMessageStream,
) (uint64, error) {
	return 1, nil
}

func (restorePartitionNodeStub) InstallLocalRestorePartition(
	context.Context,
	uint16,
	io.ReadSeeker,
	int64,
	[]clusterpkg.RestoreMessageStream,
) error {
	return nil
}

func (restorePartitionNodeStub) ActivateLocalRestore(context.Context) error {
	return nil
}
func (restorePartitionNodeStub) CheckLocalRestoreHealth(context.Context) error {
	return nil
}

var _ backupusecase.RestoreExecutor = (*backupinfra.DistributedRestoreExecutor)(nil)
