package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

const (
	checkpointRestoreReplicaChunkBytes = 3 << 20
	checkpointRestoreReplicaBatchSize  = 4096
	checkpointRestoreReplicaParallel   = 8
)

// CheckpointRestoreReplicaNode exposes restore-only target installation and
// the current Slot authority needed to fence a plaintext snapshot transfer.
type CheckpointRestoreReplicaNode interface {
	RestoreInstallNode
	NodeID() uint64
	RouteHashSlot(uint16) (clusterpkg.Route, error)
	VerifyLocalRestorePartition(
		context.Context,
		uint16,
		string,
		[]clusterpkg.RestoreVerifyBoundary,
	) error
	RestoreLiveMessageSnapshotEvidence(
		context.Context,
		uint16,
		[]clusterpkg.RestoreVerifyBoundary,
	) (clusterpkg.RestoreMessageSnapshotEvidence, error)
	DiscardLocalRestorePartition(
		context.Context,
		uint16,
		[]clusterpkg.RestoreVerifyBoundary,
	) error
}

// RemoteCheckpointRestoreReplicaClient sends one bounded transfer operation to
// an exact target replica.
type RemoteCheckpointRestoreReplicaClient interface {
	HandleCheckpointReplica(
		context.Context,
		uint64,
		backupcontract.CheckpointReplicaRequest,
	) (backupcontract.CheckpointReplicaResponse, error)
}

// LocalCheckpointRestoreReplicaInstaller installs the Leader-local copy
// without serializing its bytes through the node RPC codec.
type LocalCheckpointRestoreReplicaInstaller interface {
	InstallCheckpointRestoreSnapshot(
		context.Context,
		CheckpointRestoreInstallFence,
		CheckpointRestoreSnapshot,
	) (backupcontract.CheckpointReplicaResponse, error)
}

// CheckpointRestoreReplicaReceiverOptions configures one restore-only replica
// staging endpoint.
type CheckpointRestoreReplicaReceiverOptions struct {
	// Node owns the target databases and current routing fence.
	Node CheckpointRestoreReplicaNode
	// StagingDir is shared with DurableCheckpointRestoreTarget so a promoted
	// follower can resume from the same immutable semantic attempt.
	StagingDir string
	// StagingMaxBytes bounds one fully materialized target Slot snapshot.
	StagingMaxBytes uint64
	// StagingQuota is shared by every restore staging component on this node.
	StagingQuota *CheckpointRestoreStagingQuota
}

// CheckpointRestoreReplicaReceiver durably stages, installs, and verifies
// plaintext target snapshots. It has no repository or KMS dependency.
type CheckpointRestoreReplicaReceiver struct {
	// node owns the live target databases and current Slot routing fence.
	node CheckpointRestoreReplicaNode
	// stagingDir is the canonical node-local transfer root.
	stagingDir string
	// stagingMaxBytes is the shared hard node-local restore ceiling.
	stagingMaxBytes uint64
	// stagingQuota coordinates receiver bytes with source and target staging.
	stagingQuota *CheckpointRestoreStagingQuota
	// reservationsMu protects the diagnostic/restart reservation index.
	reservationsMu sync.Mutex
	// reservations records active receiver attempt capacities by directory.
	reservations map[string]uint64
}

// NewCheckpointRestoreReplicaReceiver creates a fail-closed replica receiver.
func NewCheckpointRestoreReplicaReceiver(
	options CheckpointRestoreReplicaReceiverOptions,
) (*CheckpointRestoreReplicaReceiver, error) {
	if options.Node == nil || options.Node.NodeID() == 0 ||
		strings.TrimSpace(options.StagingDir) == "" ||
		options.StagingMaxBytes == 0 {
		return nil, fmt.Errorf(
			"backup checkpoint restore replica receiver: invalid options",
		)
	}
	absolute, err := filepath.Abs(options.StagingDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	quota := options.StagingQuota
	if quota == nil {
		quota, err = NewCheckpointRestoreStagingQuota(
			resolved, options.StagingMaxBytes,
		)
		if err != nil {
			return nil, err
		}
	}
	if quota.maxBytes != options.StagingMaxBytes ||
		!quota.contains(resolved) {
		return nil, fmt.Errorf(
			"backup checkpoint restore replica receiver: staging quota mismatch",
		)
	}
	if err := quota.validate(); err != nil {
		return nil, err
	}
	receiver := &CheckpointRestoreReplicaReceiver{
		node: options.Node, stagingDir: resolved,
		stagingMaxBytes: options.StagingMaxBytes, stagingQuota: quota,
		reservations: make(map[string]uint64),
	}
	if err := receiver.rehydrateReplicaReservations(); err != nil {
		for attemptDir := range receiver.reservations {
			receiver.releaseReplicaReservation(attemptDir)
		}
		return nil, err
	}
	return receiver, nil
}

// rehydrateReplicaReservations rebuilds active attempt claims from durable
// transfer descriptors before the receiver accepts new work after restart.
func (r *CheckpointRestoreReplicaReceiver) rehydrateReplicaReservations() error {
	return filepath.WalkDir(
		r.stagingDir,
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Name() != "transfer.json" {
				return nil
			}
			attemptDir := filepath.Dir(path)
			if _, found, err := readCheckpointRestoreReceipt(attemptDir); err != nil || found {
				return err
			}
			transfer, found, err :=
				readCheckpointRestoreReplicaTransfer(attemptDir)
			if err != nil || !found {
				return errors.Join(err, backupartifact.ErrObjectCorrupt)
			}
			files, total, err := validateCheckpointReplicaFiles(
				transfer.Files, r.stagingMaxBytes,
			)
			if err != nil {
				return err
			}
			for _, descriptor := range files {
				if _, err := checkpointRestoreReplicaStagedFileSize(
					attemptDir, descriptor,
				); err != nil {
					return err
				}
			}
			capacity, err := checkpointRestoreReplicaAttemptCapacity(
				total, transfer.Evidence,
			)
			if err != nil {
				return err
			}
			if err := r.stagingQuota.reserveClaim(
				attemptDir, attemptDir, capacity,
			); err != nil {
				return err
			}
			r.trackReplicaReservation(attemptDir, capacity)
			return nil
		},
	)
}

func checkpointRestoreReplicaStagedFileSize(
	attemptDir string,
	descriptor backupcontract.CheckpointReplicaFile,
) (int64, error) {
	for _, path := range []string{
		checkpointRestoreReplicaPartPath(attemptDir, descriptor),
		checkpointRestoreReplicaFinalPath(attemptDir, descriptor),
	} {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 ||
			info.Size() > descriptor.Size {
			return 0, backupartifact.ErrObjectCorrupt
		}
		return info.Size(), nil
	}
	return 0, nil
}

// HandleCheckpointReplica applies one idempotent begin, chunk, commit, or
// status operation after validating the current Slot authority.
func (r *CheckpointRestoreReplicaReceiver) HandleCheckpointReplica(
	ctx context.Context,
	request backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	if r == nil {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf("backup checkpoint restore replica receiver: unavailable")
	}
	fence := checkpointRestoreFenceFromContract(request.Fence)
	if err := validateCheckpointRestoreFence(fence); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	attemptDir := checkpointRestoreAttemptDir(r.stagingDir, fence)
	unlock := r.stagingQuota.attemptLocks.lock(attemptDir)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if _, err := r.validateCurrentFence(fence); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	switch request.Action {
	case backupcontract.CheckpointReplicaBegin:
		return r.begin(ctx, fence, request)
	case backupcontract.CheckpointReplicaChunk:
		return r.chunk(ctx, fence, request)
	case backupcontract.CheckpointReplicaCommit:
		return r.commit(ctx, fence)
	case backupcontract.CheckpointReplicaStatus:
		return r.status(ctx, fence)
	default:
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf("backup checkpoint restore replica receiver: invalid action")
	}
}

// InstallCheckpointRestoreSnapshot installs and verifies the existing
// Leader-local snapshot without copying it through RPC.
func (r *CheckpointRestoreReplicaReceiver) InstallCheckpointRestoreSnapshot(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
	snapshot CheckpointRestoreSnapshot,
) (
	response backupcontract.CheckpointReplicaResponse,
	retErr error,
) {
	if r == nil {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf("backup checkpoint restore replica receiver: unavailable")
	}
	if err := validateCheckpointRestoreFence(fence); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	attemptDir := checkpointRestoreAttemptDir(r.stagingDir, fence)
	unlock := r.stagingQuota.attemptLocks.lock(attemptDir)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if _, err := r.validateCurrentFence(fence); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if err := validateCheckpointRestoreSnapshot(attemptDir, snapshot); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	_, snapshotBytes, err := checkpointRestoreReplicaFiles(snapshot)
	if err != nil || snapshotBytes > r.stagingMaxBytes {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf(
				"%w: checkpoint restore replica snapshot exceeds its bound",
				backupartifact.ErrInvalidObject,
			)
	}
	if receipt, found, err := readCheckpointRestoreReceipt(attemptDir); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	} else if found {
		if !checkpointRestoreFenceSameIdentity(receipt.Fence, fence) ||
			receipt.Snapshot.Metadata.SHA256 != snapshot.Metadata.SHA256 {
			return backupcontract.CheckpointReplicaResponse{},
				fmt.Errorf(
					"%w: checkpoint restore replica receipt conflicts",
					backupartifact.ErrObjectCorrupt,
				)
		}
		receipt.Fence = fence
		receipt.Resume.Replicas.ReplicaCount = fence.ReplicaCount
		receipt.Resume.Replicas.ConvergedReplicas = 1
		if err := r.verifyInstalledSnapshot(
			ctx, fence, attemptDir, receipt.Snapshot,
			receipt.Resume.Replicas.MetadataSHA256,
		); err != nil {
			return backupcontract.CheckpointReplicaResponse{},
				r.handleExistingReceiptVerificationError(
					ctx, fence, attemptDir, err,
				)
		}
		if _, err := r.validateCurrentFence(fence); err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
		return checkpointRestoreCompletedResponse(
			receipt.Snapshot,
			receipt.Resume.Replicas.MetadataSHA256,
		), nil
	}
	capacity, err := checkpointRestoreReplicaAttemptCapacity(
		snapshotBytes, snapshot.Evidence,
	)
	if err != nil || capacity < snapshotBytes {
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(err, backupartifact.ErrInvalidObject)
	}
	if err := r.stagingQuota.reserveClaim(
		attemptDir, attemptDir, capacity,
	); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	r.trackReplicaReservation(attemptDir, capacity)
	defer func() {
		settleErr := r.settleReplicaReservation(attemptDir)
		if settleErr == nil {
			return
		}
		cleanupErr := errors.Join(
			r.invalidateInstalledSnapshot(
				context.WithoutCancel(ctx),
				fence.HashSlot, attemptDir,
			),
			os.RemoveAll(attemptDir),
		)
		refreshErr := r.stagingQuota.refresh()
		response = backupcontract.CheckpointReplicaResponse{}
		retErr = errors.Join(retErr, settleErr, cleanupErr, refreshErr)
	}()
	if err := r.installSnapshot(ctx, fence, snapshot); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	metadataSHA256, err := r.node.RestoreHashSlotMetadataDigest(
		ctx, fence.HashSlot,
	)
	if err != nil || !validLowerSHA256(metadataSHA256) {
		return backupcontract.CheckpointReplicaResponse{}, errors.Join(
			err, backupartifact.ErrObjectCorrupt,
			r.invalidateInstalledSnapshot(
				context.WithoutCancel(ctx), fence.HashSlot, attemptDir,
			),
		)
	}
	if err := r.verifyInstalledSnapshot(
		ctx, fence, attemptDir, snapshot, metadataSHA256,
	); err != nil {
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(
				err,
				r.invalidateInstalledSnapshot(
					context.WithoutCancel(ctx),
					fence.HashSlot, attemptDir,
				),
			)
	}
	if _, err := r.validateCurrentFence(fence); err != nil {
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(
				err,
				r.invalidateInstalledSnapshot(
					context.WithoutCancel(ctx),
					fence.HashSlot, attemptDir,
				),
			)
	}
	if err := r.writeReplicaReceipt(
		attemptDir, fence, snapshot, metadataSHA256,
	); err != nil {
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(
				err,
				r.invalidateInstalledSnapshot(
					context.WithoutCancel(ctx),
					fence.HashSlot, attemptDir,
				),
			)
	}
	return checkpointRestoreCompletedResponse(snapshot, metadataSHA256), nil
}

type checkpointRestoreReplicaTransfer struct {
	Fence                 CheckpointRestoreInstallFence          `json:"fence"`
	Files                 []backupcontract.CheckpointReplicaFile `json:"files"`
	Evidence              backupartifact.RestoreEvidence         `json:"evidence"`
	FinalMessageCount     uint64                                 `json:"final_message_count"`
	FinalMaxMessageID     uint64                                 `json:"final_max_message_id"`
	DownloadedBytes       uint64                                 `json:"downloaded_bytes"`
	InstalledAtUnixMillis int64                                  `json:"installed_at_unix_millis"`
}

func (r *CheckpointRestoreReplicaReceiver) begin(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
	request backupcontract.CheckpointReplicaRequest,
) (
	response backupcontract.CheckpointReplicaResponse,
	retErr error,
) {
	files, total, err := validateCheckpointReplicaFiles(
		request.Files, r.stagingMaxBytes,
	)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if !validCheckpointRestoreEvidence(request.Evidence) ||
		request.InstalledAtUnixMillis <= 0 {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf(
				"%w: checkpoint restore replica evidence is invalid",
				backupartifact.ErrInvalidObject,
			)
	}
	attemptDir := checkpointRestoreAttemptDir(r.stagingDir, fence)
	capacity, err := checkpointRestoreReplicaAttemptCapacity(
		total, request.Evidence,
	)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if receipt, found, err := readCheckpointRestoreReceipt(attemptDir); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	} else if found {
		if !checkpointRestoreFenceSameIdentity(receipt.Fence, fence) ||
			!checkpointReplicaFilesMatchSnapshot(files, receipt.Snapshot) {
			return backupcontract.CheckpointReplicaResponse{},
				fmt.Errorf(
					"%w: checkpoint restore replica receipt conflicts",
					backupartifact.ErrObjectCorrupt,
				)
		}
		receipt.Fence = fence
		receipt.Resume.Replicas.ReplicaCount = fence.ReplicaCount
		receipt.Resume.Replicas.ConvergedReplicas = 1
		if err := r.verifyInstalledSnapshot(
			ctx, fence, attemptDir, receipt.Snapshot,
			receipt.Resume.Replicas.MetadataSHA256,
		); err != nil {
			return backupcontract.CheckpointReplicaResponse{},
				r.handleExistingReceiptVerificationError(
					ctx, fence, attemptDir, err,
				)
		}
		if _, err := r.validateCurrentFence(fence); err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
		return checkpointRestoreCompletedResponse(
			receipt.Snapshot,
			receipt.Resume.Replicas.MetadataSHA256,
		), nil
	}
	next := checkpointRestoreReplicaTransfer{
		Fence: fence, Files: files, Evidence: request.Evidence,
		FinalMessageCount:     request.FinalMessageCount,
		FinalMaxMessageID:     request.FinalMaxMessageID,
		DownloadedBytes:       request.DownloadedBytes,
		InstalledAtUnixMillis: request.InstalledAtUnixMillis,
	}
	current, found, err := readCheckpointRestoreReplicaTransfer(attemptDir)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if err := r.stagingQuota.reserveClaim(
		attemptDir, attemptDir, capacity,
	); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	r.trackReplicaReservation(attemptDir, capacity)
	setupFailed := true
	defer func() {
		if !setupFailed {
			return
		}
		removeErr := os.RemoveAll(attemptDir)
		r.untrackReplicaReservation(attemptDir)
		settleErr := r.stagingQuota.settleClaim(attemptDir)
		response = backupcontract.CheckpointReplicaResponse{}
		retErr = errors.Join(retErr, removeErr, settleErr)
	}()
	if !found || !sameCheckpointRestoreReplicaTransfer(current, next) {
		if err := os.RemoveAll(attemptDir); err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
		if err := os.MkdirAll(attemptDir, 0o750); err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
	} else {
		next.Fence = fence
	}
	for _, file := range files {
		partPath := checkpointRestoreReplicaPartPath(attemptDir, file)
		finalPath := checkpointRestoreReplicaFinalPath(attemptDir, file)
		if _, err := os.Lstat(partPath); errors.Is(err, os.ErrNotExist) {
			if _, finalErr := os.Lstat(finalPath); finalErr == nil {
				if err := os.Rename(finalPath, partPath); err != nil {
					return backupcontract.CheckpointReplicaResponse{}, err
				}
			} else if !errors.Is(finalErr, os.ErrNotExist) {
				return backupcontract.CheckpointReplicaResponse{}, finalErr
			}
		} else if err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
		handle, err := os.OpenFile(
			partPath, os.O_CREATE|os.O_WRONLY, 0o600,
		)
		if err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
		info, statErr := handle.Stat()
		closeErr := handle.Close()
		if statErr != nil || closeErr != nil {
			return backupcontract.CheckpointReplicaResponse{},
				errors.Join(statErr, closeErr)
		}
		if !info.Mode().IsRegular() || info.Size() > file.Size {
			return backupcontract.CheckpointReplicaResponse{},
				fmt.Errorf(
					"%w: checkpoint restore replica partial file is invalid",
					backupartifact.ErrObjectCorrupt,
				)
		}
	}
	if err := writeCheckpointRestoreReplicaTransfer(attemptDir, next); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	setupFailed = false
	return backupcontract.CheckpointReplicaResponse{}, nil
}

// checkpointRestoreReplicaAttemptCapacity returns a conservative peak claim
// covering the received files, finalized copies, indexes, and durable metadata.
func checkpointRestoreReplicaAttemptCapacity(
	snapshotBytes uint64,
	evidence backupartifact.RestoreEvidence,
) (uint64, error) {
	const fixedOverhead = uint64(4 << 20)
	if evidence.ChannelBoundaryCount >
		(^uint64(0)-fixedOverhead-snapshotBytes)/256 {
		return 0, backupartifact.ErrInvalidObject
	}
	overhead := fixedOverhead + evidence.ChannelBoundaryCount*256
	if snapshotBytes > ^uint64(0)-snapshotBytes ||
		snapshotBytes*2 > ^uint64(0)-overhead {
		return 0, backupartifact.ErrInvalidObject
	}
	return snapshotBytes*2 + overhead, nil
}

func (r *CheckpointRestoreReplicaReceiver) chunk(
	_ context.Context,
	fence CheckpointRestoreInstallFence,
	request backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	attemptDir := checkpointRestoreAttemptDir(r.stagingDir, fence)
	transfer, found, err := readCheckpointRestoreReplicaTransfer(attemptDir)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if !found || transfer.Fence != fence ||
		!checkpointReplicaFileInSet(request.File, transfer.Files) {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf(
				"%w: checkpoint restore replica transfer is not active",
				backupartifact.ErrObjectCorrupt,
			)
	}
	path := checkpointRestoreReplicaPartPath(attemptDir, request.File)
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		info.Size() > request.File.Size {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf(
				"%w: checkpoint restore replica partial file is invalid",
				backupartifact.ErrObjectCorrupt,
			)
	}
	if request.Offset != info.Size() {
		return backupcontract.CheckpointReplicaResponse{
			AcceptedOffset: info.Size(),
		}, nil
	}
	if _, err := file.Seek(request.Offset, io.SeekStart); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	written, err := file.Write(request.Data)
	if err != nil || written != len(request.Data) {
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(err, io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	return backupcontract.CheckpointReplicaResponse{
		AcceptedOffset: request.Offset + int64(written),
	}, nil
}

func (r *CheckpointRestoreReplicaReceiver) releaseReplicaReservation(
	attemptDir string,
) {
	r.untrackReplicaReservation(attemptDir)
	_ = r.stagingQuota.settleClaim(attemptDir)
}

func (r *CheckpointRestoreReplicaReceiver) settleReplicaReservation(
	attemptDir string,
) error {
	r.untrackReplicaReservation(attemptDir)
	return r.stagingQuota.settleClaim(attemptDir)
}

func (r *CheckpointRestoreReplicaReceiver) trackReplicaReservation(
	attemptDir string,
	capacity uint64,
) {
	r.reservationsMu.Lock()
	r.reservations[attemptDir] = capacity
	r.reservationsMu.Unlock()
}

func (r *CheckpointRestoreReplicaReceiver) untrackReplicaReservation(
	attemptDir string,
) {
	r.reservationsMu.Lock()
	delete(r.reservations, attemptDir)
	r.reservationsMu.Unlock()
}

func (r *CheckpointRestoreReplicaReceiver) commit(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
) (backupcontract.CheckpointReplicaResponse, error) {
	attemptDir := checkpointRestoreAttemptDir(r.stagingDir, fence)
	if receipt, found, err := readCheckpointRestoreReceipt(attemptDir); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	} else if found {
		if !checkpointRestoreFenceSameIdentity(receipt.Fence, fence) {
			return backupcontract.CheckpointReplicaResponse{},
				fmt.Errorf(
					"%w: checkpoint restore replica receipt conflicts",
					backupartifact.ErrObjectCorrupt,
				)
		}
		if err := r.verifyInstalledSnapshot(
			ctx, fence, attemptDir, receipt.Snapshot,
			receipt.Resume.Replicas.MetadataSHA256,
		); err != nil {
			return backupcontract.CheckpointReplicaResponse{},
				r.handleExistingReceiptVerificationError(
					ctx, fence, attemptDir, err,
				)
		}
		if _, err := r.validateCurrentFence(fence); err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
		return checkpointRestoreCompletedResponse(
			receipt.Snapshot,
			receipt.Resume.Replicas.MetadataSHA256,
		), nil
	}
	transfer, found, err := readCheckpointRestoreReplicaTransfer(attemptDir)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if !found || transfer.Fence != fence {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf(
				"%w: checkpoint restore replica transfer is not active",
				backupartifact.ErrObjectCorrupt,
			)
	}
	snapshot := CheckpointRestoreSnapshot{
		Evidence:              transfer.Evidence,
		FinalMessageCount:     transfer.FinalMessageCount,
		FinalMaxMessageID:     transfer.FinalMaxMessageID,
		DownloadedBytes:       transfer.DownloadedBytes,
		InstalledAtUnixMillis: transfer.InstalledAtUnixMillis,
	}
	for _, descriptor := range transfer.Files {
		partPath := checkpointRestoreReplicaPartPath(attemptDir, descriptor)
		if err := validateCheckpointRestoreFile(
			partPath, descriptor.Size, descriptor.SHA256,
		); err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
		finalPath := checkpointRestoreReplicaFinalPath(attemptDir, descriptor)
		if err := os.Rename(partPath, finalPath); err != nil {
			return backupcontract.CheckpointReplicaResponse{}, err
		}
		file := CheckpointRestoreSnapshotFile{
			Path: finalPath, Size: descriptor.Size, SHA256: descriptor.SHA256,
		}
		switch descriptor.Kind {
		case backupcontract.CheckpointReplicaMetadata:
			snapshot.Metadata = file
		case backupcontract.CheckpointReplicaMessages:
			snapshot.Messages = append(snapshot.Messages, file)
		case backupcontract.CheckpointReplicaErasures:
			snapshot.Erasures = file
		}
	}
	if err := validateCheckpointRestoreSnapshot(attemptDir, snapshot); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if err := r.installSnapshot(ctx, fence, snapshot); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	metadataSHA256, err := r.node.RestoreHashSlotMetadataDigest(
		ctx, fence.HashSlot,
	)
	if err != nil || !validLowerSHA256(metadataSHA256) {
		return backupcontract.CheckpointReplicaResponse{}, errors.Join(
			err, backupartifact.ErrObjectCorrupt,
			r.invalidateInstalledSnapshot(
				context.WithoutCancel(ctx), fence.HashSlot, attemptDir,
			),
		)
	}
	if err := r.verifyInstalledSnapshot(
		ctx, fence, attemptDir, snapshot, metadataSHA256,
	); err != nil {
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(
				err,
				r.invalidateInstalledSnapshot(
					context.WithoutCancel(ctx),
					fence.HashSlot, attemptDir,
				),
			)
	}
	if _, err := r.validateCurrentFence(fence); err != nil {
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(
				err,
				r.invalidateInstalledSnapshot(
					context.WithoutCancel(ctx),
					fence.HashSlot, attemptDir,
				),
			)
	}
	if err := r.writeReplicaReceipt(
		attemptDir, fence, snapshot, metadataSHA256,
	); err != nil {
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(
				err,
				r.invalidateInstalledSnapshot(
					context.WithoutCancel(ctx),
					fence.HashSlot, attemptDir,
				),
			)
	}
	if err := r.settleReplicaReservation(attemptDir); err != nil {
		cleanupErr := errors.Join(
			r.invalidateInstalledSnapshot(
				context.WithoutCancel(ctx),
				fence.HashSlot, attemptDir,
			),
			os.RemoveAll(attemptDir),
		)
		refreshErr := r.stagingQuota.refresh()
		return backupcontract.CheckpointReplicaResponse{},
			errors.Join(err, cleanupErr, refreshErr)
	}
	return checkpointRestoreCompletedResponse(snapshot, metadataSHA256), nil
}

func (r *CheckpointRestoreReplicaReceiver) status(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
) (backupcontract.CheckpointReplicaResponse, error) {
	attemptDir := checkpointRestoreAttemptDir(r.stagingDir, fence)
	receipt, found, err := readCheckpointRestoreReceipt(attemptDir)
	if err != nil || !found {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if !checkpointRestoreFenceSameIdentity(receipt.Fence, fence) {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf(
				"%w: checkpoint restore replica receipt conflicts",
				backupartifact.ErrObjectCorrupt,
			)
	}
	if err := r.verifyInstalledSnapshot(
		ctx, fence, attemptDir, receipt.Snapshot,
		receipt.Resume.Replicas.MetadataSHA256,
	); err != nil {
		return backupcontract.CheckpointReplicaResponse{},
			r.handleExistingReceiptVerificationError(
				ctx, fence, attemptDir, err,
			)
	}
	if _, err := r.validateCurrentFence(fence); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	return checkpointRestoreCompletedResponse(
		receipt.Snapshot,
		receipt.Resume.Replicas.MetadataSHA256,
	), nil
}

func (r *CheckpointRestoreReplicaReceiver) validateCurrentFence(
	fence CheckpointRestoreInstallFence,
) (clusterpkg.Route, error) {
	route, err := r.node.RouteHashSlot(fence.HashSlot)
	if err != nil {
		return clusterpkg.Route{}, err
	}
	if route.HashSlot != fence.HashSlot ||
		route.SlotID != fence.TargetSlotID ||
		route.Leader != fence.LeaderNodeID ||
		route.LeaderTerm != fence.LeaderTerm ||
		route.ConfigEpoch != fence.ConfigEpoch ||
		len(route.Peers) != int(fence.ReplicaCount) ||
		!containsRestoreNode(route.Peers, r.node.NodeID()) ||
		!containsRestoreNode(route.Peers, fence.LeaderNodeID) ||
		restoreNodeIDsContainDuplicate(route.Peers) {
		return clusterpkg.Route{},
			fmt.Errorf(
				"%w: checkpoint restore replica Slot fence is stale",
				backupartifact.ErrObjectCorrupt,
			)
	}
	return route, nil
}

func (r *CheckpointRestoreReplicaReceiver) installSnapshot(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
	snapshot CheckpointRestoreSnapshot,
) (retErr error) {
	if err := validateCheckpointRestoreSnapshot(
		checkpointRestoreAttemptDir(r.stagingDir, fence), snapshot,
	); err != nil {
		return err
	}
	indexPath := filepath.Join(
		checkpointRestoreAttemptDir(r.stagingDir, fence),
		"replica-boundaries",
	)
	if err := os.RemoveAll(indexPath); err != nil {
		return err
	}
	index, err := openCheckpointRestoreEvidenceIndex(indexPath)
	if err != nil {
		return err
	}
	indexOpen := true
	defer func() {
		if indexOpen {
			_ = index.Close()
		}
	}()
	for _, descriptor := range snapshot.Messages {
		file, err := os.Open(descriptor.Path)
		if err != nil {
			return err
		}
		stats, replayErr := messagedb.ReplayBackupSnapshotReader(
			ctx, file, descriptor.Size,
			func(boundary messagedb.BackupSnapshotBoundary) error {
				return index.ObserveCursor(backupartifact.ChannelBoundary{
					ChannelID:      boundary.ChannelID,
					ChannelType:    boundary.ChannelType,
					Epoch:          boundary.Epoch,
					LogStartOffset: boundary.LogStartOffset,
					HW:             boundary.HW,
				})
			},
			func(messagedb.BackupSnapshotRecord) error { return nil },
		)
		closeErr := file.Close()
		if replayErr != nil || closeErr != nil {
			return errors.Join(replayErr, closeErr)
		}
		if stats.HashSlot != fence.HashSlot {
			return fmt.Errorf(
				"%w: checkpoint restore message snapshot is invalid",
				backupartifact.ErrObjectCorrupt,
			)
		}
	}
	if err := r.applyReplicaErasures(
		ctx, fence.HashSlot, snapshot.Erasures, index, false,
	); err != nil {
		return err
	}
	if index.ChannelCount() != snapshot.Evidence.ChannelBoundaryCount {
		return fmt.Errorf(
			"%w: checkpoint restore Channel evidence mismatch",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if _, err := r.validateCurrentFence(fence); err != nil {
		return err
	}
	liveStarted := true
	defer func() {
		if retErr == nil || !liveStarted {
			return
		}
		cleanupCtx := context.WithoutCancel(ctx)
		cleanupErr := r.discardPartialInstall(
			cleanupCtx, fence.HashSlot, index,
		)
		retErr = errors.Join(retErr, cleanupErr)
	}()
	metadata, err := os.Open(snapshot.Metadata.Path)
	if err != nil {
		return err
	}
	_, installErr := r.node.InstallRestoreHashSlotMetadata(
		ctx, fence.HashSlot, metadata, snapshot.Metadata.Size,
		fence.InvalidateTokens,
	)
	closeErr := metadata.Close()
	if installErr != nil || closeErr != nil {
		return errors.Join(installErr, closeErr)
	}
	for _, descriptor := range snapshot.Messages {
		file, err := os.Open(descriptor.Path)
		if err != nil {
			return err
		}
		_, installErr := r.node.InstallRestoreMessageStream(
			ctx, file, descriptor.Size,
		)
		closeErr := file.Close()
		if installErr != nil || closeErr != nil {
			return errors.Join(installErr, closeErr)
		}
	}
	if err := r.applyReplicaErasures(
		ctx, fence.HashSlot, snapshot.Erasures, nil, true,
	); err != nil {
		return err
	}
	if err := r.visitReplicaBoundaries(
		index,
		func(boundaries []clusterpkg.RestoreVerifyBoundary) error {
			return r.node.InstallRestoreChannelRuntimeMeta(
				ctx, fence.HashSlot, boundaries,
			)
		},
	); err != nil {
		return err
	}
	if _, err := r.validateCurrentFence(fence); err != nil {
		return err
	}
	digest, err := r.node.RestoreHashSlotMetadataDigest(
		ctx, fence.HashSlot,
	)
	if err != nil {
		return err
	}
	verifiedDigest := false
	if err := r.visitReplicaBoundaries(
		index,
		func(boundaries []clusterpkg.RestoreVerifyBoundary) error {
			metadataDigest := ""
			if !verifiedDigest {
				metadataDigest = digest
				verifiedDigest = true
			}
			return r.node.VerifyLocalRestorePartition(
				ctx, fence.HashSlot, metadataDigest, boundaries,
			)
		},
	); err != nil {
		return err
	}
	if !verifiedDigest {
		if err := r.node.VerifyLocalRestorePartition(
			ctx, fence.HashSlot, digest, nil,
		); err != nil {
			return err
		}
	}
	if err := index.Close(); err != nil {
		return err
	}
	indexOpen = false
	liveStarted = false
	return nil
}

func (r *CheckpointRestoreReplicaReceiver) verifyInstalledSnapshot(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
	attemptDir string,
	snapshot CheckpointRestoreSnapshot,
	expectedMetadataSHA256 string,
) error {
	if err := validateCheckpointRestoreSnapshot(attemptDir, snapshot); err != nil {
		return err
	}
	digest, err := r.node.RestoreHashSlotMetadataDigest(
		ctx, fence.HashSlot,
	)
	if err != nil {
		return err
	}
	if !validLowerSHA256(expectedMetadataSHA256) ||
		digest != expectedMetadataSHA256 {
		return fmt.Errorf(
			"%w: checkpoint restore metadata digest mismatch",
			backupartifact.ErrObjectCorrupt,
		)
	}
	index, err := openCheckpointRestoreEvidenceIndexReadOnly(
		filepath.Join(attemptDir, "replica-boundaries"),
	)
	if err != nil {
		return err
	}
	defer index.Close()
	var boundaryCount uint64
	verifiedDigest := false
	err = r.visitReplicaBoundaries(
		index,
		func(boundaries []clusterpkg.RestoreVerifyBoundary) error {
			if uint64(len(boundaries)) > ^uint64(0)-boundaryCount {
				return backupartifact.ErrInvalidObject
			}
			boundaryCount += uint64(len(boundaries))
			metadataDigest := ""
			if !verifiedDigest {
				metadataDigest = digest
				verifiedDigest = true
			}
			return r.node.VerifyLocalRestorePartition(
				ctx, fence.HashSlot, metadataDigest, boundaries,
			)
		},
	)
	if err != nil {
		return err
	}
	if boundaryCount != snapshot.Evidence.ChannelBoundaryCount {
		return fmt.Errorf(
			"%w: checkpoint restore Channel evidence mismatch",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if err := r.verifyLiveMessageSnapshots(
		ctx, fence.HashSlot, index, snapshot,
	); err != nil {
		return err
	}
	if !verifiedDigest {
		return r.node.VerifyLocalRestorePartition(
			ctx, fence.HashSlot, digest, nil,
		)
	}
	return nil
}

func (r *CheckpointRestoreReplicaReceiver) applyReplicaErasures(
	ctx context.Context,
	hashSlot uint16,
	descriptor CheckpointRestoreSnapshotFile,
	index *checkpointRestoreEvidenceIndex,
	apply bool,
) error {
	file, err := os.Open(descriptor.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, descriptor.Size))
	decoder.DisallowUnknownFields()
	batch := make([]clusterpkg.RestorePermanentErasure, 0,
		checkpointRestoreReplicaBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if !apply {
			batch = batch[:0]
			return nil
		}
		if err := r.node.ApplyRestorePermanentErasures(
			ctx, hashSlot, batch,
		); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for {
		var item channelstore.RestorePermanentErasure
		err := decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || item.ID.ID == "" || item.ID.Type == 0 ||
			item.Epoch == 0 || item.ThroughSeq == 0 {
			return fmt.Errorf(
				"%w: checkpoint restore erasure snapshot is invalid",
				backupartifact.ErrObjectCorrupt,
			)
		}
		if !apply {
			if index == nil {
				return backupartifact.ErrInvalidObject
			}
			if err := index.applyErasure(backupartifact.ChannelBoundary{
				ChannelID: item.ID.ID, ChannelType: item.ID.Type,
				Epoch: item.Epoch, LogStartOffset: item.ThroughSeq,
				HW: item.ThroughSeq,
			}); err != nil {
				return err
			}
		}
		batch = append(batch, clusterpkg.RestorePermanentErasure{
			ChannelID: item.ID.ID, ChannelType: item.ID.Type,
			Epoch: item.Epoch, ThroughSeq: item.ThroughSeq,
		})
		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func (r *CheckpointRestoreReplicaReceiver) discardPartialInstall(
	ctx context.Context,
	hashSlot uint16,
	index *checkpointRestoreEvidenceIndex,
) error {
	called := false
	err := r.visitReplicaBoundaries(
		index,
		func(boundaries []clusterpkg.RestoreVerifyBoundary) error {
			called = true
			return r.node.DiscardLocalRestorePartition(
				ctx, hashSlot, boundaries,
			)
		},
	)
	if err != nil {
		return err
	}
	if !called {
		return r.node.DiscardLocalRestorePartition(ctx, hashSlot, nil)
	}
	return nil
}

func (r *CheckpointRestoreReplicaReceiver) invalidateInstalledSnapshot(
	ctx context.Context,
	hashSlot uint16,
	attemptDir string,
) error {
	index, err := openCheckpointRestoreEvidenceIndexReadOnly(
		filepath.Join(attemptDir, "replica-boundaries"),
	)
	if err != nil {
		discardErr := r.node.DiscardLocalRestorePartition(
			ctx, hashSlot, nil,
		)
		removeErr := r.stagingQuota.removeTrackedPath(
			filepath.Join(attemptDir, "receipt.json"), nil,
		)
		return errors.Join(err, discardErr, removeErr)
	}
	discardErr := r.discardPartialInstall(ctx, hashSlot, index)
	closeErr := index.Close()
	removeErr := r.stagingQuota.removeTrackedPath(
		filepath.Join(attemptDir, "receipt.json"), nil,
	)
	return errors.Join(discardErr, closeErr, removeErr)
}

// handleExistingReceiptVerificationError preserves durable evidence when the
// caller canceled a read-only revalidation. Only completed verification that
// proves corruption is allowed to invalidate an installed replica.
func (r *CheckpointRestoreReplicaReceiver) handleExistingReceiptVerificationError(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
	attemptDir string,
	verifyErr error,
) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(verifyErr, ctxErr)
	}
	return errors.Join(
		verifyErr,
		r.invalidateInstalledSnapshot(
			context.WithoutCancel(ctx), fence.HashSlot, attemptDir,
		),
	)
}

func (r *CheckpointRestoreReplicaReceiver) verifyLiveMessageSnapshots(
	ctx context.Context,
	hashSlot uint16,
	index *checkpointRestoreEvidenceIndex,
	snapshot CheckpointRestoreSnapshot,
) error {
	batch := make([]clusterpkg.RestoreVerifyBoundary, 0,
		checkpointRestoreExportChannels)
	fileIndex := 0
	var messageCount uint64
	var maxMessageID uint64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if fileIndex >= len(snapshot.Messages) {
			return backupartifact.ErrObjectCorrupt
		}
		evidence, err := r.node.RestoreLiveMessageSnapshotEvidence(
			ctx, hashSlot, batch,
		)
		if err != nil {
			return err
		}
		expected := snapshot.Messages[fileIndex]
		if evidence.Size != expected.Size ||
			evidence.SHA256 != expected.SHA256 ||
			evidence.ChannelCount != uint64(len(batch)) {
			return fmt.Errorf(
				"%w: checkpoint restore live message content mismatch",
				backupartifact.ErrObjectCorrupt,
			)
		}
		if ^uint64(0)-messageCount < evidence.MessageCount {
			return backupartifact.ErrInvalidObject
		}
		messageCount += evidence.MessageCount
		if evidence.MaxMessageID > maxMessageID {
			maxMessageID = evidence.MaxMessageID
		}
		fileIndex++
		batch = batch[:0]
		return nil
	}
	err := index.visitBoundaries(
		func(boundary backupartifact.ChannelBoundary, erased uint64) error {
			batch = append(batch, clusterpkg.RestoreVerifyBoundary{
				ChannelID:                boundary.ChannelID,
				ChannelType:              boundary.ChannelType,
				Epoch:                    boundary.Epoch,
				LogStartOffset:           boundary.LogStartOffset,
				HW:                       boundary.HW,
				PermanentEraseThroughSeq: erased,
			})
			if len(batch) == cap(batch) {
				return flush()
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if fileIndex != len(snapshot.Messages) ||
		messageCount != snapshot.FinalMessageCount ||
		maxMessageID != snapshot.FinalMaxMessageID {
		return fmt.Errorf(
			"%w: checkpoint restore live message evidence mismatch",
			backupartifact.ErrObjectCorrupt,
		)
	}
	return nil
}

func (r *CheckpointRestoreReplicaReceiver) visitReplicaBoundaries(
	index *checkpointRestoreEvidenceIndex,
	visit func([]clusterpkg.RestoreVerifyBoundary) error,
) error {
	batch := make([]clusterpkg.RestoreVerifyBoundary, 0,
		checkpointRestoreReplicaBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := visit(batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	err := index.visitBoundaries(
		func(boundary backupartifact.ChannelBoundary, erased uint64) error {
			batch = append(batch, clusterpkg.RestoreVerifyBoundary{
				ChannelID:                boundary.ChannelID,
				ChannelType:              boundary.ChannelType,
				Epoch:                    boundary.Epoch,
				LogStartOffset:           boundary.LogStartOffset,
				HW:                       boundary.HW,
				PermanentEraseThroughSeq: erased,
			})
			if len(batch) == cap(batch) {
				return flush()
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	return flush()
}

func (r *CheckpointRestoreReplicaReceiver) writeReplicaReceipt(
	attemptDir string,
	fence CheckpointRestoreInstallFence,
	snapshot CheckpointRestoreSnapshot,
	metadataSHA256 string,
) error {
	if !validLowerSHA256(metadataSHA256) {
		return backupartifact.ErrObjectCorrupt
	}
	return writeCheckpointRestoreReceipt(
		filepath.Join(attemptDir, "receipt.json"),
		checkpointRestoreReceipt{
			Fence: fence,
			Resume: CheckpointRestoreResume{
				Evidence:              snapshot.Evidence,
				DownloadedBytes:       snapshot.DownloadedBytes,
				InstalledAtUnixMillis: snapshot.InstalledAtUnixMillis,
				Replicas: CheckpointRestoreReplicaResult{
					ReplicaCount:      fence.ReplicaCount,
					ConvergedReplicas: 1,
					MetadataSHA256:    metadataSHA256,
				},
			},
			Snapshot: snapshot,
		},
	)
}

// CheckpointRestoreReplicaDistributorOptions configures replica-aware
// final-snapshot convergence.
type CheckpointRestoreReplicaDistributorOptions struct {
	// Node supplies the exact current target replica set.
	Node interface {
		NodeID() uint64
		RouteHashSlot(uint16) (clusterpkg.Route, error)
	}
	// Local installs the current node without RPC serialization.
	Local LocalCheckpointRestoreReplicaInstaller
	// Remote streams bounded chunks to other desired replicas.
	Remote RemoteCheckpointRestoreReplicaClient
	// ChunkBytes bounds each plaintext RPC payload and defaults to 3 MiB.
	ChunkBytes int
}

// CheckpointRestoreReplicaDistributor converges one validated target snapshot
// to current replicas with bounded parallelism.
type CheckpointRestoreReplicaDistributor struct {
	node interface {
		NodeID() uint64
		RouteHashSlot(uint16) (clusterpkg.Route, error)
	}
	local      LocalCheckpointRestoreReplicaInstaller
	remote     RemoteCheckpointRestoreReplicaClient
	chunkBytes int
}

// NewCheckpointRestoreReplicaDistributor creates a current-placement
// distributor.
func NewCheckpointRestoreReplicaDistributor(
	options CheckpointRestoreReplicaDistributorOptions,
) (*CheckpointRestoreReplicaDistributor, error) {
	if options.Node == nil || options.Node.NodeID() == 0 ||
		options.Local == nil || options.Remote == nil {
		return nil, fmt.Errorf(
			"backup checkpoint restore replica distributor: invalid options",
		)
	}
	if options.ChunkBytes == 0 {
		options.ChunkBytes = checkpointRestoreReplicaChunkBytes
	}
	if options.ChunkBytes <= 0 ||
		options.ChunkBytes > checkpointRestoreReplicaChunkBytes {
		return nil, fmt.Errorf(
			"backup checkpoint restore replica distributor: invalid chunk size",
		)
	}
	return &CheckpointRestoreReplicaDistributor{
		node: options.Node, local: options.Local,
		remote: options.Remote, chunkBytes: options.ChunkBytes,
	}, nil
}

// DistributeCheckpointRestoreSnapshot installs the local replica first, then
// converges remaining desired peers in at most eight parallel streams.
func (d *CheckpointRestoreReplicaDistributor) DistributeCheckpointRestoreSnapshot(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
	snapshot CheckpointRestoreSnapshot,
) (CheckpointRestoreReplicaResult, error) {
	if d == nil {
		return CheckpointRestoreReplicaResult{},
			fmt.Errorf("backup checkpoint restore replica distributor: unavailable")
	}
	if err := validateCheckpointRestoreFence(fence); err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	route, err := d.node.RouteHashSlot(fence.HashSlot)
	if err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	if route.HashSlot != fence.HashSlot ||
		route.SlotID != fence.TargetSlotID ||
		route.Leader != fence.LeaderNodeID ||
		route.LeaderTerm != fence.LeaderTerm ||
		route.ConfigEpoch != fence.ConfigEpoch ||
		len(route.Peers) != int(fence.ReplicaCount) ||
		d.node.NodeID() != fence.LeaderNodeID ||
		!containsRestoreNode(route.Peers, d.node.NodeID()) ||
		restoreNodeIDsContainDuplicate(route.Peers) {
		return CheckpointRestoreReplicaResult{},
			fmt.Errorf(
				"%w: checkpoint restore distributor Slot fence is stale",
				backupartifact.ErrObjectCorrupt,
			)
	}
	if err := validateCheckpointRestoreSnapshot(
		filepath.Dir(snapshot.Metadata.Path), snapshot,
	); err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	files, _, err := checkpointRestoreReplicaFiles(snapshot)
	if err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	local, err := d.local.InstallCheckpointRestoreSnapshot(
		ctx, fence, snapshot,
	)
	if err != nil || !local.Completed ||
		!validLowerSHA256(local.MetadataSHA256) {
		if err == nil {
			err = fmt.Errorf(
				"%w: local checkpoint restore replica did not complete",
				backupartifact.ErrObjectCorrupt,
			)
		}
		return CheckpointRestoreReplicaResult{}, err
	}
	totalBytes := checkpointRestoreSnapshotBytes(snapshot)
	type outcome struct {
		bytes uint64
		err   error
	}
	peers := append([]uint64(nil), route.Peers...)
	sort.Slice(peers, func(left, right int) bool {
		return peers[left] < peers[right]
	})
	outcomes := make(chan outcome, len(peers)-1)
	limit := make(chan struct{}, checkpointRestoreReplicaParallel)
	var wait sync.WaitGroup
	for _, nodeID := range peers {
		if nodeID == d.node.NodeID() {
			continue
		}
		wait.Add(1)
		go func(target uint64) {
			defer wait.Done()
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				outcomes <- outcome{err: ctx.Err()}
				return
			}
			defer func() { <-limit }()
			var transferErr error
			for attempt := 0; attempt < 8; attempt++ {
				transferErr = d.transferReplica(
					ctx, target, fence, snapshot, files,
					local.MetadataSHA256,
				)
				if transferErr == nil {
					break
				}
				timer := time.NewTimer(
					time.Duration(1<<attempt) * time.Millisecond,
				)
				select {
				case <-ctx.Done():
					timer.Stop()
					transferErr = errors.Join(transferErr, ctx.Err())
					attempt = 8
				case <-timer.C:
				}
			}
			outcomes <- outcome{bytes: totalBytes, err: transferErr}
		}(nodeID)
	}
	wait.Wait()
	close(outcomes)
	result := CheckpointRestoreReplicaResult{
		ReplicaCount:      fence.ReplicaCount,
		ConvergedReplicas: 1,
		MetadataSHA256:    local.MetadataSHA256,
	}
	var convergenceErr error
	for outcome := range outcomes {
		if outcome.err != nil {
			convergenceErr = errors.Join(convergenceErr, outcome.err)
			continue
		}
		result.ConvergedReplicas++
		if ^uint64(0)-result.ReplicatedBytes < outcome.bytes {
			return CheckpointRestoreReplicaResult{},
				fmt.Errorf(
					"%w: checkpoint restore replicated bytes overflow",
					backupartifact.ErrInvalidObject,
				)
		}
		result.ReplicatedBytes += outcome.bytes
	}
	if convergenceErr != nil {
		return result, fmt.Errorf(
			"backup checkpoint restore replica convergence: %w",
			convergenceErr,
		)
	}
	return result, nil
}

func (d *CheckpointRestoreReplicaDistributor) transferReplica(
	ctx context.Context,
	nodeID uint64,
	fence CheckpointRestoreInstallFence,
	snapshot CheckpointRestoreSnapshot,
	files []backupcontract.CheckpointReplicaFile,
	expectedMetadataSHA256 string,
) error {
	contractFence := checkpointRestoreFenceToContract(fence)
	response, err := d.remote.HandleCheckpointReplica(
		ctx, nodeID, backupcontract.CheckpointReplicaRequest{
			Action: backupcontract.CheckpointReplicaBegin,
			Fence:  contractFence, Files: files,
			Evidence:              snapshot.Evidence,
			FinalMessageCount:     snapshot.FinalMessageCount,
			FinalMaxMessageID:     snapshot.FinalMaxMessageID,
			DownloadedBytes:       snapshot.DownloadedBytes,
			InstalledAtUnixMillis: snapshot.InstalledAtUnixMillis,
		},
	)
	if err != nil {
		return err
	}
	if response.Completed {
		if response.MetadataSHA256 != expectedMetadataSHA256 {
			return fmt.Errorf(
				"%w: checkpoint restore completed replica digest mismatch",
				backupartifact.ErrObjectCorrupt,
			)
		}
		return nil
	}
	for _, descriptor := range files {
		path := checkpointRestoreSnapshotPath(snapshot, descriptor)
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		offset := int64(0)
		for offset < descriptor.Size {
			length := int64(d.chunkBytes)
			if descriptor.Size-offset < length {
				length = descriptor.Size - offset
			}
			buffer := make([]byte, int(length))
			if _, err := file.ReadAt(buffer, offset); err != nil {
				_ = file.Close()
				return err
			}
			response, err = d.remote.HandleCheckpointReplica(
				ctx, nodeID, backupcontract.CheckpointReplicaRequest{
					Action: backupcontract.CheckpointReplicaChunk,
					Fence:  contractFence, File: descriptor,
					Offset: offset, Data: buffer,
				},
			)
			if err != nil {
				_ = file.Close()
				return err
			}
			if response.Completed {
				_ = file.Close()
				if response.MetadataSHA256 != expectedMetadataSHA256 {
					return fmt.Errorf(
						"%w: checkpoint restore completed replica digest mismatch",
						backupartifact.ErrObjectCorrupt,
					)
				}
				return nil
			}
			if response.AcceptedOffset < 0 ||
				response.AcceptedOffset > descriptor.Size ||
				response.AcceptedOffset == offset {
				_ = file.Close()
				return fmt.Errorf(
					"%w: checkpoint restore replica offset did not advance",
					backupartifact.ErrObjectCorrupt,
				)
			}
			offset = response.AcceptedOffset
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	response, err = d.remote.HandleCheckpointReplica(
		ctx, nodeID, backupcontract.CheckpointReplicaRequest{
			Action: backupcontract.CheckpointReplicaCommit,
			Fence:  contractFence,
		},
	)
	if err != nil {
		return err
	}
	if !response.Completed ||
		response.MetadataSHA256 != expectedMetadataSHA256 {
		return fmt.Errorf(
			"%w: checkpoint restore replica commit mismatch",
			backupartifact.ErrObjectCorrupt,
		)
	}
	return nil
}

func validateCheckpointRestoreSnapshot(
	attemptDir string,
	snapshot CheckpointRestoreSnapshot,
) error {
	if strings.TrimSpace(attemptDir) == "" ||
		!validCheckpointRestoreEvidence(snapshot.Evidence) ||
		snapshot.InstalledAtUnixMillis <= 0 {
		return fmt.Errorf(
			"%w: checkpoint restore snapshot is invalid",
			backupartifact.ErrObjectCorrupt,
		)
	}
	files := make([]CheckpointRestoreSnapshotFile, 0,
		len(snapshot.Messages)+2)
	files = append(files, snapshot.Metadata)
	files = append(files, snapshot.Messages...)
	files = append(files, snapshot.Erasures)
	cleanDir := filepath.Clean(attemptDir)
	for index, file := range files {
		if filepath.Dir(filepath.Clean(file.Path)) != cleanDir ||
			(index != len(files)-1 && file.Size <= 0) ||
			file.Size < 0 || !validLowerSHA256(file.SHA256) {
			return fmt.Errorf(
				"%w: checkpoint restore snapshot descriptor is invalid",
				backupartifact.ErrObjectCorrupt,
			)
		}
		if err := validateCheckpointRestoreFile(
			file.Path, file.Size, file.SHA256,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateCheckpointRestoreFile(
	path string,
	size int64,
	expectedSHA string,
) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return fmt.Errorf(
			"%w: checkpoint restore snapshot file size mismatch",
			backupartifact.ErrObjectCorrupt,
		)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	copied, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if copied != size ||
		hex.EncodeToString(hash.Sum(nil)) != expectedSHA {
		return fmt.Errorf(
			"%w: checkpoint restore snapshot digest mismatch",
			backupartifact.ErrObjectCorrupt,
		)
	}
	return nil
}

func validCheckpointRestoreEvidence(
	evidence backupartifact.RestoreEvidence,
) bool {
	return evidence.Version == backupartifact.RestoreEvidenceVersion &&
		validLowerSHA256(evidence.ContentSHA256) &&
		validLowerSHA256(evidence.MessageMerkleSHA256) &&
		(evidence.MessageRecords == 0) == (evidence.MaxMessageID == 0)
}

func validateCheckpointReplicaFiles(
	input []backupcontract.CheckpointReplicaFile,
	maxBytes uint64,
) ([]backupcontract.CheckpointReplicaFile, uint64, error) {
	if len(input) < 2 || len(input) > checkpointRestoreExportChannels+2 {
		return nil, 0, fmt.Errorf(
			"%w: checkpoint restore replica file set is invalid",
			backupartifact.ErrInvalidObject,
		)
	}
	files := append([]backupcontract.CheckpointReplicaFile(nil), input...)
	sort.Slice(files, func(left, right int) bool {
		if files[left].Kind != files[right].Kind {
			return files[left].Kind < files[right].Kind
		}
		return files[left].Ordinal < files[right].Ordinal
	})
	var metadata, erasures int
	var messageOrdinal uint32
	var total uint64
	for _, file := range files {
		if file.Size < 0 || !validLowerSHA256(file.SHA256) ||
			uint64(file.Size) > maxBytes ||
			^uint64(0)-total < uint64(file.Size) {
			return nil, 0, fmt.Errorf(
				"%w: checkpoint restore replica file is invalid",
				backupartifact.ErrInvalidObject,
			)
		}
		total += uint64(file.Size)
		switch file.Kind {
		case backupcontract.CheckpointReplicaMetadata:
			metadata++
			if file.Ordinal != 0 || file.Size == 0 {
				return nil, 0, fmt.Errorf(
					"%w: checkpoint restore metadata descriptor is invalid",
					backupartifact.ErrInvalidObject,
				)
			}
		case backupcontract.CheckpointReplicaErasures:
			erasures++
			if file.Ordinal != 0 {
				return nil, 0, fmt.Errorf(
					"%w: checkpoint restore erasure descriptor is invalid",
					backupartifact.ErrInvalidObject,
				)
			}
		case backupcontract.CheckpointReplicaMessages:
			if file.Ordinal != messageOrdinal || file.Size == 0 {
				return nil, 0, fmt.Errorf(
					"%w: checkpoint restore message descriptors are not contiguous",
					backupartifact.ErrInvalidObject,
				)
			}
			messageOrdinal++
		default:
			return nil, 0, fmt.Errorf(
				"%w: checkpoint restore replica file kind is invalid",
				backupartifact.ErrInvalidObject,
			)
		}
	}
	if metadata != 1 || erasures != 1 || total > maxBytes {
		return nil, 0, fmt.Errorf(
			"%w: checkpoint restore replica file set exceeds its bound",
			backupartifact.ErrInvalidObject,
		)
	}
	return files, total, nil
}

func checkpointRestoreReplicaFiles(
	snapshot CheckpointRestoreSnapshot,
) ([]backupcontract.CheckpointReplicaFile, uint64, error) {
	files := make([]backupcontract.CheckpointReplicaFile, 0,
		len(snapshot.Messages)+2)
	files = append(files, backupcontract.CheckpointReplicaFile{
		Kind: backupcontract.CheckpointReplicaMetadata,
		Size: snapshot.Metadata.Size, SHA256: snapshot.Metadata.SHA256,
	})
	for index, message := range snapshot.Messages {
		files = append(files, backupcontract.CheckpointReplicaFile{
			Kind:    backupcontract.CheckpointReplicaMessages,
			Ordinal: uint32(index), Size: message.Size, SHA256: message.SHA256,
		})
	}
	files = append(files, backupcontract.CheckpointReplicaFile{
		Kind: backupcontract.CheckpointReplicaErasures,
		Size: snapshot.Erasures.Size, SHA256: snapshot.Erasures.SHA256,
	})
	return validateCheckpointReplicaFiles(files, ^uint64(0))
}

func checkpointRestoreSnapshotPath(
	snapshot CheckpointRestoreSnapshot,
	descriptor backupcontract.CheckpointReplicaFile,
) string {
	switch descriptor.Kind {
	case backupcontract.CheckpointReplicaMetadata:
		return snapshot.Metadata.Path
	case backupcontract.CheckpointReplicaErasures:
		return snapshot.Erasures.Path
	case backupcontract.CheckpointReplicaMessages:
		if int(descriptor.Ordinal) < len(snapshot.Messages) {
			return snapshot.Messages[descriptor.Ordinal].Path
		}
	}
	return ""
}

func checkpointRestoreReplicaFinalPath(
	attemptDir string,
	file backupcontract.CheckpointReplicaFile,
) string {
	switch file.Kind {
	case backupcontract.CheckpointReplicaMetadata:
		return filepath.Join(attemptDir, "replica-metadata.snapshot")
	case backupcontract.CheckpointReplicaErasures:
		return filepath.Join(attemptDir, "replica-erasures.jsonl")
	default:
		return filepath.Join(
			attemptDir,
			fmt.Sprintf("replica-messages-%06d.snapshot", file.Ordinal),
		)
	}
}

func checkpointRestoreReplicaPartPath(
	attemptDir string,
	file backupcontract.CheckpointReplicaFile,
) string {
	return checkpointRestoreReplicaFinalPath(attemptDir, file) + ".part"
}

func checkpointReplicaFileInSet(
	target backupcontract.CheckpointReplicaFile,
	files []backupcontract.CheckpointReplicaFile,
) bool {
	for _, file := range files {
		if file == target {
			return true
		}
	}
	return false
}

func checkpointReplicaFilesMatchSnapshot(
	files []backupcontract.CheckpointReplicaFile,
	snapshot CheckpointRestoreSnapshot,
) bool {
	expected, _, err := checkpointRestoreReplicaFiles(snapshot)
	if err != nil || len(expected) != len(files) {
		return false
	}
	for index := range files {
		if files[index] != expected[index] {
			return false
		}
	}
	return true
}

func checkpointRestoreSnapshotBytes(
	snapshot CheckpointRestoreSnapshot,
) uint64 {
	total := uint64(snapshot.Metadata.Size) + uint64(snapshot.Erasures.Size)
	for _, message := range snapshot.Messages {
		total += uint64(message.Size)
	}
	return total
}

func checkpointRestoreCompletedResponse(
	snapshot CheckpointRestoreSnapshot,
	metadataSHA256 string,
) backupcontract.CheckpointReplicaResponse {
	return backupcontract.CheckpointReplicaResponse{
		Completed: true, MetadataSHA256: metadataSHA256,
		InstalledBytes: checkpointRestoreSnapshotBytes(snapshot),
	}
}

func checkpointRestoreFenceToContract(
	fence CheckpointRestoreInstallFence,
) backupcontract.CheckpointReplicaFence {
	return backupcontract.CheckpointReplicaFence{
		PlanID: fence.PlanID, CheckpointID: fence.CheckpointID,
		CheckpointSHA256: fence.CheckpointSHA256,
		TargetGeneration: fence.TargetGeneration,
		HashSlot:         fence.HashSlot, TargetSlotID: fence.TargetSlotID,
		ReplicaCount: fence.ReplicaCount,
		LeaderNodeID: fence.LeaderNodeID, LeaderTerm: fence.LeaderTerm,
		ConfigEpoch: fence.ConfigEpoch, Attempt: fence.Attempt,
		InvalidateTokens: fence.InvalidateTokens,
	}
}

func checkpointRestoreFenceFromContract(
	fence backupcontract.CheckpointReplicaFence,
) CheckpointRestoreInstallFence {
	return CheckpointRestoreInstallFence{
		PlanID: fence.PlanID, CheckpointID: fence.CheckpointID,
		CheckpointSHA256: fence.CheckpointSHA256,
		TargetGeneration: fence.TargetGeneration,
		HashSlot:         fence.HashSlot, TargetSlotID: fence.TargetSlotID,
		ReplicaCount: fence.ReplicaCount,
		LeaderNodeID: fence.LeaderNodeID, LeaderTerm: fence.LeaderTerm,
		ConfigEpoch: fence.ConfigEpoch, Attempt: fence.Attempt,
		InvalidateTokens: fence.InvalidateTokens,
	}
}

func restoreNodeIDsContainDuplicate(nodeIDs []uint64) bool {
	seen := make(map[uint64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID == 0 {
			return true
		}
		if _, found := seen[nodeID]; found {
			return true
		}
		seen[nodeID] = struct{}{}
	}
	return false
}

func sameCheckpointRestoreReplicaTransfer(
	left checkpointRestoreReplicaTransfer,
	right checkpointRestoreReplicaTransfer,
) bool {
	if !checkpointRestoreFenceSameIdentity(left.Fence, right.Fence) ||
		left.Evidence != right.Evidence ||
		left.FinalMessageCount != right.FinalMessageCount ||
		left.FinalMaxMessageID != right.FinalMaxMessageID ||
		left.DownloadedBytes != right.DownloadedBytes ||
		left.InstalledAtUnixMillis != right.InstalledAtUnixMillis ||
		len(left.Files) != len(right.Files) {
		return false
	}
	for index := range left.Files {
		if left.Files[index] != right.Files[index] {
			return false
		}
	}
	return true
}

func readCheckpointRestoreReplicaTransfer(
	attemptDir string,
) (checkpointRestoreReplicaTransfer, bool, error) {
	body, err := os.ReadFile(filepath.Join(attemptDir, "transfer.json"))
	if errors.Is(err, os.ErrNotExist) {
		return checkpointRestoreReplicaTransfer{}, false, nil
	}
	if err != nil {
		return checkpointRestoreReplicaTransfer{}, false, err
	}
	var transfer checkpointRestoreReplicaTransfer
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&transfer); err != nil {
		return checkpointRestoreReplicaTransfer{}, false, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return checkpointRestoreReplicaTransfer{}, false,
			fmt.Errorf(
				"%w: checkpoint restore transfer has trailing data",
				backupartifact.ErrObjectCorrupt,
			)
	}
	return transfer, true, nil
}

func writeCheckpointRestoreReplicaTransfer(
	attemptDir string,
	transfer checkpointRestoreReplicaTransfer,
) error {
	body, err := json.Marshal(transfer)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(attemptDir, ".transfer-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err == nil {
		_, err = temp.Write(body)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if err := os.Rename(
		tempPath, filepath.Join(attemptDir, "transfer.json"),
	); err != nil {
		return err
	}
	return syncDirectory(attemptDir)
}

func readCheckpointRestoreReceipt(
	attemptDir string,
) (checkpointRestoreReceipt, bool, error) {
	body, err := os.ReadFile(filepath.Join(attemptDir, "receipt.json"))
	if errors.Is(err, os.ErrNotExist) {
		return checkpointRestoreReceipt{}, false, nil
	}
	if err != nil {
		return checkpointRestoreReceipt{}, false, err
	}
	var receipt checkpointRestoreReceipt
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return checkpointRestoreReceipt{}, false, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return checkpointRestoreReceipt{}, false,
			fmt.Errorf(
				"%w: checkpoint restore receipt has trailing data",
				backupartifact.ErrObjectCorrupt,
			)
	}
	if err := validateCheckpointRestoreReceipt(attemptDir, receipt); err != nil {
		return checkpointRestoreReceipt{}, false, err
	}
	return receipt, true, nil
}

func validateCheckpointRestoreReceipt(
	attemptDir string,
	receipt checkpointRestoreReceipt,
) error {
	if err := validateCheckpointRestoreFence(receipt.Fence); err != nil {
		return err
	}
	if err := validateCheckpointRestoreSnapshot(
		attemptDir, receipt.Snapshot,
	); err != nil {
		return err
	}
	if receipt.Resume.Evidence != receipt.Snapshot.Evidence ||
		receipt.Resume.DownloadedBytes != receipt.Snapshot.DownloadedBytes ||
		receipt.Resume.InstalledAtUnixMillis !=
			receipt.Snapshot.InstalledAtUnixMillis ||
		receipt.Resume.Replicas.ReplicaCount != receipt.Fence.ReplicaCount ||
		receipt.Resume.Replicas.ConvergedReplicas == 0 ||
		receipt.Resume.Replicas.ConvergedReplicas >
			receipt.Resume.Replicas.ReplicaCount ||
		!validLowerSHA256(
			receipt.Resume.Replicas.MetadataSHA256,
		) {
		return fmt.Errorf(
			"%w: checkpoint restore receipt evidence is invalid",
			backupartifact.ErrObjectCorrupt,
		)
	}
	return nil
}

var (
	_ CheckpointRestoreSnapshotDistributor   = (*CheckpointRestoreReplicaDistributor)(nil)
	_ LocalCheckpointRestoreReplicaInstaller = (*CheckpointRestoreReplicaReceiver)(nil)
)
