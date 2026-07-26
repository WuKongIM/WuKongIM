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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channel "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const checkpointRestoreExportChannels = 1024

// CheckpointRestoreSnapshotFile authenticates one target-local snapshot file.
type CheckpointRestoreSnapshotFile struct {
	// Path is the Leader-local absolute staging path.
	Path string
	// Size and SHA256 authenticate the exact plaintext snapshot bytes.
	Size   int64
	SHA256 string
}

// CheckpointRestoreSnapshot is a fully validated target-state image. It
// contains no repository credentials or encrypted source objects.
type CheckpointRestoreSnapshot struct {
	// Metadata is the canonical semantic metadata snapshot.
	Metadata CheckpointRestoreSnapshotFile
	// Messages contains bounded channel batches in stable key order.
	Messages []CheckpointRestoreSnapshotFile
	// Erasures is a bounded-stream JSON index applied before runtime activation.
	Erasures CheckpointRestoreSnapshotFile
	// Evidence is the authenticated single-pass source install result.
	Evidence backupartifact.RestoreEvidence
	// FinalMessageCount and FinalMaxMessageID describe the exported live rows
	// after permanent erasure has been applied.
	FinalMessageCount uint64
	FinalMaxMessageID uint64
	// DownloadedBytes and InstalledAtUnixMillis are durable attempt progress
	// copied to every replica receipt for Leader-change resume.
	DownloadedBytes       uint64
	InstalledAtUnixMillis int64
}

// CheckpointRestoreSnapshotDistributor installs and verifies one final target
// snapshot on the desired Slot replicas. Followers never receive repository or
// KMS handles.
type CheckpointRestoreSnapshotDistributor interface {
	DistributeCheckpointRestoreSnapshot(
		context.Context,
		CheckpointRestoreInstallFence,
		CheckpointRestoreSnapshot,
	) (CheckpointRestoreReplicaResult, error)
}

// DurableCheckpointRestoreTargetOptions configures isolated Leader staging.
type DurableCheckpointRestoreTargetOptions struct {
	// StagingDir retains completed snapshots and receipts across Leader changes.
	StagingDir string
	// StagingMaxBytes bounds all restore attempts retained by this node,
	// including scratch databases, exported snapshots, and receipts.
	StagingMaxBytes uint64
	// StagingQuota is shared by every restore staging component on this node.
	StagingQuota *CheckpointRestoreStagingQuota
	// Distributor installs final plaintext target snapshots on replicas.
	Distributor CheckpointRestoreSnapshotDistributor
	// Now supplies durable completion timestamps.
	Now func() time.Time
}

// DurableCheckpointRestoreTarget replays one Slot into isolated databases,
// exports a final target snapshot, and records an idempotent completion receipt.
type DurableCheckpointRestoreTarget struct {
	// stagingDir is the canonical node-local root for semantic attempts.
	stagingDir string
	// stagingQuota enforces the shared node-wide restore staging ceiling.
	stagingQuota *CheckpointRestoreStagingQuota
	// distributor installs the completed plaintext image on desired replicas.
	distributor CheckpointRestoreSnapshotDistributor
	// now supplies durable installation timestamps.
	now func() time.Time
	// operationLocks singleflight target Begin/Resume for one attempt while
	// allowing the local receiver to enter the shared attempt lock.
	operationLocks checkpointRestoreAttemptLocks
}

// NewDurableCheckpointRestoreTarget creates a fail-closed target.
func NewDurableCheckpointRestoreTarget(
	options DurableCheckpointRestoreTargetOptions,
) (*DurableCheckpointRestoreTarget, error) {
	if strings.TrimSpace(options.StagingDir) == "" ||
		options.StagingMaxBytes == 0 || options.Distributor == nil {
		return nil, fmt.Errorf("backup checkpoint restore target: invalid options")
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
			"backup checkpoint restore target: staging quota mismatch",
		)
	}
	if err := quota.validate(); err != nil {
		return nil, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &DurableCheckpointRestoreTarget{
		stagingDir:   resolved,
		stagingQuota: quota, distributor: options.Distributor, now: options.Now,
	}, nil
}

// ResumeCheckpointRestore loads a completed local receipt without repository
// access and revalidates its exact Controller fence.
func (t *DurableCheckpointRestoreTarget) ResumeCheckpointRestore(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
) (CheckpointRestoreResume, bool, error) {
	if err := validateCheckpointRestoreFence(fence); err != nil {
		return CheckpointRestoreResume{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return CheckpointRestoreResume{}, false, err
	}
	attemptDir := checkpointRestoreAttemptDir(t.stagingDir, fence)
	operationUnlock := t.operationLocks.lock(attemptDir)
	defer operationUnlock()
	if err := ctx.Err(); err != nil {
		return CheckpointRestoreResume{}, false, err
	}
	var receipt checkpointRestoreReceipt
	found, err := func() (bool, error) {
		unlock := t.stagingQuota.attemptLocks.lock(attemptDir)
		defer unlock()
		if err := ctx.Err(); err != nil {
			return false, err
		}
		body, err := os.ReadFile(
			filepath.Join(attemptDir, "receipt.json"),
		)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&receipt); err != nil ||
			!checkpointRestoreFenceSameIdentity(receipt.Fence, fence) ||
			validateCheckpointRestoreReceipt(attemptDir, receipt) != nil {
			return false, fmt.Errorf(
				"%w: checkpoint restore receipt is corrupt",
				backupartifact.ErrObjectCorrupt,
			)
		}
		var trailing json.RawMessage
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return false, fmt.Errorf(
				"%w: checkpoint restore receipt has trailing data",
				backupartifact.ErrObjectCorrupt,
			)
		}
		return true, nil
	}()
	if err != nil {
		return CheckpointRestoreResume{}, false, err
	}
	if !found {
		return CheckpointRestoreResume{}, false, nil
	}
	previousFence := receipt.Fence
	previousReplicas := receipt.Resume.Replicas
	receipt.Fence = fence
	if previousFence != fence ||
		previousReplicas.ReplicaCount != fence.ReplicaCount ||
		previousReplicas.ConvergedReplicas < fence.ReplicaCount {
		result, distributeErr := t.distributor.
			DistributeCheckpointRestoreSnapshot(
				ctx, fence, receipt.Snapshot,
			)
		if result.ReplicaCount != fence.ReplicaCount ||
			result.ConvergedReplicas == 0 ||
			result.ConvergedReplicas > result.ReplicaCount ||
			(previousFence == fence &&
				result.ConvergedReplicas <
					previousReplicas.ConvergedReplicas) ||
			result.MetadataSHA256 !=
				previousReplicas.MetadataSHA256 {
			return CheckpointRestoreResume{}, false,
				fmt.Errorf(
					"%w: resumed replica convergence regressed",
					backupartifact.ErrObjectCorrupt,
				)
		}
		receipt.Resume.Replicas = result
		const aggregateReceiptReservation = uint64(4 << 20)
		receiptPath := filepath.Join(
			attemptDir,
			"receipt.json",
		)
		receiptClaim := receiptPath + "#resume"
		unlock := t.stagingQuota.attemptLocks.lock(attemptDir)
		if err := reserveCheckpointRestoreReceiptClaim(
			t.stagingQuota,
			filepath.Dir(receiptPath),
			receiptClaim,
			aggregateReceiptReservation,
		); err != nil {
			unlock()
			return CheckpointRestoreResume{}, false, err
		}
		writeErr := writeCheckpointRestoreReceipt(
			receiptPath,
			receipt,
		)
		quotaErr := t.stagingQuota.settleClaim(receiptClaim)
		unlock()
		if writeErr != nil || quotaErr != nil {
			return CheckpointRestoreResume{}, false,
				errors.Join(writeErr, quotaErr)
		}
		if distributeErr != nil {
			return receipt.Resume, true, distributeErr
		}
	}
	return receipt.Resume, true, nil
}

// BeginCheckpointRestore creates fresh disposable scratch databases for one
// exact Leader attempt.
func (t *DurableCheckpointRestoreTarget) BeginCheckpointRestore(
	ctx context.Context,
	fence CheckpointRestoreInstallFence,
	stagingClaimBytes uint64,
) (CheckpointRestoreSession, error) {
	if err := validateCheckpointRestoreFence(fence); err != nil {
		return nil, err
	}
	if stagingClaimBytes == 0 ||
		stagingClaimBytes > t.stagingQuota.maxBytes {
		return nil, fmt.Errorf(
			"backup checkpoint restore target: invalid staging claim",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attemptDir := checkpointRestoreAttemptDir(t.stagingDir, fence)
	operationUnlock := t.operationLocks.lock(attemptDir)
	operationTransferred := false
	defer func() {
		if !operationTransferred {
			operationUnlock()
		}
	}()
	sharedUnlock := t.stagingQuota.attemptLocks.lock(attemptDir)
	sharedTransferred := false
	defer func() {
		if !sharedTransferred {
			sharedUnlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "receipt.json")); err == nil {
		return nil, fmt.Errorf("backup checkpoint restore target: attempt already finalized")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := t.stagingQuota.reserveClaim(
		attemptDir, attemptDir, stagingClaimBytes,
	); err != nil {
		return nil, err
	}
	releaseQuota := true
	defer func() {
		if releaseQuota {
			_ = t.stagingQuota.settleClaim(attemptDir)
		}
	}()
	if err := os.RemoveAll(attemptDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(attemptDir, 0o750); err != nil {
		return nil, err
	}
	meta, err := metadb.Open(filepath.Join(attemptDir, "meta"))
	if err != nil {
		_ = os.RemoveAll(attemptDir)
		return nil, err
	}
	messages := channelstore.NewMessageDBFactory(
		filepath.Join(attemptDir, "messages"),
	)
	evidence, err := openCheckpointRestoreEvidenceIndex(
		filepath.Join(attemptDir, "evidence"),
	)
	if err != nil {
		_ = messages.Close()
		_ = meta.Close()
		_ = os.RemoveAll(attemptDir)
		return nil, err
	}
	writer, err := meta.MetaDB().NewRestoreSnapshotWriter(
		ctx, []uint16{fence.HashSlot}, fence.InvalidateTokens,
	)
	if err != nil {
		_ = evidence.Close()
		_ = messages.Close()
		_ = meta.Close()
		_ = os.RemoveAll(attemptDir)
		return nil, err
	}
	stateMachine, err := fsm.NewStateMachineWithHashSlots(
		meta, uint64(fence.TargetSlotID), []uint16{fence.HashSlot},
	)
	if err != nil {
		_ = writer.Close()
		_ = evidence.Close()
		_ = messages.Close()
		_ = meta.Close()
		_ = os.RemoveAll(attemptDir)
		return nil, err
	}
	releaseQuota = false
	operationTransferred = true
	sharedTransferred = true
	return &durableCheckpointRestoreSession{
		target: t, fence: fence, attemptDir: attemptDir,
		meta: meta, metadataWriter: writer, stateMachine: stateMachine,
		messages: messages, evidence: evidence,
		quotaClaim: attemptDir, quotaReservation: stagingClaimBytes,
		operationUnlock: operationUnlock, sharedUnlock: sharedUnlock,
	}, nil
}

func checkpointRestoreAttemptDir(
	stagingDir string,
	fence CheckpointRestoreInstallFence,
) string {
	identity := struct {
		PlanID           string
		CheckpointID     string
		CheckpointSHA256 string
		TargetGeneration string
		HashSlot         uint16
		TargetSlotID     uint32
		InvalidateTokens bool
	}{
		PlanID: fence.PlanID, CheckpointID: fence.CheckpointID,
		CheckpointSHA256: fence.CheckpointSHA256,
		TargetGeneration: fence.TargetGeneration,
		HashSlot:         fence.HashSlot, TargetSlotID: fence.TargetSlotID,
		InvalidateTokens: fence.InvalidateTokens,
	}
	body, _ := json.Marshal(identity)
	digest := sha256.Sum256(body)
	return filepath.Join(
		stagingDir,
		fmt.Sprintf("slot-%05d", fence.HashSlot),
		hex.EncodeToString(digest[:16]),
	)
}

func checkpointRestoreFenceSameIdentity(
	left CheckpointRestoreInstallFence,
	right CheckpointRestoreInstallFence,
) bool {
	return left.PlanID == right.PlanID &&
		left.CheckpointID == right.CheckpointID &&
		left.CheckpointSHA256 == right.CheckpointSHA256 &&
		left.TargetGeneration == right.TargetGeneration &&
		left.HashSlot == right.HashSlot &&
		left.TargetSlotID == right.TargetSlotID &&
		left.InvalidateTokens == right.InvalidateTokens
}

type checkpointRestoreReceipt struct {
	Fence    CheckpointRestoreInstallFence `json:"fence"`
	Resume   CheckpointRestoreResume       `json:"resume"`
	Snapshot CheckpointRestoreSnapshot     `json:"snapshot"`
}

type durableCheckpointRestoreSession struct {
	target     *DurableCheckpointRestoreTarget
	fence      CheckpointRestoreInstallFence
	attemptDir string

	meta           *metadb.DB
	metadataWriter *metadb.RestoreSnapshotWriter
	stateMachine   multiraft.StateMachine
	messages       *channelstore.MessageDBFactory
	evidence       *checkpointRestoreEvidenceIndex

	finalized bool
	closed    bool
	// quotaClaim and quotaReservation own the active target scratch capacity.
	quotaClaim       string
	quotaReservation uint64
	// operationUnlock spans the complete target session; sharedUnlock protects
	// scratch bytes until replica distribution must re-enter the local receiver.
	operationUnlock func()
	sharedUnlock    func()
	// operationOnce and sharedOnce make every terminal/error cleanup idempotent.
	operationOnce sync.Once
	sharedOnce    sync.Once
	// sharedReleased tells Abort to reacquire cross-component exclusion.
	sharedReleased atomic.Bool
}

func (s *durableCheckpointRestoreSession) RestoreEvidenceIndex() backupartifact.RestoreEvidenceIndex {
	return s.evidence
}

func (s *durableCheckpointRestoreSession) ApplyMetadataSnapshot(
	ctx context.Context,
	key []byte,
	value []byte,
) error {
	if err := s.available(ctx); err != nil {
		return err
	}
	return s.metadataWriter.Put(ctx, key, value)
}

func (s *durableCheckpointRestoreSession) ApplyMetadata(
	ctx context.Context,
	record backupartifact.MetadataLogRecord,
) error {
	if err := s.available(ctx); err != nil {
		return err
	}
	if err := s.finishMetadataBaseline(); err != nil {
		return err
	}
	portable, err := fsm.IsRestorePortableCommand(record.Command)
	if err != nil || !portable {
		return err
	}
	_, err = s.stateMachine.Apply(ctx, multiraft.Command{
		SlotID:   multiraft.SlotID(s.fence.TargetSlotID),
		HashSlot: s.fence.HashSlot,
		Index:    record.RaftIndex, Term: record.RaftTerm,
		Data: record.Command,
	})
	return err
}

func (s *durableCheckpointRestoreSession) ApplyMessageBoundary(
	ctx context.Context,
	_ backupartifact.ChannelBoundary,
) error {
	return s.available(ctx)
}

func (s *durableCheckpointRestoreSession) ApplyMessage(
	ctx context.Context,
	record backupartifact.MessageLogRecord,
) error {
	if err := s.available(ctx); err != nil {
		return err
	}
	boundary := backupartifact.ChannelBoundary{
		ChannelID: record.ChannelID, ChannelType: record.ChannelType,
		Epoch: record.Epoch, LogStartOffset: record.LogStartOffset,
		HW: record.HW,
	}
	first, err := s.evidence.claimTargetInitialization(boundary)
	if err != nil {
		return err
	}
	id := channel.ChannelID{ID: record.ChannelID, Type: record.ChannelType}
	if first {
		if err := s.messages.ApplyRestoreChannelBoundary(
			ctx, channelstore.RestoreChannelBoundary{
				ID: id, Epoch: record.Epoch,
				LogStartOffset: record.LogStartOffset,
				HW:             record.LogStartOffset,
			},
		); err != nil {
			return err
		}
	}
	if record.Kind == backupartifact.MessageLogRecordBoundary {
		return nil
	}
	store, err := s.messages.ChannelStore(
		channel.ChannelKeyForID(id), id,
	)
	if err != nil {
		return err
	}
	_, applyErr := store.ApplyFollower(
		ctx, channelstore.ApplyFollowerRequest{
			Records: []channel.Record{{
				ID: record.MessageID, Index: record.MessageSeq,
				Epoch: record.Epoch, Setting: record.Setting,
				FromUID: record.FromUID, ClientMsgNo: record.ClientMsgNo,
				ServerTimestampMS: record.ServerTimestampMS,
				SyncOnce:          record.SyncOnce, Payload: record.Payload,
				SizeBytes: len(record.Payload),
			}},
			LeaderHW: record.MessageSeq,
		},
	)
	closeErr := store.Close()
	return errors.Join(applyErr, closeErr)
}

// StagePermanentErasure collapses one replayed ledger event into the
// disk-backed Channel evidence index without retaining it on the Go heap.
func (s *durableCheckpointRestoreSession) StagePermanentErasure(
	ctx context.Context,
	erasure PermanentErasureBoundary,
) error {
	if err := s.available(ctx); err != nil {
		return err
	}
	if erasure.ChannelID == "" || erasure.ChannelType == 0 ||
		erasure.ThroughSeq == 0 {
		return backupartifact.ErrInvalidObject
	}
	return s.evidence.applyErasure(backupartifact.ChannelBoundary{
		ChannelID: erasure.ChannelID, ChannelType: erasure.ChannelType,
		LogStartOffset: erasure.ThroughSeq, HW: erasure.ThroughSeq,
	})
}

func (s *durableCheckpointRestoreSession) Finalize(
	ctx context.Context,
	evidence backupartifact.RestoreEvidence,
	downloadedBytes uint64,
) (CheckpointRestoreReplicaResult, error) {
	defer func() {
		if s != nil && s.finalized {
			s.releaseTerminalLocks()
		}
	}()
	if err := s.available(ctx); err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	if evidence.ChannelBoundaryCount != s.evidence.ChannelCount() {
		return CheckpointRestoreReplicaResult{},
			fmt.Errorf("%w: restore boundary evidence mismatch", backupartifact.ErrObjectCorrupt)
	}
	if err := s.finishMetadataBaseline(); err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	erasureFile, err := s.applyAndWriteErasures(ctx)
	if err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	messageFiles, finalMessageCount, finalMaxMessageID, err :=
		s.exportMessages(ctx)
	if err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	metadataFile, err := s.exportMetadata(ctx)
	if err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	if err := s.closeDatabases(); err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	attemptBytes, err := checkpointRestoreStagingBytes(s.attemptDir, "")
	if err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	if attemptBytes > s.quotaReservation {
		return CheckpointRestoreReplicaResult{}, fmt.Errorf(
			"%w: checkpoint restore target exceeded its staging claim",
			backupartifact.ErrInvalidObject,
		)
	}
	if err := s.settleQuota(); err != nil {
		return CheckpointRestoreReplicaResult{}, err
	}
	// Replica distribution re-enters the same shared attempt through the
	// Leader-local receiver, so scratch ownership must end before that call.
	s.releaseSharedAttemptLock()
	installedAt := s.target.now().UTC().UnixMilli()
	snapshot := CheckpointRestoreSnapshot{
		Metadata: metadataFile, Messages: messageFiles,
		Erasures: erasureFile, Evidence: evidence,
		FinalMessageCount:     finalMessageCount,
		FinalMaxMessageID:     finalMaxMessageID,
		DownloadedBytes:       downloadedBytes,
		InstalledAtUnixMillis: installedAt,
	}
	result, err := s.target.distributor.DistributeCheckpointRestoreSnapshot(
		ctx, s.fence, snapshot,
	)
	if result.ReplicaCount != s.fence.ReplicaCount ||
		result.ConvergedReplicas == 0 ||
		result.ConvergedReplicas > result.ReplicaCount ||
		!validLowerSHA256(result.MetadataSHA256) {
		return CheckpointRestoreReplicaResult{},
			fmt.Errorf("%w: restore replica result mismatch", backupartifact.ErrObjectCorrupt)
	}
	resume := CheckpointRestoreResume{
		Evidence: evidence, DownloadedBytes: downloadedBytes,
		InstalledAtUnixMillis: installedAt, Replicas: result,
	}
	const aggregateReceiptReservation = uint64(4 << 20)
	receiptPath := filepath.Join(s.attemptDir, "receipt.json")
	receiptClaim := receiptPath + "#aggregate"
	sharedUnlock := s.target.stagingQuota.attemptLocks.lock(s.attemptDir)
	if err := reserveCheckpointRestoreReceiptClaim(
		s.target.stagingQuota,
		s.attemptDir,
		receiptClaim,
		aggregateReceiptReservation,
	); err != nil {
		sharedUnlock()
		s.finalized = true
		return CheckpointRestoreReplicaResult{}, err
	}
	receiptErr := writeCheckpointRestoreReceipt(
		receiptPath,
		checkpointRestoreReceipt{
			Fence: s.fence, Resume: resume, Snapshot: snapshot,
		},
	)
	quotaErr := s.target.stagingQuota.settleClaim(receiptClaim)
	sharedUnlock()
	if receiptErr != nil || quotaErr != nil {
		// The production distributor already persisted a Leader-local replica
		// receipt. Preserve that durable fallback and the immutable snapshot so
		// ResumeCheckpointRestore can finish the aggregate receipt without
		// replaying repository or KMS payloads.
		s.finalized = true
		return CheckpointRestoreReplicaResult{},
			errors.Join(receiptErr, quotaErr)
	}
	s.finalized = true
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *durableCheckpointRestoreSession) Abort(
	_ context.Context,
) error {
	if s == nil {
		return nil
	}
	if s.finalized {
		s.releaseTerminalLocks()
		return nil
	}
	var cleanupUnlock func()
	if s.sharedReleased.Load() {
		cleanupUnlock = s.target.stagingQuota.attemptLocks.lock(s.attemptDir)
	}
	closeErr := s.closeDatabases()
	var removeErr, quotaErr error
	if s.sharedReleased.Load() {
		removeErr = s.target.stagingQuota.removeTrackedPath(
			s.attemptDir, nil,
		)
	} else {
		removeErr = os.RemoveAll(s.attemptDir)
		quotaErr = s.settleQuota()
	}
	if cleanupUnlock != nil {
		cleanupUnlock()
	}
	s.releaseTerminalLocks()
	return errors.Join(closeErr, removeErr, quotaErr)
}

// releaseSharedAttemptLock ends scratch ownership before local distribution.
func (s *durableCheckpointRestoreSession) releaseSharedAttemptLock() {
	if s == nil {
		return
	}
	s.sharedOnce.Do(func() {
		if s.sharedUnlock != nil {
			s.sharedReleased.Store(true)
			s.sharedUnlock()
		}
	})
}

// releaseTerminalLocks releases both cross-component and target singleflight
// ownership exactly once after Finalize or Abort reaches a terminal state.
func (s *durableCheckpointRestoreSession) releaseTerminalLocks() {
	if s == nil {
		return
	}
	s.releaseSharedAttemptLock()
	s.operationOnce.Do(func() {
		if s.operationUnlock != nil {
			s.operationUnlock()
		}
	})
}

func (s *durableCheckpointRestoreSession) settleQuota() error {
	if s == nil || s.quotaClaim == "" {
		return nil
	}
	owner := s.quotaClaim
	s.quotaClaim = ""
	s.quotaReservation = 0
	return s.target.stagingQuota.settleClaim(owner)
}

func (s *durableCheckpointRestoreSession) available(
	ctx context.Context,
) error {
	if s == nil || s.closed || s.finalized {
		return fmt.Errorf("backup checkpoint restore session: closed")
	}
	return ctx.Err()
}

func (s *durableCheckpointRestoreSession) finishMetadataBaseline() error {
	if s.metadataWriter == nil {
		return nil
	}
	writer := s.metadataWriter
	s.metadataWriter = nil
	return writer.Close()
}

func (s *durableCheckpointRestoreSession) closeDatabases() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	writerErr := s.finishMetadataBaseline()
	evidenceErr := s.evidence.Close()
	messageErr := s.messages.Close()
	metaErr := s.meta.Close()
	return errors.Join(writerErr, evidenceErr, messageErr, metaErr)
}

func (s *durableCheckpointRestoreSession) applyAndWriteErasures(
	ctx context.Context,
) (CheckpointRestoreSnapshotFile, error) {
	path := filepath.Join(s.attemptDir, "erasures.jsonl")
	file, err := os.OpenFile(
		path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600,
	)
	if err != nil {
		return CheckpointRestoreSnapshotFile{}, err
	}
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	encoder := json.NewEncoder(writer)
	var total int64
	batch := make([]channelstore.RestorePermanentErasure, 0, 4096)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := s.messages.ApplyRestorePermanentErasures(
			ctx, batch,
		); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	err = s.evidence.visitBoundaries(
		func(
			boundary backupartifact.ChannelBoundary,
			erasureThrough uint64,
		) error {
			if erasureThrough == 0 {
				return nil
			}
			item := channelstore.RestorePermanentErasure{
				ID: channel.ChannelID{
					ID: boundary.ChannelID, Type: boundary.ChannelType,
				},
				Epoch: boundary.Epoch, ThroughSeq: erasureThrough,
			}
			batch = append(batch, item)
			before, _ := file.Seek(0, io.SeekCurrent)
			if err := encoder.Encode(item); err != nil {
				return err
			}
			after, _ := file.Seek(0, io.SeekCurrent)
			total += after - before
			if len(batch) == cap(batch) {
				return flush()
			}
			return nil
		},
	)
	if err == nil {
		err = flush()
	}
	if err != nil {
		_ = file.Close()
		return CheckpointRestoreSnapshotFile{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return CheckpointRestoreSnapshotFile{}, err
	}
	if err := file.Close(); err != nil {
		return CheckpointRestoreSnapshotFile{}, err
	}
	return CheckpointRestoreSnapshotFile{
		Path: path, Size: total, SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func (s *durableCheckpointRestoreSession) exportMessages(
	ctx context.Context,
) ([]CheckpointRestoreSnapshotFile, uint64, uint64, error) {
	result := make([]CheckpointRestoreSnapshotFile, 0)
	var messageCount uint64
	var maxMessageID uint64
	cuts := make([]channelstore.BackupChannelCut, 0, checkpointRestoreExportChannels)
	flush := func() error {
		if len(cuts) == 0 {
			return nil
		}
		reader, stats, err := s.messages.OpenBackupSnapshotWithStats(
			ctx, channelstore.BackupSnapshotRequest{
				HashSlot: s.fence.HashSlot, Channels: cuts,
			},
		)
		if err != nil {
			return err
		}
		if ^uint64(0)-messageCount < stats.MessageCount {
			_ = reader.Close()
			return backupartifact.ErrInvalidObject
		}
		messageCount += stats.MessageCount
		if stats.MaxMessageID > maxMessageID {
			maxMessageID = stats.MaxMessageID
		}
		path := filepath.Join(
			s.attemptDir,
			fmt.Sprintf("messages-%06d.snapshot", len(result)),
		)
		file, err := copyCheckpointRestoreSnapshot(path, reader)
		if err != nil {
			return err
		}
		result = append(result, file)
		cuts = cuts[:0]
		return nil
	}
	err := s.evidence.visitBoundaries(
		func(
			boundary backupartifact.ChannelBoundary,
			erasureThrough uint64,
		) error {
			id := channel.ChannelID{
				ID: boundary.ChannelID, Type: boundary.ChannelType,
			}
			if err := s.messages.ApplyRestoreChannelBoundary(
				ctx, channelstore.RestoreChannelBoundary{
					ID: id, Epoch: boundary.Epoch,
					LogStartOffset: boundary.LogStartOffset, HW: boundary.HW,
				},
			); err != nil {
				return err
			}
			cuts = append(cuts, channelstore.BackupChannelCut{
				Key: channel.ChannelKeyForID(id), ID: id,
				Epoch:          boundary.Epoch,
				LogStartOffset: boundary.LogStartOffset, HW: boundary.HW,
				PermanentEraseThroughSeq: erasureThrough,
			})
			if len(cuts) == cap(cuts) {
				return flush()
			}
			return nil
		},
	)
	if err != nil {
		return nil, 0, 0, err
	}
	if err := flush(); err != nil {
		return nil, 0, 0, err
	}
	return result, messageCount, maxMessageID, nil
}

func (s *durableCheckpointRestoreSession) exportMetadata(
	ctx context.Context,
) (CheckpointRestoreSnapshotFile, error) {
	reader, err := s.meta.OpenBackupHashSlotSnapshot(
		ctx, []uint16{s.fence.HashSlot},
	)
	if err != nil {
		return CheckpointRestoreSnapshotFile{}, err
	}
	return copyCheckpointRestoreSnapshot(
		filepath.Join(s.attemptDir, "metadata.snapshot"), reader,
	)
}

func copyCheckpointRestoreSnapshot(
	path string,
	reader io.ReadCloser,
) (CheckpointRestoreSnapshotFile, error) {
	file, err := os.OpenFile(
		path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600,
	)
	if err != nil {
		_ = reader.Close()
		return CheckpointRestoreSnapshotFile{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), reader)
	readerErr := reader.Close()
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, readerErr, syncErr, closeErr); err != nil {
		_ = os.Remove(path)
		return CheckpointRestoreSnapshotFile{}, err
	}
	return CheckpointRestoreSnapshotFile{
		Path: path, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func checkpointRestoreStagingBytes(
	root string,
	excludeDir string,
) (uint64, error) {
	root = filepath.Clean(root)
	excludeDir = filepath.Clean(excludeDir)
	var total uint64
	err := filepath.WalkDir(
		root,
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			clean := filepath.Clean(path)
			if excludeDir != "." && excludeDir != "" &&
				clean == excludeDir && entry.IsDir() {
				return filepath.SkipDir
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf(
					"%w: checkpoint restore staging contains a symlink",
					backupartifact.ErrObjectCorrupt,
				)
			}
			if entry.IsDir() {
				return nil
			}
			if !info.Mode().IsRegular() || info.Size() < 0 ||
				uint64(info.Size()) > ^uint64(0)-total {
				return backupartifact.ErrInvalidObject
			}
			total += uint64(info.Size())
			return nil
		},
	)
	return total, err
}

func writeCheckpointRestoreReceipt(
	path string,
	receipt checkpointRestoreReceipt,
) error {
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".receipt-*.tmp")
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
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func reserveCheckpointRestoreReceiptClaim(
	quota *CheckpointRestoreStagingQuota,
	attemptDir string,
	owner string,
	peakOverhead uint64,
) error {
	attemptBytes, err := checkpointRestoreStagingBytes(attemptDir, "")
	if err != nil {
		return err
	}
	if attemptBytes > ^uint64(0)-peakOverhead {
		return backupartifact.ErrInvalidObject
	}
	return quota.reserveClaim(
		owner, attemptDir, attemptBytes+peakOverhead,
	)
}

func validateCheckpointRestoreFence(
	fence CheckpointRestoreInstallFence,
) error {
	if strings.TrimSpace(fence.PlanID) == "" ||
		strings.TrimSpace(fence.CheckpointID) == "" ||
		!validLowerSHA256(fence.CheckpointSHA256) ||
		strings.TrimSpace(fence.TargetGeneration) == "" ||
		fence.TargetSlotID == 0 || fence.ReplicaCount == 0 ||
		fence.LeaderNodeID == 0 || fence.LeaderTerm == 0 ||
		fence.ConfigEpoch == 0 || fence.Attempt == 0 {
		return fmt.Errorf("backup checkpoint restore target: invalid fence")
	}
	return nil
}

var (
	_ CheckpointRestoreTarget          = (*DurableCheckpointRestoreTarget)(nil)
	_ CheckpointRestoreSession         = (*durableCheckpointRestoreSession)(nil)
	_ CheckpointRestoreEvidenceSession = (*durableCheckpointRestoreSession)(nil)
)
