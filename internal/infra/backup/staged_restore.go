package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"golang.org/x/sys/unix"
)

const restoreFileMode = 0o600

const (
	restoreMarkerSwitching = "SWITCHING"
	restoreMarkerSwitched  = "SWITCHED"
)

// RestorePartitionNode owns node-local live storage while Controller
// maintenance fences ordinary business traffic.
type RestorePartitionNode interface {
	NodeID() uint64
	BackupControllerFence(context.Context) (uint64, uint64, error)
	LocalState(context.Context) (controller.ClusterState, error)
	RestoreMaintenanceReady() bool
	OpenLocalRestoreMetadataSnapshot(
		context.Context,
		uint16,
	) (io.ReadCloser, error)
	OpenLocalRestoreMessageSnapshot(
		context.Context,
		uint16,
	) (clusterpkg.BackupMessageSnapshot, error)
	VerifyLocalRestorePartitionStreams(
		context.Context,
		uint16,
		io.ReadSeeker,
		int64,
		[]clusterpkg.RestoreMessageStream,
	) (uint64, error)
	InstallLocalRestorePartition(
		context.Context,
		uint16,
		io.ReadSeeker,
		int64,
		[]clusterpkg.RestoreMessageStream,
	) error
	ActivateLocalRestore(context.Context) error
	CheckLocalRestoreHealth(context.Context) error
}

// StagedRestoreNodeService stages, verifies, switches, and rolls back one
// replica using only local files and the shared archive repository.
type StagedRestoreNodeService struct {
	node           RestorePartitionNode
	repository     *RepositoryProvider
	root           string
	messageIDFloor func(uint64) error
	quiesce        func(context.Context) error
	resume         func(context.Context) error
	// operationMu serializes all node-local restore actions. The Controller
	// fence is re-read while holding it so an old coordinator cannot overlap a
	// newer switch or rollback after leadership changes.
	operationMu sync.Mutex
}

// SetMessageIDFloor installs the node-local allocator fence applied during
// activation while Controller maintenance still blocks business traffic.
func (s *StagedRestoreNodeService) SetMessageIDFloor(set func(uint64) error) {
	if s != nil {
		s.messageIDFloor = set
	}
}

// SetMaintenanceQuiescer installs the app-local drain barrier that must pass on
// every data node before Prepare acknowledges Controller maintenance.
func (s *StagedRestoreNodeService) SetMaintenanceQuiescer(
	quiesce func(context.Context) error,
) {
	if s != nil {
		s.quiesce = quiesce
	}
}

// SetMaintenanceResumer installs the node-local runtime restart barrier used
// in Finalizing before Controller maintenance is cleared.
func (s *StagedRestoreNodeService) SetMaintenanceResumer(
	resume func(context.Context) error,
) {
	if s != nil {
		s.resume = resume
	}
}

// NewStagedRestoreNodeService creates the node-local restore endpoint.
func NewStagedRestoreNodeService(
	node RestorePartitionNode,
	repository *RepositoryProvider,
	dataDir string,
) (*StagedRestoreNodeService, error) {
	if node == nil || repository == nil || dataDir == "" {
		return nil, fmt.Errorf("backup staged restore: dependencies are required")
	}
	root, err := filepath.Abs(filepath.Join(dataDir, "backup-restore"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &StagedRestoreNodeService{
		node: node, repository: repository, root: root,
	}, nil
}

// Run executes one idempotent replica-local restore action.
func (s *StagedRestoreNodeService) Run(
	ctx context.Context,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error) {
	if err := s.validate(command); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	leaderID, term, err := s.node.BackupControllerFence(ctx)
	if err != nil ||
		leaderID != command.CoordinatorNodeID ||
		term != command.CoordinatorTerm {
		return backupcontract.RestoreNodeReceipt{},
			fmt.Errorf("backup staged restore: stale coordinator fence")
	}
	if err := s.validateFence(ctx, command); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	if command.Action == backupcontract.RestoreNodeActionPreflight {
		return s.preflight(ctx, command)
	}
	if command.Action == backupcontract.RestoreNodeActionPrepare {
		if !s.node.RestoreMaintenanceReady() {
			return backupcontract.RestoreNodeReceipt{},
				fmt.Errorf("backup staged restore: maintenance is not active")
		}
		if s.quiesce == nil {
			return backupcontract.RestoreNodeReceipt{},
				fmt.Errorf("backup staged restore: node quiescence is unavailable")
		}
		if err := s.quiesce(ctx); err != nil {
			return backupcontract.RestoreNodeReceipt{},
				fmt.Errorf("backup staged restore: node quiescence: %w", err)
		}
		return backupcontract.RestoreNodeReceipt{}, nil
	}
	switch command.Action {
	case backupcontract.RestoreNodeActionStage:
		return s.stage(ctx, command)
	case backupcontract.RestoreNodeActionVerify:
		bytes, err := s.verify(ctx, command, "target")
		return backupcontract.RestoreNodeReceipt{LogicalBytes: bytes}, err
	case backupcontract.RestoreNodeActionSwitch:
		return backupcontract.RestoreNodeReceipt{}, s.switchPartition(ctx, command)
	case backupcontract.RestoreNodeActionActivate:
		if err := s.node.ActivateLocalRestore(ctx); err != nil {
			return backupcontract.RestoreNodeReceipt{}, err
		}
		if s.messageIDFloor != nil {
			if err := s.messageIDFloor(command.MaxMessageID); err != nil {
				return backupcontract.RestoreNodeReceipt{}, err
			}
		}
		return backupcontract.RestoreNodeReceipt{}, nil
	case backupcontract.RestoreNodeActionHealth:
		return backupcontract.RestoreNodeReceipt{},
			s.node.CheckLocalRestoreHealth(ctx)
	case backupcontract.RestoreNodeActionRollback:
		return backupcontract.RestoreNodeReceipt{}, s.rollback(ctx, command)
	case backupcontract.RestoreNodeActionResume:
		if s.resume == nil {
			return backupcontract.RestoreNodeReceipt{},
				fmt.Errorf("backup staged restore: node resume is unavailable")
		}
		return backupcontract.RestoreNodeReceipt{}, s.resume(ctx)
	case backupcontract.RestoreNodeActionCleanup:
		return backupcontract.RestoreNodeReceipt{}, s.cleanup(command)
	default:
		return backupcontract.RestoreNodeReceipt{},
			fmt.Errorf("backup staged restore: unsupported action %q", command.Action)
	}
}

func (s *StagedRestoreNodeService) preflight(
	ctx context.Context,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error) {
	store, err := s.repository.Open(ctx, command.Store)
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	root := "backups/" + command.BackupID + "/"
	manifestBody, err := backupartifact.ReadStoredObject(
		ctx, store, root+"manifest.json", 4<<20,
	)
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	markerBody, err := backupartifact.ReadStoredObject(
		ctx, store, root+"COMPLETE", 4<<20,
	)
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	if _, err := backupartifact.LoadCompleteMarker(
		markerBody, manifestBody,
	); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	manifest, err := backupartifact.LoadArchiveManifest(manifestBody)
	if err != nil || manifest.ID != command.BackupID {
		if err == nil {
			err = fmt.Errorf("backup staged restore: archive identity changed")
		}
		return backupcontract.RestoreNodeReceipt{}, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(s.root, &stat); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	if stat.Bsize <= 0 {
		return backupcontract.RestoreNodeReceipt{},
			fmt.Errorf("backup staged restore: invalid filesystem capacity")
	}
	blockSize := uint64(stat.Bsize)
	available := uint64(stat.Bavail)
	if available > math.MaxUint64/blockSize {
		available = math.MaxUint64
	} else {
		available *= blockSize
	}
	if available < command.RequiredBytes {
		return backupcontract.RestoreNodeReceipt{},
			fmt.Errorf("backup staged restore: insufficient free space")
	}
	currentBusinessBytes, err := s.currentBusinessBytes()
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	return backupcontract.RestoreNodeReceipt{
		AvailableBytes:       available,
		CurrentBusinessBytes: currentBusinessBytes,
	}, nil
}

func (s *StagedRestoreNodeService) currentBusinessBytes() (uint64, error) {
	dataDir := filepath.Dir(s.root)
	var total uint64
	err := filepath.WalkDir(dataDir, func(
		path string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != dataDir {
			switch entry.Name() {
			case "backup-repository", "backup-restore":
				return filepath.SkipDir
			}
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() < 0 || uint64(info.Size()) > math.MaxUint64-total {
			return fmt.Errorf("backup staged restore: business data size overflow")
		}
		total += uint64(info.Size())
		return nil
	})
	return total, err
}

func (s *StagedRestoreNodeService) stage(
	ctx context.Context,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error) {
	if !s.node.RestoreMaintenanceReady() {
		return backupcontract.RestoreNodeReceipt{},
			fmt.Errorf("backup staged restore: maintenance is not active")
	}
	if err := s.captureRollback(ctx, command); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	target := s.targetDir(command)
	if _, err := os.Stat(filepath.Join(target, "READY")); err == nil {
		logicalBytes, verifyErr := s.verify(ctx, command, "target")
		return backupcontract.RestoreNodeReceipt{LogicalBytes: logicalBytes}, verifyErr
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	temporary, err := os.MkdirTemp(parent, ".target-*")
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	store, err := s.repository.Open(ctx, command.Store)
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	_, manifest, err := backupartifact.LoadStoredSlotReference(
		ctx, store, command.BackupID, command.SlotReference, false,
	)
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	if err := decodeRestoreStreams(
		ctx, store, command.BackupID, manifest, temporary,
	); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	if err := os.WriteFile(
		filepath.Join(temporary, "READY"), []byte("ready\n"), restoreFileMode,
	); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	if err := os.RemoveAll(target); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	if err := os.Rename(temporary, target); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	keep = true
	logicalBytes, err := s.verify(ctx, command, "target")
	return backupcontract.RestoreNodeReceipt{LogicalBytes: logicalBytes}, err
}

func (s *StagedRestoreNodeService) captureRollback(
	ctx context.Context,
	command backupcontract.RestoreNodeCommand,
) error {
	target := s.rollbackDir(command)
	if _, err := os.Stat(filepath.Join(target, "READY")); err == nil {
		_, verifyErr := s.verify(ctx, command, "rollback")
		return verifyErr
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".rollback-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	metadata, err := s.node.OpenLocalRestoreMetadataSnapshot(
		ctx, command.HashSlot,
	)
	if err != nil {
		return err
	}
	if err := writeRestoreFile(
		ctx, filepath.Join(temporary, "metadata.bin"), metadata,
	); err != nil {
		return err
	}
	messages, err := s.node.OpenLocalRestoreMessageSnapshot(
		ctx, command.HashSlot,
	)
	if err != nil {
		return err
	}
	if err := writeRestoreFile(
		ctx, filepath.Join(temporary, "messages-000001.bin"), messages.Reader,
	); err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(temporary, "READY"), []byte("ready\n"), restoreFileMode,
	); err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	keep = true
	_, err = s.verify(ctx, command, "rollback")
	return err
}

func (s *StagedRestoreNodeService) verify(
	ctx context.Context,
	command backupcontract.RestoreNodeCommand,
	kind string,
) (uint64, error) {
	directory := s.targetDir(command)
	if kind == "rollback" {
		directory = s.rollbackDir(command)
	}
	metadata, metadataSize, messages, closeFiles, err := openRestoreFiles(
		directory,
	)
	if err != nil {
		return 0, err
	}
	defer closeFiles()
	return s.node.VerifyLocalRestorePartitionStreams(
		ctx, command.HashSlot, metadata, metadataSize, messages,
	)
}

func (s *StagedRestoreNodeService) switchPartition(
	ctx context.Context,
	command backupcontract.RestoreNodeCommand,
) error {
	target := s.targetDir(command)
	switched := filepath.Join(target, restoreMarkerSwitched)
	if _, err := os.Stat(switched); err == nil {
		return nil
	}
	if _, err := s.verify(ctx, command, "target"); err != nil {
		return err
	}
	switching := filepath.Join(target, restoreMarkerSwitching)
	if _, err := os.Stat(switching); errors.Is(err, os.ErrNotExist) {
		if err := writeDurableRestoreMarker(switching, "switching\n"); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	metadata, metadataSize, messages, closeFiles, err := openRestoreFiles(
		target,
	)
	if err != nil {
		return err
	}
	defer closeFiles()
	if err := s.node.InstallLocalRestorePartition(
		ctx, command.HashSlot, metadata, metadataSize, messages,
	); err != nil {
		return err
	}
	if err := writeDurableRestoreMarker(switched, "switched\n"); err != nil {
		return err
	}
	if err := os.Remove(switching); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRestoreDirectory(target)
}

func (s *StagedRestoreNodeService) rollback(
	ctx context.Context,
	command backupcontract.RestoreNodeCommand,
) error {
	target := s.targetDir(command)
	switching := filepath.Join(target, restoreMarkerSwitching)
	switched := filepath.Join(target, restoreMarkerSwitched)
	started, err := restoreMarkerExists(switching)
	if err != nil {
		return err
	}
	completed, err := restoreMarkerExists(switched)
	if err != nil {
		return err
	}
	if !started && !completed {
		return nil
	}
	if _, err := s.verify(ctx, command, "rollback"); err != nil {
		return err
	}
	metadata, metadataSize, messages, closeFiles, err := openRestoreFiles(
		s.rollbackDir(command),
	)
	if err != nil {
		return err
	}
	defer closeFiles()
	if err := s.node.InstallLocalRestorePartition(
		ctx, command.HashSlot, metadata, metadataSize, messages,
	); err != nil {
		return err
	}
	for _, marker := range []string{switched, switching} {
		if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncRestoreDirectory(target)
}

func (s *StagedRestoreNodeService) cleanup(
	command backupcontract.RestoreNodeCommand,
) error {
	return os.RemoveAll(s.jobDir(command))
}

func (s *StagedRestoreNodeService) validate(
	command backupcontract.RestoreNodeCommand,
) error {
	if s == nil || s.node == nil || s.repository == nil ||
		!safeRestoreIdentifier(command.JobID) ||
		!safeRestoreIdentifier(command.BackupID) ||
		!safeRestoreIdentifier(command.TargetActivation) ||
		command.ControllerRevision == 0 ||
		command.CoordinatorNodeID == 0 ||
		command.CoordinatorTerm == 0 ||
		int(command.HashSlot) >= backupcontract.HashSlotCount {
		return fmt.Errorf("backup staged restore: invalid command")
	}
	switch command.Action {
	case backupcontract.RestoreNodeActionPreflight:
		if command.RequiredBytes == 0 {
			return fmt.Errorf("backup staged restore: preflight capacity is required")
		}
	case backupcontract.RestoreNodeActionPrepare,
		backupcontract.RestoreNodeActionHealth,
		backupcontract.RestoreNodeActionResume,
		backupcontract.RestoreNodeActionCleanup:
	case backupcontract.RestoreNodeActionStage,
		backupcontract.RestoreNodeActionVerify,
		backupcontract.RestoreNodeActionSwitch,
		backupcontract.RestoreNodeActionRollback:
		if command.Attempt == 0 {
			return fmt.Errorf("backup staged restore: attempt is required")
		}
	case backupcontract.RestoreNodeActionActivate:
	default:
		return fmt.Errorf("backup staged restore: invalid action")
	}
	return nil
}

func (s *StagedRestoreNodeService) validateFence(
	ctx context.Context,
	command backupcontract.RestoreNodeCommand,
) error {
	state, err := s.node.LocalState(ctx)
	if err != nil {
		return err
	}
	if command.ControllerRevision == 0 ||
		state.Revision < command.ControllerRevision ||
		state.ScheduledBackup == nil ||
		state.ScheduledBackup.Plan == nil {
		return fmt.Errorf("backup staged restore: stale Controller fence")
	}
	system := scheduledStateFromController(*state.ScheduledBackup)
	if command.Action == backupcontract.RestoreNodeActionPreflight {
		if system.Plan == nil ||
			system.ActiveBackup != nil ||
			system.ActiveRestore != nil ||
			!equalRestoreStore(system.Plan.Store, command.Store) {
			return fmt.Errorf("backup staged restore: preflight fence changed")
		}
		for _, node := range state.Nodes {
			if node.NodeID == s.node.NodeID() &&
				node.JoinState == controller.NodeJoinStateActive &&
				node.HasRole(controller.NodeRoleData) {
				return nil
			}
		}
		return fmt.Errorf("backup staged restore: preflight node is not active")
	}
	job := system.ActiveRestore
	if job == nil || system.Plan == nil ||
		job.ID != command.JobID ||
		job.BackupID != command.BackupID ||
		job.TargetActivation != command.TargetActivation ||
		job.MaxMessageID != command.MaxMessageID ||
		!equalRestoreStore(system.Plan.Store, command.Store) {
		return fmt.Errorf("backup staged restore: restore identity changed")
	}
	nodeID := s.node.NodeID()
	switch command.Action {
	case backupcontract.RestoreNodeActionPrepare:
		if job.Status != backupcontract.RestoreStatusMaintenance ||
			!job.MaintenanceEntered ||
			job.PreviousActivation != "" {
			return fmt.Errorf("backup staged restore: prepare phase changed")
		}
	case backupcontract.RestoreNodeActionStage:
		if job.Status != backupcontract.RestoreStatusMaintenance &&
			job.Status != backupcontract.RestoreStatusStaging {
			return fmt.Errorf("backup staged restore: stage phase changed")
		}
		slot, err := fencedRestoreSlot(*job, command)
		if err != nil ||
			slot.Status != backupcontract.RestoreSlotStatusStaging ||
			!currentRestoreReplica(state, command.HashSlot, nodeID) {
			return fmt.Errorf("backup staged restore: stage fence changed")
		}
	case backupcontract.RestoreNodeActionVerify:
		slot, err := fencedRestoreSlot(*job, command)
		if err != nil ||
			job.Status != backupcontract.RestoreStatusVerifying ||
			slot.Status != backupcontract.RestoreSlotStatusStaged ||
			!recordedRestoreReplica(slot, nodeID) ||
			!currentRestoreReplica(state, command.HashSlot, nodeID) {
			return fmt.Errorf("backup staged restore: verify fence changed")
		}
	case backupcontract.RestoreNodeActionSwitch:
		slot, err := fencedRestoreSlot(*job, command)
		if err != nil ||
			job.Status != backupcontract.RestoreStatusSwitching ||
			slot.Status != backupcontract.RestoreSlotStatusVerified ||
			!recordedRestoreReplica(slot, nodeID) ||
			!currentRestoreReplica(state, command.HashSlot, nodeID) {
			return fmt.Errorf("backup staged restore: switch fence changed")
		}
	case backupcontract.RestoreNodeActionActivate:
		if job.Status != backupcontract.RestoreStatusSwitching &&
			job.Status != backupcontract.RestoreStatusRollingBack {
			return fmt.Errorf("backup staged restore: activate phase changed")
		}
		if job.Status == backupcontract.RestoreStatusSwitching {
			for _, slot := range job.Slots {
				if slot.Status != backupcontract.RestoreSlotStatusVerified {
					return fmt.Errorf("backup staged restore: activate slots changed")
				}
			}
		}
	case backupcontract.RestoreNodeActionRollback:
		slot, err := fencedRestoreSlot(*job, command)
		if err != nil ||
			job.Status != backupcontract.RestoreStatusRollingBack ||
			!recordedRestoreReplica(slot, nodeID) {
			return fmt.Errorf("backup staged restore: rollback fence changed")
		}
	case backupcontract.RestoreNodeActionHealth:
		if job.Status != backupcontract.RestoreStatusSwitching &&
			job.Status != backupcontract.RestoreStatusRollingBack {
			return fmt.Errorf("backup staged restore: health phase changed")
		}
	case backupcontract.RestoreNodeActionCleanup:
		if job.Status != backupcontract.RestoreStatusFinalizing &&
			job.Status != backupcontract.RestoreStatusRollingBack {
			return fmt.Errorf("backup staged restore: cleanup phase changed")
		}
	case backupcontract.RestoreNodeActionResume:
		if job.Status != backupcontract.RestoreStatusFinalizing &&
			job.Status != backupcontract.RestoreStatusRollingBack {
			return fmt.Errorf("backup staged restore: resume phase changed")
		}
	default:
		return fmt.Errorf("backup staged restore: unsupported fenced action")
	}
	return nil
}

func fencedRestoreSlot(
	job backupcontract.RestoreJob,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreSlotProgress, error) {
	if int(command.HashSlot) >= len(job.Slots) {
		return backupcontract.RestoreSlotProgress{},
			fmt.Errorf("backup staged restore: Hash Slot fence is missing")
	}
	slot := job.Slots[command.HashSlot]
	if slot.HashSlot != command.HashSlot || slot.Attempt != command.Attempt {
		return backupcontract.RestoreSlotProgress{},
			fmt.Errorf("backup staged restore: attempt fence changed")
	}
	return slot, nil
}

func currentRestoreReplica(
	state controller.ClusterState,
	hashSlot uint16,
	nodeID uint64,
) bool {
	peers, err := restoreSlotPeers(state, hashSlot)
	if err != nil {
		return false
	}
	return slices.Contains(peers, nodeID)
}

func recordedRestoreReplica(
	slot backupcontract.RestoreSlotProgress,
	nodeID uint64,
) bool {
	return slices.Contains(slot.ReplicaNodeIDs, nodeID)
}

func equalRestoreStore(left, right backupcontract.StoreConfig) bool {
	return left.Kind == right.Kind &&
		left.Endpoint == right.Endpoint &&
		left.Region == right.Region &&
		left.Bucket == right.Bucket &&
		left.Prefix == right.Prefix &&
		left.PathStyle == right.PathStyle &&
		left.CredentialRevision == right.CredentialRevision &&
		bytes.Equal(left.CredentialCiphertext, right.CredentialCiphertext)
}

func (s *StagedRestoreNodeService) jobDir(
	command backupcontract.RestoreNodeCommand,
) string {
	return filepath.Join(s.root, command.JobID)
}

func (s *StagedRestoreNodeService) slotDir(
	command backupcontract.RestoreNodeCommand,
) string {
	return filepath.Join(
		s.jobDir(command), "slots",
		fmt.Sprintf("%03d", command.HashSlot),
	)
}

func (s *StagedRestoreNodeService) targetDir(
	command backupcontract.RestoreNodeCommand,
) string {
	return filepath.Join(
		s.slotDir(command), "attempt-"+strconv.FormatUint(uint64(command.Attempt), 10),
		"target",
	)
}

func (s *StagedRestoreNodeService) rollbackDir(
	command backupcontract.RestoreNodeCommand,
) string {
	return filepath.Join(s.slotDir(command), "rollback")
}

func safeRestoreIdentifier(value string) bool {
	if value == "" || len(value) > 128 || strings.Contains(value, "..") {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' || (char == '.' && index > 0) {
			continue
		}
		return false
	}
	return true
}

func decodeRestoreStreams(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	backupID string,
	manifest backupartifact.SlotManifest,
	directory string,
) error {
	var current *os.File
	var currentKind backupartifact.ChunkKind
	var currentStream uint32
	closeCurrent := func() error {
		if current == nil {
			return nil
		}
		syncErr := current.Sync()
		closeErr := current.Close()
		current = nil
		return errors.Join(syncErr, closeErr)
	}
	defer closeCurrent()
	for _, chunk := range manifest.Chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if current == nil || currentKind != chunk.Kind ||
			currentStream != chunk.Stream {
			if err := closeCurrent(); err != nil {
				return err
			}
			name := "metadata.bin"
			if chunk.Kind == backupartifact.ChunkKindMessages {
				name = fmt.Sprintf("messages-%06d.bin", chunk.Stream)
			}
			var err error
			current, err = os.OpenFile(
				filepath.Join(directory, name),
				os.O_CREATE|os.O_EXCL|os.O_WRONLY,
				restoreFileMode,
			)
			if err != nil {
				return err
			}
			currentKind = chunk.Kind
			currentStream = chunk.Stream
		}
		reader, object, err := store.Open(
			ctx, "backups/"+backupID+"/"+chunk.Key,
		)
		if err != nil {
			return err
		}
		if object.Bytes != chunk.Descriptor.StoredBytes {
			_ = reader.Close()
			return fmt.Errorf("backup staged restore: chunk size mismatch")
		}
		decodeErr := backupartifact.DecodeChunk(
			current, reader, chunk.Descriptor,
		)
		closeErr := reader.Close()
		if decodeErr != nil || closeErr != nil {
			return errors.Join(decodeErr, closeErr)
		}
	}
	return closeCurrent()
}

func writeRestoreFile(
	ctx context.Context,
	path string,
	reader io.ReadCloser,
) error {
	if reader == nil {
		return fmt.Errorf("backup staged restore: snapshot is unavailable")
	}
	file, err := os.OpenFile(
		path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, restoreFileMode,
	)
	if err != nil {
		_ = reader.Close()
		return err
	}
	buffer := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			_ = reader.Close()
			return err
		}
		read, readErr := reader.Read(buffer)
		if read > 0 {
			if err := writeRestoreBytes(file, buffer[:read]); err != nil {
				_ = file.Close()
				_ = reader.Close()
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = file.Close()
			_ = reader.Close()
			return readErr
		}
	}
	return errors.Join(file.Sync(), file.Close(), reader.Close())
}

func writeRestoreBytes(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(value) {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func openRestoreFiles(
	directory string,
) (
	*os.File,
	int64,
	[]clusterpkg.RestoreMessageStream,
	func() error,
	error,
) {
	if _, err := os.Stat(filepath.Join(directory, "READY")); err != nil {
		return nil, 0, nil, func() error { return nil }, err
	}
	metadata, err := os.Open(filepath.Join(directory, "metadata.bin"))
	if err != nil {
		return nil, 0, nil, func() error { return nil }, err
	}
	metadataInfo, err := metadata.Stat()
	if err != nil || metadataInfo.Size() <= 0 {
		_ = metadata.Close()
		if err == nil {
			err = fmt.Errorf("backup staged restore: empty metadata stream")
		}
		return nil, 0, nil, func() error { return nil }, err
	}
	paths, err := filepath.Glob(filepath.Join(directory, "messages-*.bin"))
	if err != nil {
		_ = metadata.Close()
		return nil, 0, nil, func() error { return nil }, err
	}
	sort.Strings(paths)
	files := make([]*os.File, 0, len(paths))
	streams := make([]clusterpkg.RestoreMessageStream, 0, len(paths))
	for _, path := range paths {
		file, openErr := os.Open(path)
		if openErr != nil {
			_ = metadata.Close()
			for _, opened := range files {
				_ = opened.Close()
			}
			return nil, 0, nil, func() error { return nil }, openErr
		}
		info, statErr := file.Stat()
		if statErr != nil || info.Size() <= 0 {
			_ = file.Close()
			_ = metadata.Close()
			for _, opened := range files {
				_ = opened.Close()
			}
			if statErr == nil {
				statErr = fmt.Errorf("backup staged restore: empty message stream")
			}
			return nil, 0, nil, func() error { return nil }, statErr
		}
		files = append(files, file)
		streams = append(streams, clusterpkg.RestoreMessageStream{
			Reader: file, Size: info.Size(),
		})
	}
	closeFiles := func() error {
		errs := make([]error, 0, len(files)+1)
		errs = append(errs, metadata.Close())
		for _, file := range files {
			errs = append(errs, file.Close())
		}
		return errors.Join(errs...)
	}
	return metadata, metadataInfo.Size(), streams, closeFiles, nil
}

func restoreMarkerExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func writeDurableRestoreMarker(path, content string) error {
	file, err := os.OpenFile(
		path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, restoreFileMode,
	)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	return syncRestoreDirectory(filepath.Dir(path))
}

func syncRestoreDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
