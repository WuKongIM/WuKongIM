package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"golang.org/x/sync/errgroup"
)

const maxCheckpointProofParallel = 16

// CheckpointOptions configures complete Slot vector-cut publication.
type CheckpointOptions struct {
	// Enabled allows checkpoint publication when true.
	Enabled bool
	// HashSlotCount is the exact configured logical partition count.
	HashSlotCount uint16
	// RepositoryID identifies the logical dual repository.
	RepositoryID string
	// SourceClusterID and SourceGeneration fence the captured source.
	SourceClusterID  string
	SourceGeneration string
	// Store supplies durable Slot frontiers and stores only the catalog head.
	Store StateStore
	// Catalog publishes immutable checkpoint and catalog objects to both repositories.
	Catalog CheckpointCatalogPublisher
	// Proofs authenticates only the current frontier commit proofs.
	Proofs SegmentCommitVerifier
	// CaptureStatus optionally supplies a complete cluster-wide health
	// projection. Durable SlotFrontiers remain publication authority.
	CaptureStatus SlotCaptureStatusSource
	// Now and NewCheckpointID provide deterministic publication identity.
	Now             func() time.Time
	NewCheckpointID func() string
}

// CheckpointCoordinator publishes complete vector cuts without pausing source writes.
type CheckpointCoordinator struct {
	enabled          bool
	hashSlotCount    uint16
	repositoryID     string
	sourceClusterID  string
	sourceGeneration string
	store            StateStore
	catalog          CheckpointCatalogPublisher
	proofs           SegmentCommitVerifier
	captureStatus    SlotCaptureStatusSource
	now              func() time.Time
	newCheckpointID  func() string
}

// PublishCheckpoint exposes the single explicit vNext backup mutation.
func (a *App) PublishCheckpoint(
	ctx context.Context,
) (CheckpointPublication, error) {
	if a == nil || !a.enabled {
		return CheckpointPublication{}, ErrDisabled
	}
	if a.checkpoints == nil {
		return CheckpointPublication{}, ErrInvalidRequest
	}
	commit, err := a.checkpoints.Publish(ctx)
	if err != nil {
		return CheckpointPublication{}, err
	}
	catalogHeadToken, err := EncodeCatalogHeadToken(commit.Head)
	if err != nil {
		return CheckpointPublication{}, err
	}
	return CheckpointPublication{
		Checkpoint: CheckpointSummary{
			ID:                    commit.Checkpoint.ID,
			CreatedAtUnixMillis:   commit.Checkpoint.CreatedAtUnixMillis,
			EffectiveAtUnixMillis: commit.Checkpoint.EffectiveAtUnixMillis,
		},
		CheckpointSHA256: commit.Checkpoint.SHA256,
		CatalogHeadToken: catalogHeadToken,
	}, nil
}

// NewCheckpointCoordinator creates a complete-vector publication coordinator.
func NewCheckpointCoordinator(options CheckpointOptions) (*CheckpointCoordinator, error) {
	if options.HashSlotCount == 0 || strings.TrimSpace(options.RepositoryID) == "" ||
		strings.TrimSpace(options.SourceClusterID) == "" ||
		strings.TrimSpace(options.SourceGeneration) == "" || options.Store == nil ||
		options.Catalog == nil || options.Proofs == nil || options.Now == nil ||
		options.NewCheckpointID == nil {
		return nil, fmt.Errorf("%w: checkpoint dependencies are incomplete", ErrInvalidRequest)
	}
	return &CheckpointCoordinator{
		enabled: options.Enabled, hashSlotCount: options.HashSlotCount,
		repositoryID:     strings.TrimSpace(options.RepositoryID),
		sourceClusterID:  strings.TrimSpace(options.SourceClusterID),
		sourceGeneration: strings.TrimSpace(options.SourceGeneration),
		store:            options.Store, catalog: options.Catalog, proofs: options.Proofs,
		captureStatus: options.CaptureStatus,
		now:           options.Now, newCheckpointID: options.NewCheckpointID,
	}, nil
}

// Publish freezes the latest durable healthy frontier for every configured Slot,
// dual-commits the new artifacts, and only then advances the Controller head.
func (c *CheckpointCoordinator) Publish(ctx context.Context) (backupartifact.CheckpointCatalogCommit, error) {
	if !c.enabled {
		return backupartifact.CheckpointCatalogCommit{}, ErrDisabled
	}
	state, err := c.store.Load(ctx)
	if err != nil {
		return backupartifact.CheckpointCatalogCommit{}, err
	}
	if frozen := backupcontract.FrozenAuditHashSlots(state.IntegrityAudit); len(frozen) != 0 {
		return backupartifact.CheckpointCatalogCommit{}, fmt.Errorf(
			"%w: Slot %d integrity audit is unhealthy",
			ErrCheckpointUnhealthy, frozen[0],
		)
	}
	var frontiers []backupcontract.SlotFrontier
	if c.captureStatus != nil {
		statuses := c.captureStatus.Status()
		frontiers, err = checkpointFrontiersFromHealthyStatuses(
			statuses, c.hashSlotCount,
		)
		if err != nil {
			return backupartifact.CheckpointCatalogCommit{}, err
		}
	} else {
		frontiers, err = checkpointFrontiersFromDurableState(
			state.SlotFrontiers, c.hashSlotCount,
		)
		if err != nil {
			return backupartifact.CheckpointCatalogCommit{}, err
		}
	}
	checkpoint, err := c.buildCheckpoint(frontiers, erasureHeadsFromStreams(state.ErasureStreams))
	if err != nil {
		return backupartifact.CheckpointCatalogCommit{}, err
	}
	if err := c.verifyCurrentProofs(ctx, checkpoint); err != nil {
		return backupartifact.CheckpointCatalogCommit{}, err
	}
	commit, err := c.catalog.Publish(ctx, checkpoint, state.CatalogHead)
	if err != nil {
		return backupartifact.CheckpointCatalogCommit{}, err
	}
	if err := c.advanceHead(ctx, state.CatalogHead, commit.Head); err != nil {
		return backupartifact.CheckpointCatalogCommit{}, err
	}
	return commit, nil
}

func (c *CheckpointCoordinator) verifyCurrentProofs(ctx context.Context, checkpoint backupartifact.Checkpoint) error {
	seen := make(map[string]struct{}, len(checkpoint.Slots)*3)
	type proof struct {
		hashSlot   uint16
		generation string
		stream     backupartifact.SegmentStream
		sequence   uint64
		reference  backupartifact.SegmentReference
	}
	proofs := make([]proof, 0, len(checkpoint.Slots)*3)
	for _, slot := range checkpoint.Slots {
		if slot.Baseline != nil {
			reference := slot.Baseline.MessageCursor
			if _, exists := seen[reference.CommitKey]; exists {
				return fmt.Errorf("%w: duplicate checkpoint commit proof %q", ErrInvalidRequest, reference.CommitKey)
			}
			seen[reference.CommitKey] = struct{}{}
			proofs = append(proofs, proof{
				hashSlot: slot.HashSlot, generation: slot.Generation,
				stream:   backupartifact.SegmentStreamMessageBaselineCursor,
				sequence: 1, reference: reference,
			})
		}
		for _, candidate := range []struct {
			stream    backupartifact.SegmentStream
			sequence  uint64
			reference *backupartifact.SegmentReference
		}{
			{backupartifact.SegmentStreamMetadata, slot.Metadata.Sequence, slot.Metadata.Head},
			{backupartifact.SegmentStreamMessages, slot.Messages.Sequence, slot.Messages.Head},
			{backupartifact.SegmentStreamMessageCursor, slot.Messages.Sequence, slot.Messages.CursorHead},
		} {
			reference := candidate.reference
			if reference == nil {
				continue
			}
			if _, exists := seen[reference.CommitKey]; exists {
				return fmt.Errorf("%w: duplicate checkpoint commit proof %q", ErrInvalidRequest, reference.CommitKey)
			}
			seen[reference.CommitKey] = struct{}{}
			proofs = append(proofs, proof{
				hashSlot: slot.HashSlot, generation: slot.Generation,
				stream: candidate.stream, sequence: candidate.sequence, reference: *reference,
			})
		}
	}
	group, verifyContext := errgroup.WithContext(ctx)
	group.SetLimit(maxCheckpointProofParallel)
	for _, item := range proofs {
		item := item
		group.Go(func() error {
			header, err := c.proofs.VerifyCommit(verifyContext, item.reference)
			if err != nil {
				return fmt.Errorf("backup checkpoint: verify Slot %d commit proof: %w", item.hashSlot, err)
			}
			logical := header.Logical
			if logical.RepositoryID != checkpoint.RepositoryID ||
				logical.SourceClusterID != checkpoint.SourceClusterID ||
				logical.SourceGeneration != checkpoint.SourceGeneration ||
				logical.Generation != item.generation ||
				logical.HashSlot != item.hashSlot ||
				logical.Stream != item.stream ||
				logical.Sequence != item.sequence {
				return fmt.Errorf("%w: Slot %d commit proof identity mismatch", ErrInvalidRequest, item.hashSlot)
			}
			return nil
		})
	}
	return group.Wait()
}

func (c *CheckpointCoordinator) buildCheckpoint(frontiers []backupcontract.SlotFrontier, erasureHeads []backupartifact.ErasureStreamHead) (backupartifact.Checkpoint, error) {
	if len(frontiers) != int(c.hashSlotCount) {
		return backupartifact.Checkpoint{}, ErrPartitionsIncomplete
	}
	createdAt := c.now().UTC().UnixMilli()
	checkpoint := backupartifact.Checkpoint{
		Format: backupartifact.CheckpointFormat, Version: backupartifact.CheckpointVersion,
		ID: strings.TrimSpace(c.newCheckpointID()), RepositoryID: c.repositoryID,
		SourceClusterID:  c.sourceClusterID,
		SourceGeneration: c.sourceGeneration, HashSlotCount: c.hashSlotCount,
		CreatedAtUnixMillis: createdAt, Slots: make([]backupartifact.CheckpointSlot, c.hashSlotCount),
		ErasureHeads: erasureHeads,
	}
	for index, frontier := range frontiers {
		if frontier.HashSlot != uint16(index) || frontier.Revision == 0 {
			return backupartifact.Checkpoint{}, ErrPartitionsIncomplete
		}
		slot := backupartifact.CheckpointSlot{
			HashSlot: frontier.HashSlot, Generation: frontier.Generation,
			Baseline:              checkpointBaseline(frontier.Baseline, frontier.Messages.BaselineCursorHead),
			Metadata:              checkpointStream(frontier.Metadata),
			Messages:              checkpointStream(frontier.Messages),
			WatermarkAtUnixMillis: frontier.WatermarkAtUnixMillis,
		}
		checkpoint.Slots[index] = slot
		if checkpoint.EffectiveAtUnixMillis == 0 ||
			slot.WatermarkAtUnixMillis < checkpoint.EffectiveAtUnixMillis {
			checkpoint.EffectiveAtUnixMillis = slot.WatermarkAtUnixMillis
		}
	}
	return checkpoint, nil
}

func erasureHeadsFromStreams(streams []backupcontract.ErasureStreamState) []backupartifact.ErasureStreamHead {
	heads := make([]backupartifact.ErasureStreamHead, 0, len(streams))
	for _, stream := range streams {
		if stream.Head != nil {
			heads = append(heads, *stream.Head)
		}
	}
	return heads
}

func checkpointBaseline(reference *backupcontract.SlotBaselineReference, cursor *backupartifact.SegmentReference) *backupartifact.CheckpointBaseline {
	if reference == nil || cursor == nil {
		return nil
	}
	return &backupartifact.CheckpointBaseline{
		Partition: reference.Partition, MessageCursor: *cursor,
	}
}

func checkpointStream(frontier backupcontract.StreamFrontier) backupartifact.CheckpointStream {
	return backupartifact.CheckpointStream{
		Sequence: frontier.Sequence, Head: cloneCheckpointSegment(frontier.Head),
		CursorHead:            cloneCheckpointSegment(frontier.CursorHead),
		SourceHighWatermark:   frontier.SourceHighWatermark,
		WatermarkAtUnixMillis: frontier.WatermarkAtUnixMillis,
	}
}

func cloneCheckpointSegment(reference *backupartifact.SegmentReference) *backupartifact.SegmentReference {
	if reference == nil {
		return nil
	}
	out := *reference
	return &out
}

func checkpointFrontiersFromHealthyStatuses(
	statuses []backupcontract.SlotCaptureStatus,
	hashSlotCount uint16,
) ([]backupcontract.SlotFrontier, error) {
	if len(statuses) != int(hashSlotCount) {
		return nil, ErrPartitionsIncomplete
	}
	frontiers := make([]backupcontract.SlotFrontier, hashSlotCount)
	for index, status := range statuses {
		if status.HashSlot != uint16(index) || status.ObservedAtUnixMillis <= 0 ||
			status.Frontier.HashSlot != status.HashSlot || status.Frontier.Revision == 0 ||
			status.Frontier.Metadata.WatermarkAtUnixMillis <= 0 ||
			status.Frontier.Messages.WatermarkAtUnixMillis <= 0 ||
			status.Frontier.WatermarkAtUnixMillis <= 0 {
			return nil, ErrPartitionsIncomplete
		}
		if status.Frontier.Rebase != nil {
			return nil, fmt.Errorf("%w: Slot %d capture is rebasing", ErrCheckpointUnhealthy, status.HashSlot)
		}
		switch status.State {
		case backupcontract.CaptureStateIdle, backupcontract.CaptureStateReconciling, backupcontract.CaptureStateCapturing:
		default:
			return nil, fmt.Errorf("%w: Slot %d capture is %s", ErrCheckpointUnhealthy, status.HashSlot, status.State)
		}
		frontiers[index] = backupcontract.CloneSlotFrontier(status.Frontier)
	}
	return frontiers, nil
}

func checkpointFrontiersFromDurableState(
	frontiers []backupcontract.SlotFrontier,
	hashSlotCount uint16,
) ([]backupcontract.SlotFrontier, error) {
	if len(frontiers) != int(hashSlotCount) {
		return nil, ErrPartitionsIncomplete
	}
	result := make([]backupcontract.SlotFrontier, hashSlotCount)
	for index, frontier := range frontiers {
		if frontier.HashSlot != uint16(index) || frontier.Revision == 0 ||
			frontier.Lease.Sequence == 0 ||
			frontier.Generation == "" ||
			frontier.Rebase != nil ||
			frontier.Metadata.WatermarkAtUnixMillis <= 0 ||
			frontier.Messages.WatermarkAtUnixMillis <= 0 ||
			frontier.WatermarkAtUnixMillis <= 0 {
			if frontier.Rebase != nil {
				return nil, fmt.Errorf(
					"%w: Slot %d capture is rebasing",
					ErrCheckpointUnhealthy, frontier.HashSlot,
				)
			}
			return nil, ErrPartitionsIncomplete
		}
		result[index] = backupcontract.CloneSlotFrontier(frontier)
	}
	return result, nil
}

func (c *CheckpointCoordinator) advanceHead(
	ctx context.Context,
	previous *backupartifact.CatalogPageReference,
	head backupartifact.CatalogPageReference,
) error {
	for attempt := 0; attempt < maxStateRetries; attempt++ {
		state, err := c.store.Load(ctx)
		if err != nil {
			return err
		}
		if catalogPageReferenceEqual(state.CatalogHead, &head) {
			return nil
		}
		if !catalogPageReferenceEqual(state.CatalogHead, previous) {
			return ErrStateConflict
		}
		if frozen := backupcontract.FrozenAuditHashSlots(state.IntegrityAudit); len(frozen) != 0 {
			return fmt.Errorf(
				"%w: Slot %d integrity audit became unhealthy before head advance",
				ErrCheckpointUnhealthy, frozen[0],
			)
		}
		next := state.Clone()
		next.CatalogHead = cloneCatalogPageHead(&head)
		if next.CatalogAuditRootSequence == 0 {
			next.CatalogAuditRootSequence = head.Sequence
		}
		if next.CatalogRetentionRevision == 0 {
			next.CatalogRetentionRevision = 1
		}
		if err := c.store.CompareAndSwap(ctx, state.Revision, next); err != nil {
			if errors.Is(err, ErrStateConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrStateConflict
}

func catalogPageReferenceEqual(left, right *backupartifact.CatalogPageReference) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneCatalogPageHead(reference *backupartifact.CatalogPageReference) *backupartifact.CatalogPageReference {
	if reference == nil {
		return nil
	}
	out := *reference
	return &out
}
