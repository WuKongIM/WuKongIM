package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"golang.org/x/sync/semaphore"
)

// CheckpointRestoreRecordSink consumes authenticated portable records in
// chronological order.
type CheckpointRestoreRecordSink interface {
	MetadataSnapshot([]byte, []byte) error
	Metadata([]byte) error
	Message([]byte) error
	Boundary(backupartifact.ChannelBoundary) error
}

// CheckpointBaselineReplayer converts one materialized baseline into the same
// portable chronological record stream used by incremental segments.
type CheckpointBaselineReplayer interface {
	ReplayCheckpointBaseline(
		context.Context,
		backupartifact.Repository,
		backupartifact.CheckpointSlot,
		CheckpointRestoreRecordSink,
	) (uint64, error)
}

// CheckpointRestoreInstallFence binds target-local staging to one durable
// Controller attempt and current Slot Leader authority.
type CheckpointRestoreInstallFence struct {
	// PlanID and Checkpoint identity bind target-local state to one immutable plan.
	PlanID           string
	CheckpointID     string
	CheckpointSHA256 string
	// TargetGeneration prevents replay into a different successor incarnation.
	TargetGeneration string
	// HashSlot and TargetSlotID identify the logical and physical partitions.
	HashSlot     uint16
	TargetSlotID uint32
	// ReplicaCount is the exact desired target Slot replica width.
	ReplicaCount uint32
	// LeaderNodeID, LeaderTerm, and ConfigEpoch fence current target authority.
	LeaderNodeID uint64
	LeaderTerm   uint64
	ConfigEpoch  uint64
	// Attempt is the Controller-durable monotonically increasing install attempt.
	Attempt uint64
	// InvalidateTokens applies the immutable restore-time credential transform.
	InvalidateTokens bool
}

// CheckpointRestoreResume is target-durable completion evidence used when the
// Controller report was interrupted after Leader finalization.
type CheckpointRestoreResume struct {
	// Evidence is the target-durable single-pass logical install summary.
	Evidence backupartifact.RestoreEvidence
	// DownloadedBytes is the authenticated source byte count for this attempt.
	DownloadedBytes uint64
	// InstalledAtUnixMillis records target-local finalization time.
	InstalledAtUnixMillis int64
	// Replicas reports current convergence for the finalized target snapshot.
	Replicas CheckpointRestoreReplicaResult
}

// CheckpointRestoreReplicaResult reports follower convergence achieved through
// the target cluster's existing snapshot/replication mechanism.
type CheckpointRestoreReplicaResult struct {
	// ReplicaCount is the exact desired replica width of the target Slot.
	ReplicaCount uint32
	// ConvergedReplicas is the number of replicas that durably installed and
	// verified the same final target snapshot.
	ConvergedReplicas uint32
	// ReplicatedBytes counts target snapshot bytes acknowledged by followers.
	ReplicatedBytes uint64
	// MetadataSHA256 authenticates the canonical target metadata snapshot after
	// restore-time transforms, rather than the source record stream.
	MetadataSHA256 string
}

// CheckpointRestoreSession is a disposable target-Leader install. Finalize is
// the only operation allowed to make validated state durable and start replica
// convergence; Abort must discard an unfinalized partial install.
type CheckpointRestoreSession interface {
	ApplyMetadataSnapshot(context.Context, []byte, []byte) error
	ApplyMetadata(context.Context, backupartifact.MetadataLogRecord) error
	ApplyMessageBoundary(context.Context, backupartifact.ChannelBoundary) error
	ApplyMessage(context.Context, backupartifact.MessageLogRecord) error
	StagePermanentErasure(context.Context, PermanentErasureBoundary) error
	Finalize(
		context.Context,
		backupartifact.RestoreEvidence,
		uint64,
	) (CheckpointRestoreReplicaResult, error)
	Abort(context.Context) error
}

// CheckpointRestoreEvidenceSession exposes a disk-backed Channel evidence index.
// Implementations that omit it retain the in-memory test implementation.
type CheckpointRestoreEvidenceSession interface {
	RestoreEvidenceIndex() backupartifact.RestoreEvidenceIndex
}

// CheckpointRestoreTarget opens one idempotent attempt on the current Slot Leader.
type CheckpointRestoreTarget interface {
	ResumeCheckpointRestore(
		context.Context,
		CheckpointRestoreInstallFence,
	) (CheckpointRestoreResume, bool, error)
	BeginCheckpointRestore(
		context.Context,
		CheckpointRestoreInstallFence,
		uint64,
	) (CheckpointRestoreSession, error)
}

// CheckpointRestoreProgressReporter persists active download progress before a
// complete Slot result is available.
type CheckpointRestoreProgressReporter func(
	context.Context,
	string,
	backupusecase.RestorePartition,
) error

// CheckpointSlotInstallerOptions configures one Leader-only checkpoint importer.
type CheckpointSlotInstallerOptions struct {
	// Primary and Secondary are the independent immutable repository copies.
	Primary, Secondary backupartifact.Repository
	// Catalog and Segments load only objects pinned by the admitted proof.
	Catalog  *ReplicatedCheckpointCatalog
	Segments *backupartifact.ReplicatedSegmentStore
	// Signer and Codec authenticate erasure evidence and encrypted objects.
	Signer backupartifact.ManifestSigner
	Codec  *backupartifact.ObjectCodec
	// RepositoryID fences both physical copies to one logical repository.
	RepositoryID string
	// Baseline replays a materialized Generation root into portable records.
	Baseline CheckpointBaselineReplayer
	// Target owns disposable Leader-local installation and replica convergence.
	Target CheckpointRestoreTarget
	// StagingDir and StagingMaxBytes bound authenticated on-disk replay data.
	StagingDir      string
	StagingMaxBytes uint64
	// StagingQuota is shared by every restore staging component on this node.
	StagingQuota *CheckpointRestoreStagingQuota
	// MemoryMaxBytes bounds all concurrent plaintext and replay record working sets.
	MemoryMaxBytes uint64
	// Progress durably reports active per-Slot authenticated download bytes.
	Progress CheckpointRestoreProgressReporter
	// Now supplies UTC progress timestamps.
	Now func() time.Time
}

// CheckpointSlotInstaller downloads and decrypts each immutable Slot payload
// once on the target Leader, then replays local staging in chronological order.
type CheckpointSlotInstaller struct {
	options CheckpointSlotInstallerOptions

	stagingQuota *CheckpointRestoreStagingQuota
	memoryMax    int64
	memoryBudget *semaphore.Weighted
}

// NewCheckpointSlotInstaller creates a bounded Leader-only importer.
func NewCheckpointSlotInstaller(
	options CheckpointSlotInstallerOptions,
) (*CheckpointSlotInstaller, error) {
	if options.Primary == nil || options.Secondary == nil ||
		options.Primary.Name() == options.Secondary.Name() ||
		options.Catalog == nil || options.Segments == nil ||
		options.Signer == nil || options.Codec == nil ||
		options.Baseline == nil || options.Target == nil ||
		strings.TrimSpace(options.RepositoryID) == "" ||
		strings.TrimSpace(options.StagingDir) == "" ||
		options.StagingMaxBytes == 0 || options.MemoryMaxBytes == 0 ||
		options.Progress == nil ||
		options.MemoryMaxBytes > uint64(^uint64(0)>>1) {
		return nil, fmt.Errorf("backup checkpoint Slot installer: invalid options")
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
	options.StagingDir = resolved
	options.RepositoryID = strings.TrimSpace(options.RepositoryID)
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
			"backup checkpoint Slot installer: staging quota mismatch",
		)
	}
	if err := quota.validate(); err != nil {
		return nil, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	memoryMax := int64(options.MemoryMaxBytes)
	installer := &CheckpointSlotInstaller{
		options: options, stagingQuota: quota, memoryMax: memoryMax,
		memoryBudget: semaphore.NewWeighted(memoryMax),
	}
	if binder, ok := options.Baseline.(interface {
		bindCheckpointRestoreBudget(*CheckpointSlotInstaller)
	}); ok {
		binder.bindCheckpointRestoreBudget(installer)
	}
	return installer, nil
}

// InstallPartition imports one checkpoint Slot under its durable Leader fence.
func (i *CheckpointSlotInstaller) InstallPartition(
	ctx context.Context,
	plan backupusecase.RestorePlan,
	hashSlot uint16,
) (backupusecase.RestorePartition, error) {
	if i == nil || plan.CatalogProof == nil || hashSlot >= plan.HashSlotCount ||
		len(plan.Partitions) != int(plan.HashSlotCount) ||
		(plan.Repository != "primary" && plan.Repository != "secondary") {
		return backupusecase.RestorePartition{}, backupusecase.ErrInvalidRequest
	}
	progress := plan.Partitions[hashSlot]
	if (progress.Status != backupcontract.RestorePartitionInstalling &&
		progress.Status != backupcontract.RestorePartitionInstalled &&
		progress.Status != backupcontract.RestorePartitionConverging) ||
		progress.TargetSlotID == 0 || progress.LeaderNodeID == 0 ||
		progress.LeaderTerm == 0 || progress.ConfigEpoch == 0 ||
		progress.InstallAttempt == 0 || progress.ReplicaCount == 0 {
		return backupusecase.RestorePartition{}, backupusecase.ErrRestoreTransition
	}
	fence := CheckpointRestoreInstallFence{
		PlanID: plan.ID, CheckpointID: plan.RestorePointID,
		CheckpointSHA256: plan.ManifestSHA256,
		TargetGeneration: plan.TargetGeneration,
		HashSlot:         hashSlot, TargetSlotID: progress.TargetSlotID,
		ReplicaCount: progress.ReplicaCount,
		LeaderNodeID: progress.LeaderNodeID, LeaderTerm: progress.LeaderTerm,
		ConfigEpoch: progress.ConfigEpoch, Attempt: progress.InstallAttempt,
		InvalidateTokens: plan.InvalidateTokens,
	}
	resumed, found, err := i.options.Target.ResumeCheckpointRestore(ctx, fence)
	if found {
		report, reportErr := checkpointRestoreReport(
			progress, resumed.Evidence, resumed.DownloadedBytes,
			resumed.InstalledAtUnixMillis, resumed.Replicas,
			i.options.Now().UTC().UnixMilli(),
		)
		return report, errors.Join(reportErr, err)
	}
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	if progress.Status != backupcontract.RestorePartitionInstalling {
		return backupusecase.RestorePartition{},
			fmt.Errorf(
				"%w: durable restore completion is missing",
				backupusecase.ErrStateConflict,
			)
	}
	repository := i.options.Primary
	if plan.Repository == "secondary" {
		repository = i.options.Secondary
	}
	checkpoint, err := i.options.Catalog.LoadCheckpointProofCopy(
		ctx, repository, *plan.CatalogProof,
	)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	if checkpoint.ID != plan.RestorePointID ||
		plan.CatalogProof.Checkpoint.SHA256 != plan.ManifestSHA256 ||
		checkpoint.RepositoryID != i.options.RepositoryID ||
		checkpoint.SourceClusterID != plan.SourceClusterID ||
		checkpoint.SourceGeneration != plan.SourceGeneration ||
		checkpoint.HashSlotCount != plan.HashSlotCount ||
		checkpoint.Version != plan.CheckpointVersion ||
		checkpoint.CreatedAtUnixMillis != plan.CheckpointCreatedAtUnixMillis ||
		checkpoint.EffectiveAtUnixMillis != plan.CheckpointEffectiveAtUnixMillis {
		return backupusecase.RestorePartition{},
			fmt.Errorf("%w: restore checkpoint plan fence mismatch", backupartifact.ErrObjectCorrupt)
	}
	ledgerLoader, err := NewErasureLedgerLoader(ErasureLedgerLoaderOptions{
		Primary: i.options.Primary, Secondary: i.options.Secondary,
		Signer: i.options.Signer, Codec: i.options.Codec,
		RepositoryID:     i.options.RepositoryID,
		SourceClusterID:  plan.SourceClusterID,
		SourceGeneration: plan.SourceGeneration,
		HashSlotCount:    plan.HashSlotCount,
	})
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	slot := checkpoint.Slots[hashSlot]
	staged := make([]stagedRestoreSegment, 0)
	var reserved uint64
	defer func() {
		for _, segment := range staged {
			_ = i.stagingQuota.removeCommittedPath(
				segment.path, uint64(segment.bytes),
			)
		}
	}()
	for _, stream := range []struct {
		kind backupartifact.SegmentStream
		head backupartifact.CheckpointStream
	}{
		{backupartifact.SegmentStreamMetadata, slot.Metadata},
		{backupartifact.SegmentStreamMessages, slot.Messages},
	} {
		segments, bytes, err := i.stageStream(
			ctx, repository, checkpoint, slot, stream.kind, stream.head,
			reserved,
		)
		if err != nil {
			return backupusecase.RestorePartition{}, err
		}
		staged = append(staged, segments...)
		reserved += bytes
		if err := i.reportActiveProgress(
			ctx, plan.ID, progress, reserved,
		); err != nil {
			return backupusecase.RestorePartition{}, err
		}
	}
	cursorBytes, err := i.validateMessageCursorHead(
		ctx, repository, checkpoint, slot,
	)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	if ^uint64(0)-reserved < cursorBytes {
		return backupusecase.RestorePartition{},
			fmt.Errorf(
				"%w: restore download bytes overflow",
				backupartifact.ErrInvalidObject,
			)
	}
	if err := i.reportActiveProgress(
		ctx, plan.ID, progress, reserved+cursorBytes,
	); err != nil {
		return backupusecase.RestorePartition{}, err
	}

	stagingClaim, err := i.checkpointRestoreTargetStagingClaim(
		ctx, repository, slot, reserved+cursorBytes,
	)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	session, err := i.options.Target.BeginCheckpointRestore(
		ctx, fence, stagingClaim,
	)
	if err != nil || session == nil {
		if err == nil {
			err = fmt.Errorf(
				"backup checkpoint Slot installer: target returned a nil session",
			)
		}
		return backupusecase.RestorePartition{}, err
	}
	finalized := false
	defer func() {
		if !finalized {
			_ = session.Abort(context.WithoutCancel(ctx))
		}
	}()
	var evidenceIndex backupartifact.RestoreEvidenceIndex
	if indexed, ok := session.(CheckpointRestoreEvidenceSession); ok {
		evidenceIndex = indexed.RestoreEvidenceIndex()
	}
	accumulator := backupartifact.NewRestoreEvidenceAccumulatorWithIndex(
		hashSlot, evidenceIndex,
	)
	sink := checkpointRestoreSink{
		ctx: ctx, accumulator: accumulator, session: session,
	}
	downloaded := reserved + cursorBytes
	if slot.Baseline != nil {
		baselineBytes, err := i.options.Baseline.ReplayCheckpointBaseline(
			ctx, repository, slot, &sink,
		)
		if err != nil {
			return backupusecase.RestorePartition{}, err
		}
		if ^uint64(0)-downloaded < baselineBytes {
			return backupusecase.RestorePartition{},
				fmt.Errorf("%w: restore download bytes overflow", backupartifact.ErrInvalidObject)
		}
		downloaded += baselineBytes
		if err := i.reportActiveProgress(
			ctx, plan.ID, progress, downloaded,
		); err != nil {
			return backupusecase.RestorePartition{}, err
		}
	}
	for _, stream := range []backupartifact.SegmentStream{
		backupartifact.SegmentStreamMetadata,
		backupartifact.SegmentStreamMessages,
	} {
		for index := len(staged) - 1; index >= 0; index-- {
			if staged[index].stream != stream {
				continue
			}
			if err := i.replayStagedSegment(
				ctx, staged[index], &sink,
			); err != nil {
				return backupusecase.RestorePartition{}, err
			}
		}
	}
	if err := ledgerLoader.ReplayPinnedSlot(
		ctx, plan.Repository, plan.ErasureLedgerVersion,
		plan.ErasureEventCount, plan.ErasureLedgerSHA256,
		plan.ErasureHeads, hashSlot,
		func(boundary PermanentErasureBoundary) error {
			return session.StagePermanentErasure(ctx, boundary)
		},
	); err != nil {
		return backupusecase.RestorePartition{}, err
	}
	evidence, err := accumulator.Finish()
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	if slot.Baseline != nil &&
		(evidence.MetadataRecords < slot.Baseline.Partition.Evidence.MetadataRecords ||
			evidence.MessageRecords < slot.Baseline.Partition.Evidence.MessageRecords ||
			evidence.MaxMessageID < slot.Baseline.Partition.Evidence.MaxMessageID) {
		return backupusecase.RestorePartition{},
			fmt.Errorf(
				"%w: restore baseline evidence is incomplete",
				backupartifact.ErrObjectCorrupt,
			)
	}
	replicas, err := session.Finalize(ctx, evidence, downloaded)
	if err != nil {
		report, reportErr := checkpointRestoreReport(
			progress, evidence, downloaded,
			i.options.Now().UTC().UnixMilli(), replicas,
			i.options.Now().UTC().UnixMilli(),
		)
		if reportErr == nil {
			finalized = true
			return report, err
		}
		return backupusecase.RestorePartition{},
			errors.Join(err, reportErr)
	}
	finalized = true
	now := i.options.Now().UTC().UnixMilli()
	return checkpointRestoreReport(
		progress, evidence, downloaded, now, replicas, now,
	)
}

func (i *CheckpointSlotInstaller) checkpointRestoreTargetStagingClaim(
	ctx context.Context,
	repository backupartifact.Repository,
	slot backupartifact.CheckpointSlot,
	sourceBytes uint64,
) (uint64, error) {
	if slot.Baseline != nil {
		layers, err := loadRestorePartitionLayers(
			ctx, repository, slot.Baseline.Partition,
		)
		if err != nil {
			return 0, err
		}
		for _, layer := range layers {
			for _, object := range layer.Objects {
				if object.PlaintextBytes <= 0 ||
					uint64(object.PlaintextBytes) >
						^uint64(0)-sourceBytes {
					return 0, backupartifact.ErrInvalidObject
				}
				sourceBytes += uint64(object.PlaintextBytes)
			}
		}
		if slot.Baseline.MessageCursor.PlaintextBytes <= 0 ||
			uint64(slot.Baseline.MessageCursor.PlaintextBytes) >
				^uint64(0)-sourceBytes {
			return 0, backupartifact.ErrInvalidObject
		}
		sourceBytes += uint64(
			slot.Baseline.MessageCursor.PlaintextBytes,
		)
	}
	const fixedScratchBytes = uint64(16 << 20)
	if sourceBytes > (^uint64(0)-fixedScratchBytes)/4 {
		return 0, backupartifact.ErrInvalidObject
	}
	claim := sourceBytes*4 + fixedScratchBytes
	if claim > i.options.StagingMaxBytes {
		return 0, fmt.Errorf(
			"%w: checkpoint restore target claim %d exceeds node staging quota %d",
			backupartifact.ErrInvalidObject, claim,
			i.options.StagingMaxBytes,
		)
	}
	return claim, nil
}

func (i *CheckpointSlotInstaller) reportActiveProgress(
	ctx context.Context,
	planID string,
	progress backupusecase.RestorePartition,
	downloadedBytes uint64,
) error {
	if downloadedBytes < progress.DownloadedBytes {
		downloadedBytes = progress.DownloadedBytes
	}
	progress.Status = backupcontract.RestorePartitionInstalling
	progress.DownloadedBytes = downloadedBytes
	progress.UpdatedAtUnixMillis = 0
	_, err := checkpointRestoreReportInstalling(progress)
	if err != nil {
		return err
	}
	return i.options.Progress(ctx, planID, progress)
}

func checkpointRestoreReportInstalling(
	progress backupusecase.RestorePartition,
) (backupusecase.RestorePartition, error) {
	if progress.TargetSlotID == 0 || progress.LeaderNodeID == 0 ||
		progress.LeaderTerm == 0 || progress.ConfigEpoch == 0 ||
		progress.InstallAttempt == 0 || progress.ReplicaCount == 0 ||
		progress.StartedAtUnixMillis <= 0 {
		return backupusecase.RestorePartition{},
			backupusecase.ErrRestoreTransition
	}
	return progress, nil
}

func checkpointRestoreReport(
	progress backupusecase.RestorePartition,
	evidence backupartifact.RestoreEvidence,
	downloadedBytes uint64,
	installedAtUnixMillis int64,
	replicas CheckpointRestoreReplicaResult,
	updatedAtUnixMillis int64,
) (backupusecase.RestorePartition, error) {
	if downloadedBytes < progress.DownloadedBytes {
		downloadedBytes = progress.DownloadedBytes
	}
	if evidence.Version != backupartifact.RestoreEvidenceVersion ||
		!validLowerSHA256(evidence.ContentSHA256) ||
		!validLowerSHA256(evidence.MessageMerkleSHA256) ||
		(evidence.MessageRecords == 0) != (evidence.MaxMessageID == 0) ||
		installedAtUnixMillis < progress.StartedAtUnixMillis ||
		replicas.ReplicaCount != progress.ReplicaCount ||
		!validLowerSHA256(replicas.MetadataSHA256) ||
		replicas.ConvergedReplicas == 0 ||
		replicas.ConvergedReplicas > replicas.ReplicaCount {
		return backupusecase.RestorePartition{},
			fmt.Errorf(
				"%w: restore completion evidence is invalid",
				backupartifact.ErrObjectCorrupt,
			)
	}
	status := backupcontract.RestorePartitionInstalled
	if replicas.ConvergedReplicas == replicas.ReplicaCount {
		status = backupcontract.RestorePartitionConverged
	} else if replicas.ConvergedReplicas > 1 {
		status = backupcontract.RestorePartitionConverging
	}
	return backupusecase.RestorePartition{
		HashSlot: progress.HashSlot, Status: status,
		TargetSlotID: progress.TargetSlotID, LeaderNodeID: progress.LeaderNodeID,
		LeaderTerm: progress.LeaderTerm, ConfigEpoch: progress.ConfigEpoch,
		InstallAttempt:  progress.InstallAttempt,
		EvidenceVersion: evidence.Version, Installed: true,
		PlainBytes:          evidence.PlainBytes,
		MetadataRecordCount: evidence.MetadataRecords,
		MessageCount:        evidence.MessageRecords, MaxMessageID: evidence.MaxMessageID,
		MetadataSHA256:       replicas.MetadataSHA256,
		ContentSHA256:        evidence.ContentSHA256,
		MessageMerkleSHA256:  evidence.MessageMerkleSHA256,
		ChannelBoundaryCount: evidence.ChannelBoundaryCount,
		DownloadedBytes:      downloadedBytes, ReplicatedBytes: replicas.ReplicatedBytes,
		ReplicaCount:          replicas.ReplicaCount,
		ConvergedReplicas:     replicas.ConvergedReplicas,
		StartedAtUnixMillis:   progress.StartedAtUnixMillis,
		InstalledAtUnixMillis: installedAtUnixMillis,
		UpdatedAtUnixMillis:   updatedAtUnixMillis,
	}, nil
}

type checkpointRestoreSink struct {
	ctx         context.Context
	accumulator *backupartifact.RestoreEvidenceAccumulator
	session     CheckpointRestoreSession
}

func (s *checkpointRestoreSink) MetadataSnapshot(
	key []byte,
	value []byte,
) error {
	if err := s.accumulator.AddMetadataSnapshot(key, value); err != nil {
		return err
	}
	return s.session.ApplyMetadataSnapshot(s.ctx, key, value)
}

func (s *checkpointRestoreSink) Metadata(body []byte) error {
	record, err := s.accumulator.AddMetadata(body)
	if err != nil {
		return err
	}
	return s.session.ApplyMetadata(s.ctx, record)
}

func (s *checkpointRestoreSink) Message(body []byte) error {
	record, err := s.accumulator.AddMessage(body)
	if err != nil {
		return err
	}
	return s.session.ApplyMessage(s.ctx, record)
}

func (s *checkpointRestoreSink) Boundary(
	boundary backupartifact.ChannelBoundary,
) error {
	if err := s.accumulator.MergeBoundary(boundary); err != nil {
		return err
	}
	return s.session.ApplyMessageBoundary(s.ctx, boundary)
}

type stagedRestoreSegment struct {
	stream backupartifact.SegmentStream
	path   string
	bytes  int64
}

func (i *CheckpointSlotInstaller) stageStream(
	ctx context.Context,
	repository backupartifact.Repository,
	checkpoint backupartifact.Checkpoint,
	slot backupartifact.CheckpointSlot,
	stream backupartifact.SegmentStream,
	cut backupartifact.CheckpointStream,
	completedBytes uint64,
) (result []stagedRestoreSegment, reserved uint64, returnErr error) {
	if cut.Sequence == 0 {
		return nil, 0, nil
	}
	if cut.Head == nil {
		return nil, 0, backupartifact.ErrInvalidObject
	}
	current := *cut.Head
	expectedSequence := cut.Sequence
	// A generation rebase bounds ordinary production chains, but the schema
	// deliberately accepts any authenticated chain that fits the configured
	// staging budget. This avoids coupling restore correctness to one runtime
	// rebase threshold.
	result = make([]stagedRestoreSegment, 0)
	defer func() {
		if returnErr == nil {
			return
		}
		for _, segment := range result {
			returnErr = errors.Join(
				returnErr,
				i.stagingQuota.removeCommittedPath(
					segment.path, uint64(segment.bytes),
				),
			)
		}
		result = nil
		reserved = 0
	}()
	for expectedSequence > 0 {
		reserved += uint64(current.PlaintextBytes)
		segment, header, info, err := i.stageSegmentCopy(
			ctx, repository, current, stream,
		)
		if err != nil {
			return nil, reserved, err
		}
		if header.Logical.RepositoryID != checkpoint.RepositoryID ||
			header.Logical.SourceClusterID != checkpoint.SourceClusterID ||
			header.Logical.SourceGeneration != checkpoint.SourceGeneration ||
			header.Logical.Generation != slot.Generation ||
			header.Logical.HashSlot != slot.HashSlot ||
			header.Logical.Stream != stream ||
			header.Logical.Sequence != expectedSequence ||
			header.Logical.RecordCount != info.RecordCount ||
			info.HashSlot != slot.HashSlot || info.Generation != slot.Generation ||
			info.Stream != stream || info.Sequence != expectedSequence {
			cleanupErr := i.stagingQuota.removeCommittedPath(
				segment.path, uint64(segment.bytes),
			)
			return nil, reserved, errors.Join(
				fmt.Errorf(
					"%w: restore segment logical fence mismatch",
					backupartifact.ErrObjectCorrupt,
				),
				cleanupErr,
			)
		}
		result = append(result, segment)
		if completedBytes > ^uint64(0)-reserved {
			return nil, reserved, fmt.Errorf(
				"%w: restore download bytes overflow",
				backupartifact.ErrInvalidObject,
			)
		}
		if expectedSequence == 1 {
			if info.Previous != nil {
				return nil, reserved, backupartifact.ErrObjectCorrupt
			}
			break
		}
		if info.Previous == nil {
			return nil, reserved, backupartifact.ErrObjectCorrupt
		}
		current = *info.Previous
		expectedSequence--
	}
	return result, reserved, nil
}

func (i *CheckpointSlotInstaller) stageSegmentCopy(
	ctx context.Context,
	repository backupartifact.Repository,
	reference backupartifact.SegmentReference,
	stream backupartifact.SegmentStream,
) (
	stagedRestoreSegment,
	backupartifact.SegmentHeader,
	backupartifact.SegmentBatchInfo,
	error,
) {
	weight, ok := checkpointRestoreMemoryWeight(reference.PlaintextBytes, 2)
	if !ok || weight > i.memoryMax {
		return stagedRestoreSegment{}, backupartifact.SegmentHeader{},
			backupartifact.SegmentBatchInfo{},
			fmt.Errorf(
				"backup checkpoint Slot installer: segment exceeds memory budget",
			)
	}
	if err := i.memoryBudget.Acquire(ctx, weight); err != nil {
		return stagedRestoreSegment{}, backupartifact.SegmentHeader{},
			backupartifact.SegmentBatchInfo{}, err
	}
	defer i.memoryBudget.Release(weight)
	body, header, err := i.options.Segments.LoadCopyWithHeader(
		ctx, repository, reference,
	)
	if err != nil {
		return stagedRestoreSegment{}, backupartifact.SegmentHeader{},
			backupartifact.SegmentBatchInfo{}, err
	}
	info, err := backupartifact.InspectSegmentBatch(body)
	if err != nil {
		return stagedRestoreSegment{}, backupartifact.SegmentHeader{},
			backupartifact.SegmentBatchInfo{}, err
	}
	file, err := os.CreateTemp(
		i.options.StagingDir, "checkpoint-segment-*.stage",
	)
	if err != nil {
		return stagedRestoreSegment{}, backupartifact.SegmentHeader{},
			backupartifact.SegmentBatchInfo{}, err
	}
	path := file.Name()
	if err := i.stagingQuota.reserveClaim(
		path, path, uint64(len(body)),
	); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return stagedRestoreSegment{}, backupartifact.SegmentHeader{},
			backupartifact.SegmentBatchInfo{}, err
	}
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(body)
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		_ = i.stagingQuota.settleClaim(path)
		return stagedRestoreSegment{}, backupartifact.SegmentHeader{},
			backupartifact.SegmentBatchInfo{}, errors.Join(err, closeErr)
	}
	if err := i.stagingQuota.settleClaim(path); err != nil {
		_ = os.Remove(path)
		_ = i.stagingQuota.refresh()
		return stagedRestoreSegment{}, backupartifact.SegmentHeader{},
			backupartifact.SegmentBatchInfo{}, err
	}
	return stagedRestoreSegment{
		stream: stream, path: path, bytes: int64(len(body)),
	}, header, info, nil
}

func (i *CheckpointSlotInstaller) replayStagedSegment(
	ctx context.Context,
	segment stagedRestoreSegment,
	sink *checkpointRestoreSink,
) error {
	weight, ok := checkpointRestoreMemoryWeight(segment.bytes, 3)
	if !ok || weight > i.memoryMax {
		return fmt.Errorf(
			"backup checkpoint Slot installer: staged segment exceeds memory budget",
		)
	}
	if err := i.memoryBudget.Acquire(ctx, weight); err != nil {
		return err
	}
	defer i.memoryBudget.Release(weight)
	file, err := os.Open(segment.path)
	if err != nil {
		return err
	}
	defer file.Close()
	recordVisitor := sink.Metadata
	if segment.stream == backupartifact.SegmentStreamMessages {
		recordVisitor = sink.Message
	}
	boundaryVisitor := (func(backupartifact.ChannelBoundary) error)(nil)
	if segment.stream == backupartifact.SegmentStreamMessages {
		boundaryVisitor = sink.Boundary
	}
	_, err = backupartifact.ReplaySegmentBatch(
		file, segment.bytes, recordVisitor, boundaryVisitor,
	)
	return err
}

func (i *CheckpointSlotInstaller) validateMessageCursorHead(
	ctx context.Context,
	repository backupartifact.Repository,
	checkpoint backupartifact.Checkpoint,
	slot backupartifact.CheckpointSlot,
) (uint64, error) {
	if slot.Messages.Sequence == 0 {
		return 0, nil
	}
	if slot.Messages.CursorHead == nil {
		return 0, backupartifact.ErrInvalidObject
	}
	weight, ok := checkpointRestoreMemoryWeight(
		slot.Messages.CursorHead.PlaintextBytes, 2,
	)
	if !ok || weight > i.memoryMax {
		return 0, fmt.Errorf(
			"backup checkpoint Slot installer: cursor exceeds memory budget",
		)
	}
	if err := i.memoryBudget.Acquire(ctx, weight); err != nil {
		return 0, err
	}
	defer i.memoryBudget.Release(weight)
	body, header, err := i.options.Segments.LoadCopyWithHeader(
		ctx, repository, *slot.Messages.CursorHead,
	)
	if err != nil {
		return 0, err
	}
	cursor, err := backupartifact.LoadMessageCursorBatch(body)
	if err != nil {
		return 0, err
	}
	if header.Logical.RepositoryID != checkpoint.RepositoryID ||
		header.Logical.SourceClusterID != checkpoint.SourceClusterID ||
		header.Logical.SourceGeneration != checkpoint.SourceGeneration ||
		header.Logical.Generation != slot.Generation ||
		header.Logical.HashSlot != slot.HashSlot ||
		header.Logical.Stream != backupartifact.SegmentStreamMessageCursor ||
		header.Logical.Sequence != slot.Messages.Sequence ||
		cursor.HashSlot != slot.HashSlot || cursor.Generation != slot.Generation ||
		cursor.Sequence != slot.Messages.Sequence ||
		cursor.SourceHighWatermark != slot.Messages.SourceHighWatermark ||
		cursor.WatermarkAtUnixMillis != slot.Messages.WatermarkAtUnixMillis {
		return 0, backupartifact.ErrObjectCorrupt
	}
	return uint64(len(body)), nil
}

func checkpointRestoreMemoryWeight(bytes int64, copies int64) (int64, bool) {
	if bytes <= 0 || copies <= 0 || bytes > int64(^uint64(0)>>1)/copies {
		return 0, false
	}
	return bytes * copies, true
}

var _ RestorePartitionInstaller = (*CheckpointSlotInstaller)(nil)
