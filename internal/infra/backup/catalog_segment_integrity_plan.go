package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	catalogSegmentAuditPositionVersion      uint16 = 5
	catalogSegmentAuditStreamCount                 = 6
	catalogSegmentAuditModeSelect                  = "select"
	catalogSegmentAuditModeFindPrior               = "find_prior"
	catalogSegmentAuditModeSegments                = "segments"
	catalogSegmentAuditNavigationGeneration        = "catalog-navigation"
	catalogSegmentAuditErasureGeneration           = "erasure-ledger"
	catalogSegmentAuditCacheEntries                = 4
	catalogSegmentAuditMaxPartitionDepth           = 10_000
	defaultCatalogSegmentAuditScrubInterval        = 24 * time.Hour
)

// IntegrityAuditCatalog loads authenticated immutable catalog decisions.
type IntegrityAuditCatalog interface {
	LoadPageForIntegrityAudit(
		context.Context,
		backupartifact.CatalogPageReference,
	) (backupartifact.CatalogPage, error)
	LoadCheckpointForIntegrityAudit(
		context.Context,
		backupartifact.CatalogCheckpointReference,
	) (backupartifact.Checkpoint, error)
}

// IntegrityAuditCatalogWindow fixes both the latest visible head and the
// oldest page whose Generation graph remains protected by retention.
type IntegrityAuditCatalogWindow struct {
	Head                 *backupartifact.CatalogPageReference
	RetainedRootSequence uint64
}

// IntegrityAuditCatalogWindowSource returns one durable retention/catalog cut.
type IntegrityAuditCatalogWindowSource interface {
	LoadIntegrityAuditCatalogWindow(
		context.Context,
	) (IntegrityAuditCatalogWindow, error)
}

// CoordinationIntegrityAuditCatalogWindowSource reads the Controller's atomic
// catalog head and durable retained root.
type CoordinationIntegrityAuditCatalogWindowSource struct {
	state CoordinationStateStore
}

// NewCoordinationIntegrityAuditCatalogWindowSource creates a narrow fixed-cut reader.
func NewCoordinationIntegrityAuditCatalogWindowSource(
	state CoordinationStateStore,
) (*CoordinationIntegrityAuditCatalogWindowSource, error) {
	if state == nil {
		return nil, fmt.Errorf(
			"backup catalog audit window source: state is required",
		)
	}
	return &CoordinationIntegrityAuditCatalogWindowSource{
		state: state,
	}, nil
}

// LoadIntegrityAuditCatalogWindow returns one detached retention/catalog cut.
func (s *CoordinationIntegrityAuditCatalogWindowSource) LoadIntegrityAuditCatalogWindow(
	ctx context.Context,
) (IntegrityAuditCatalogWindow, error) {
	state, err := s.state.Load(ctx)
	if err != nil || state.CatalogHead == nil {
		return IntegrityAuditCatalogWindow{}, err
	}
	head := *state.CatalogHead
	return IntegrityAuditCatalogWindow{
		Head:                 &head,
		RetainedRootSequence: state.CatalogAuditRootSequence,
	}, nil
}

// CatalogSegmentIntegrityAuditPlan walks catalog deltas during an epoch and
// periodically scrubs the retained immutable graph for latent damage.
type CatalogSegmentIntegrityAuditPlan struct {
	window        IntegrityAuditCatalogWindowSource
	selection     IntegrityAuditRetentionSelectionSource
	catalog       IntegrityAuditCatalog
	hashSlotCount uint16
	scrubInterval time.Duration
	now           func() time.Time

	cacheMu         sync.Mutex
	pageCache       []catalogSegmentAuditPageCacheEntry
	checkpointCache []catalogSegmentAuditCheckpointCacheEntry
	selectionID     string
	retainedIDs     map[string]struct{}
}

type catalogSegmentAuditPageCacheEntry struct {
	reference backupartifact.CatalogPageReference
	page      backupartifact.CatalogPage
}

type catalogSegmentAuditCheckpointCacheEntry struct {
	reference  backupartifact.CatalogCheckpointReference
	checkpoint backupartifact.Checkpoint
}

// CatalogSegmentIntegrityAuditPlanOptions configures delta and periodic scrub cycles.
type CatalogSegmentIntegrityAuditPlanOptions struct {
	Window        IntegrityAuditCatalogWindowSource
	Selection     IntegrityAuditRetentionSelectionSource
	Catalog       IntegrityAuditCatalog
	HashSlotCount uint16
	ScrubInterval time.Duration
	Now           func() time.Time
}

// NewCatalogSegmentIntegrityAuditPlan creates a crash-resumable catalog plan.
func NewCatalogSegmentIntegrityAuditPlan(
	options CatalogSegmentIntegrityAuditPlanOptions,
) (*CatalogSegmentIntegrityAuditPlan, error) {
	if options.Window == nil || options.Selection == nil ||
		options.Catalog == nil ||
		options.HashSlotCount == 0 {
		return nil, fmt.Errorf("backup catalog segment audit plan: dependencies are invalid")
	}
	if options.ScrubInterval == 0 {
		options.ScrubInterval = defaultCatalogSegmentAuditScrubInterval
	}
	if options.ScrubInterval < time.Minute {
		return nil, fmt.Errorf("backup catalog segment audit plan: scrub interval is invalid")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &CatalogSegmentIntegrityAuditPlan{
		window: options.Window, selection: options.Selection,
		catalog:       options.Catalog,
		hashSlotCount: options.HashSlotCount,
		scrubInterval: options.ScrubInterval, now: options.Now,
	}, nil
}

// Start fixes the current catalog head and seeks the first newly introduced segment.
func (p *CatalogSegmentIntegrityAuditPlan) Start(
	ctx context.Context,
	previous *backupcontract.IntegrityAuditCursor,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	now := p.now().UTC()
	window, err := p.window.LoadIntegrityAuditCatalogWindow(ctx)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, 0, err
	}
	head := window.Head
	if head == nil {
		if window.RetainedRootSequence != 0 {
			return backupcontract.IntegrityAuditCursor{}, 0, fmt.Errorf(
				"%w: empty catalog has a retained audit root",
				backupartifact.ErrObjectCorrupt,
			)
		}
	} else if window.RetainedRootSequence == 0 ||
		window.RetainedRootSequence > head.Sequence {
		return backupcontract.IntegrityAuditCursor{}, 0, fmt.Errorf(
			"%w: retained catalog audit root is invalid",
			backupartifact.ErrObjectCorrupt,
		)
	}
	scrubEpoch := uint64(
		now.UnixMilli()/p.scrubInterval.Milliseconds(),
	) + 1
	if previous != nil && previous.ScrubEpoch > scrubEpoch {
		scrubEpoch = previous.ScrubEpoch
	}
	if previous != nil && head != nil &&
		head.Sequence < previous.CatalogSequence {
		return backupcontract.IntegrityAuditCursor{}, 0, fmt.Errorf(
			"%w: catalog audit head regressed",
			backupartifact.ErrObjectCorrupt,
		)
	}
	lowerSequence := uint64(0)
	if window.RetainedRootSequence > 0 {
		lowerSequence = window.RetainedRootSequence - 1
	}
	if previous != nil && previous.ScrubEpoch == scrubEpoch {
		lowerSequence = max(lowerSequence, previous.CatalogSequence)
	}
	if head == nil || head.Sequence <= lowerSequence {
		if previous != nil &&
			previous.Phase == backupcontract.IntegrityAuditPhaseComplete &&
			previous.ScrubEpoch == scrubEpoch {
			return *previous, 0, nil
		}
		sequence := lowerSequence
		if head != nil {
			sequence = head.Sequence
		}
		return completeCatalogSegmentAuditCursor(
			sequence, window.RetainedRootSequence, scrubEpoch,
		), 0, nil
	}
	p.resetCaches()
	selectionAt, err := catalogSegmentAuditSelectionTime(
		scrubEpoch, p.scrubInterval,
	)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, 0, err
	}
	selection, err := p.selection.LoadIntegrityAuditRetentionSelection(
		ctx,
		IntegrityAuditRetentionSelectionRequest{
			Head: *head, At: selectionAt,
		},
	)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, 0, err
	}
	if err := p.installRetentionSelection(
		*head, selectionAt, selection,
	); err != nil {
		return backupcontract.IntegrityAuditCursor{}, 0, err
	}
	position := catalogSegmentAuditPosition{
		Version:                   catalogSegmentAuditPositionVersion,
		ScrubEpoch:                scrubEpoch,
		SelectionAtUnixMillis:     selectionAt.UnixMilli(),
		SelectionID:               selection.ID,
		ActiveRestoreCheckpointID: selection.ActiveRestoreCheckpointID,
		Mode:                      catalogSegmentAuditModeSelect,
		LowerSequence:             lowerSequence,
		UpperSequence:             head.Sequence,
		RetainedRootSequence:      window.RetainedRootSequence,
		Head:                      *head,
		Page:                      *head,
		DebtObjects: catalogSegmentAuditDebtUpperBound(
			head.Sequence-lowerSequence, p.hashSlotCount,
		),
	}
	cursor, found, err := p.seek(ctx, position, 0, 0)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, 0, err
	}
	if !found {
		return completeCatalogSegmentAuditCursor(
			head.Sequence, window.RetainedRootSequence, scrubEpoch,
		), 0, nil
	}
	return cursor, position.DebtObjects, nil
}

func (p *CatalogSegmentIntegrityAuditPlan) resetCaches() {
	p.cacheMu.Lock()
	p.pageCache = nil
	p.checkpointCache = nil
	p.selectionID = ""
	p.retainedIDs = nil
	p.cacheMu.Unlock()
}

// Resolve returns the exact segment encoded by the durable opaque cursor.
func (p *CatalogSegmentIntegrityAuditPlan) Resolve(
	_ context.Context,
	cursor backupcontract.IntegrityAuditCursor,
) (SegmentIntegrityAuditTarget, error) {
	position, err := decodeCatalogSegmentAuditPosition(cursor.Position)
	if err != nil {
		return SegmentIntegrityAuditTarget{}, fmt.Errorf(
			"%w: catalog segment audit cursor is invalid",
			backupartifact.ErrInvalidObject,
		)
	}
	if catalogSegmentAuditAdministrative(position) {
		return SegmentIntegrityAuditTarget{
			Administrative: true, DebtObjects: position.DebtObjects,
		}, nil
	}
	if position.Page.Sequence > cursor.CatalogSequence {
		return SegmentIntegrityAuditTarget{}, fmt.Errorf(
			"%w: catalog segment audit cursor is invalid",
			backupartifact.ErrInvalidObject,
		)
	}
	return SegmentIntegrityAuditTarget{
		Kind:                 IntegrityAuditArtifactKind(position.ArtifactKind),
		Reference:            position.Reference,
		Partition:            position.Partition,
		PartitionObjectIndex: position.PartitionObjectIndex,
		Erasure: ErasureIntegrityAuditTarget{
			Kind:     ErasureIntegrityArtifactKind(position.ErasureArtifact),
			HashSlot: position.HashSlot, Sequence: position.ErasureSequence,
			CommitKey:            position.ErasureCommitKey,
			ExpectedCommitSHA256: position.ErasureCommitSHA256,
			EventID:              position.ErasureEventID,
			RecordKey:            position.ErasureRecordKey,
			RecordSHA256:         position.ErasureRecordSHA256,
		},
		DebtObjects: position.DebtObjects,
	}, nil
}

// LoadIntegrityAuditRetainedCheckpoints rebuilds the exact sparse set fixed by
// an unfinished cursor so Generation GC can union it into its mark phase.
func (p *CatalogSegmentIntegrityAuditPlan) LoadIntegrityAuditRetainedCheckpoints(
	ctx context.Context,
	cursor backupcontract.IntegrityAuditCursor,
) ([]backupartifact.CatalogCheckpointReference, error) {
	if cursor.Phase == backupcontract.IntegrityAuditPhaseComplete ||
		!strings.HasPrefix(cursor.CycleID, "catalog-segments-") {
		return nil, nil
	}
	position, err := decodeCatalogSegmentAuditPosition(cursor.Position)
	if err != nil {
		return nil, err
	}
	expected, err := p.cursor(position)
	if err != nil ||
		expected.CycleID != cursor.CycleID ||
		expected.ScrubEpoch != cursor.ScrubEpoch ||
		expected.CatalogSequence != cursor.CatalogSequence ||
		expected.CatalogRootSequence != cursor.CatalogRootSequence {
		return nil, backupartifact.ErrObjectCorrupt
	}
	activeRestoreCheckpointID := position.ActiveRestoreCheckpointID
	selection, err := p.selection.LoadIntegrityAuditRetentionSelection(
		ctx,
		IntegrityAuditRetentionSelectionRequest{
			Head: position.Head,
			At: time.UnixMilli(
				position.SelectionAtUnixMillis,
			).UTC(),
			ActiveRestoreCheckpointID: &activeRestoreCheckpointID,
		},
	)
	if err != nil {
		return nil, err
	}
	if selection.ID != position.SelectionID ||
		selection.ActiveRestoreCheckpointID !=
			position.ActiveRestoreCheckpointID ||
		len(selection.Checkpoints) == 0 {
		return nil, backupartifact.ErrObjectCorrupt
	}
	return append(
		[]backupartifact.CatalogCheckpointReference(nil),
		selection.Checkpoints...,
	), nil
}

// Advance uses the authenticated plaintext predecessor, then seeks the next
// stream, Slot, or older catalog transition without rescanning completed history.
func (p *CatalogSegmentIntegrityAuditPlan) Advance(
	ctx context.Context,
	cursor backupcontract.IntegrityAuditCursor,
	report IntegrityAuditArtifactReport,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	position, err := decodeCatalogSegmentAuditPosition(cursor.Position)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, 0, err
	}
	if position.DebtObjects > 0 {
		if !catalogSegmentAuditAdministrative(position) {
			position.DebtObjects--
		}
	}
	if catalogSegmentAuditAdministrative(position) {
		next, found, err := p.seek(ctx, position, 0, 0)
		if err != nil {
			return backupcontract.IntegrityAuditCursor{}, 0, err
		}
		if !found {
			complete := cursor
			complete.Position = "complete"
			complete.Phase = backupcontract.IntegrityAuditPhaseComplete
			complete.Repository = ""
			complete.Category = ""
			return complete, 0, nil
		}
		return next, position.DebtObjects, nil
	}
	if position.ArtifactKind == string(IntegrityAuditArtifactPartition) {
		next, found, err := p.advancePartitionArtifact(
			ctx, position, report.Partition,
		)
		if err != nil {
			return backupcontract.IntegrityAuditCursor{}, 0, err
		}
		if found {
			nextPosition, decodeErr := decodeCatalogSegmentAuditPosition(
				next.Position,
			)
			if decodeErr != nil {
				return backupcontract.IntegrityAuditCursor{}, 0, decodeErr
			}
			return next, nextPosition.DebtObjects, nil
		}
		return completeAuditPlanCursor(cursor), 0, nil
	}
	if position.ArtifactKind == string(IntegrityAuditArtifactErasure) {
		next, found, err := p.advanceErasureArtifact(
			ctx, position, report.Erasure,
		)
		if err != nil {
			return backupcontract.IntegrityAuditCursor{}, 0, err
		}
		if found {
			nextPosition, decodeErr := decodeCatalogSegmentAuditPosition(
				next.Position,
			)
			if decodeErr != nil {
				return backupcontract.IntegrityAuditCursor{}, 0, decodeErr
			}
			return next, nextPosition.DebtObjects, nil
		}
		return completeAuditPlanCursor(cursor), 0, nil
	}
	if report.Segment.Header != (backupartifact.SegmentHeader{}) {
		logical := report.Segment.Header.Logical
		if logical.HashSlot != cursor.HashSlot ||
			logical.Generation != cursor.Generation ||
			report.Segment.Header.PlaintextBytes != position.Reference.PlaintextBytes {
			return backupcontract.IntegrityAuditCursor{}, 0, fmt.Errorf(
				"%w: catalog segment audit identity mismatch",
				backupartifact.ErrObjectCorrupt,
			)
		}
		if !segmentAuditReferencesEqual(report.Segment.Previous, position.Stop) {
			if report.Segment.Previous == nil {
				return backupcontract.IntegrityAuditCursor{}, 0, fmt.Errorf(
					"%w: segment chain ended before audit boundary",
					backupartifact.ErrObjectCorrupt,
				)
			}
			position.Reference = *report.Segment.Previous
			next, err := p.cursor(position)
			return next, position.DebtObjects, err
		}
	}
	next, found, err := p.seek(
		ctx, position, int(position.HashSlot), int(position.Stream)+1,
	)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, 0, err
	}
	if !found {
		return completeAuditPlanCursor(cursor), 0, nil
	}
	return next, position.DebtObjects, nil
}

type catalogSegmentAuditPosition struct {
	Version                   uint16                                     `json:"version"`
	ScrubEpoch                uint64                                     `json:"scrub_epoch"`
	SelectionAtUnixMillis     int64                                      `json:"selection_at_unix_millis"`
	SelectionID               string                                     `json:"selection_id"`
	ActiveRestoreCheckpointID string                                     `json:"active_restore_checkpoint_id,omitempty"`
	Mode                      string                                     `json:"mode"`
	LowerSequence             uint64                                     `json:"lower_sequence"`
	UpperSequence             uint64                                     `json:"upper_sequence"`
	RetainedRootSequence      uint64                                     `json:"retained_root_sequence"`
	Head                      backupartifact.CatalogPageReference        `json:"head"`
	Page                      backupartifact.CatalogPageReference        `json:"page"`
	Current                   *backupartifact.CatalogCheckpointReference `json:"current,omitempty"`
	PriorPage                 *backupartifact.CatalogPageReference       `json:"prior_page,omitempty"`
	Prior                     *backupartifact.CatalogCheckpointReference `json:"prior,omitempty"`
	HashSlot                  uint16                                     `json:"hash_slot"`
	Generation                string                                     `json:"generation"`
	Stream                    uint8                                      `json:"stream"`
	ArtifactKind              string                                     `json:"artifact_kind,omitempty"`
	Reference                 backupartifact.SegmentReference            `json:"reference"`
	Stop                      *backupartifact.SegmentReference           `json:"stop,omitempty"`
	Partition                 backupartifact.PartitionReference          `json:"partition"`
	PartitionStop             *backupartifact.PartitionReference         `json:"partition_stop,omitempty"`
	PartitionObjectIndex      int                                        `json:"partition_object_index"`
	PartitionDepth            uint32                                     `json:"partition_depth"`
	ErasureArtifact           string                                     `json:"erasure_artifact,omitempty"`
	ErasureSequence           uint64                                     `json:"erasure_sequence,omitempty"`
	ErasureStopSequence       uint64                                     `json:"erasure_stop_sequence,omitempty"`
	ErasureStopSHA256         string                                     `json:"erasure_stop_sha256,omitempty"`
	ErasureCommitKey          string                                     `json:"erasure_commit_key,omitempty"`
	ErasureCommitSHA256       string                                     `json:"erasure_commit_sha256,omitempty"`
	ErasureEventID            string                                     `json:"erasure_event_id,omitempty"`
	ErasureRecordKey          string                                     `json:"erasure_record_key,omitempty"`
	ErasureRecordSHA256       string                                     `json:"erasure_record_sha256,omitempty"`
	ErasurePreviousSHA        string                                     `json:"erasure_previous_sha256,omitempty"`
	DebtObjects               uint64                                     `json:"debt_objects"`
}

func (p *CatalogSegmentIntegrityAuditPlan) seek(
	ctx context.Context,
	position catalogSegmentAuditPosition,
	startSlot int,
	startStream int,
) (backupcontract.IntegrityAuditCursor, bool, error) {
	retainedIDs, err := p.ensureRetentionSelection(ctx, position)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, false, err
	}
	switch position.Mode {
	case catalogSegmentAuditModeSelect:
		page, err := p.loadPageCached(ctx, position.Page)
		if err != nil {
			return backupcontract.IntegrityAuditCursor{}, false, err
		}
		if len(page.Entries) == 0 {
			return backupcontract.IntegrityAuditCursor{}, false, backupartifact.ErrObjectCorrupt
		}
		if page.Entries[0].StateOnly {
			return p.advanceCatalogAuditPage(position, page.Previous)
		}
		currentReference := page.Entries[0]
		if _, retained := retainedIDs[currentReference.ID]; !retained {
			return p.advanceCatalogAuditPage(position, page.Previous)
		}
		position.Current = &currentReference
		position.Prior = nil
		position.PriorPage = nil
		if page.Previous != nil &&
			page.Sequence > position.RetainedRootSequence {
			position.PriorPage = cloneAuditCatalogPageReference(page.Previous)
			position.Mode = catalogSegmentAuditModeFindPrior
			cursor, cursorErr := p.cursor(position)
			return cursor, true, cursorErr
		}
		position.Mode = catalogSegmentAuditModeSegments
		cursor, cursorErr := p.cursor(position)
		return cursor, true, cursorErr
	case catalogSegmentAuditModeFindPrior:
		if position.PriorPage == nil {
			position.PriorPage = nil
			position.Prior = nil
			position.Mode = catalogSegmentAuditModeSegments
			cursor, err := p.cursor(position)
			return cursor, true, err
		}
		page, err := p.loadPageCached(ctx, *position.PriorPage)
		if err != nil {
			return backupcontract.IntegrityAuditCursor{}, false, err
		}
		if len(page.Entries) == 0 {
			return backupcontract.IntegrityAuditCursor{}, false, backupartifact.ErrObjectCorrupt
		}
		if page.Entries[0].StateOnly {
			position.PriorPage = cloneAuditCatalogPageReference(page.Previous)
			if position.PriorPage == nil {
				position.PriorPage = nil
				position.Mode = catalogSegmentAuditModeSegments
			}
			cursor, cursorErr := p.cursor(position)
			return cursor, true, cursorErr
		}
		priorReference := page.Entries[0]
		if _, retained := retainedIDs[priorReference.ID]; !retained {
			position.PriorPage = cloneAuditCatalogPageReference(
				page.Previous,
			)
			if position.PriorPage == nil {
				position.Mode = catalogSegmentAuditModeSegments
			}
			cursor, cursorErr := p.cursor(position)
			return cursor, true, cursorErr
		}
		position.Prior = &priorReference
		// PriorPage deliberately names the data page that becomes the next
		// current transition after this checkpoint delta is consumed.
		position.PriorPage = cloneAuditCatalogPageReference(position.PriorPage)
		position.Mode = catalogSegmentAuditModeSegments
		cursor, cursorErr := p.cursor(position)
		return cursor, true, cursorErr
	case catalogSegmentAuditModeSegments:
		if position.Current == nil {
			return backupcontract.IntegrityAuditCursor{}, false, backupartifact.ErrObjectCorrupt
		}
		current, err := p.loadCheckpointCached(ctx, *position.Current)
		if err != nil {
			return backupcontract.IntegrityAuditCursor{}, false, err
		}
		if current.HashSlotCount != p.hashSlotCount ||
			len(current.Slots) != int(p.hashSlotCount) {
			return backupcontract.IntegrityAuditCursor{}, false, backupartifact.ErrObjectCorrupt
		}
		var prior *backupartifact.Checkpoint
		if position.Prior != nil {
			loaded, err := p.loadCheckpointCached(ctx, *position.Prior)
			if err != nil {
				return backupcontract.IntegrityAuditCursor{}, false, err
			}
			prior = &loaded
		}
		for slotIndex := startSlot; slotIndex < int(p.hashSlotCount); slotIndex++ {
			slot := current.Slots[slotIndex]
			if slot.HashSlot != uint16(slotIndex) {
				return backupcontract.IntegrityAuditCursor{}, false, backupartifact.ErrObjectCorrupt
			}
			streamStart := 0
			if slotIndex == startSlot {
				streamStart = startStream
			}
			for streamIndex := streamStart; streamIndex < catalogSegmentAuditStreamCount; streamIndex++ {
				if streamIndex == 4 {
					reference := catalogCheckpointPartitionReference(slot)
					if reference == nil {
						continue
					}
					var stop *backupartifact.PartitionReference
					if prior != nil && len(prior.Slots) == int(p.hashSlotCount) &&
						prior.Slots[slotIndex].HashSlot == uint16(slotIndex) &&
						prior.Slots[slotIndex].Generation == slot.Generation {
						stop = catalogCheckpointPartitionReference(
							prior.Slots[slotIndex],
						)
					}
					if partitionAuditReferencesEqual(reference, stop) {
						continue
					}
					position.HashSlot = uint16(slotIndex)
					position.Generation = slot.Generation
					position.Stream = uint8(streamIndex)
					position.ArtifactKind = string(IntegrityAuditArtifactPartition)
					position.Reference = backupartifact.SegmentReference{}
					position.Stop = nil
					position.Partition = *reference
					position.PartitionStop = cloneAuditPartitionReference(stop)
					position.PartitionObjectIndex = -1
					position.PartitionDepth = 0
					position.DebtObjects = saturatingAuditDebtAdd(
						position.DebtObjects, reference.ObjectCount,
					)
					cursor, err := p.cursor(position)
					return cursor, true, err
				}
				if streamIndex == 5 {
					head := catalogCheckpointErasureHead(
						current.ErasureHeads, uint16(slotIndex),
					)
					if head == nil {
						continue
					}
					stopSequence := uint64(0)
					stopSHA256 := ""
					if prior != nil {
						priorHead := catalogCheckpointErasureHead(
							prior.ErasureHeads, uint16(slotIndex),
						)
						if priorHead != nil {
							currentNamespace, _, _, parseErr :=
								backupartifact.ParseErasureLedgerCommitKey(
									head.CommitKey,
								)
							priorNamespace, _, _, priorParseErr :=
								backupartifact.ParseErasureLedgerCommitKey(
									priorHead.CommitKey,
								)
							if parseErr != nil || priorParseErr != nil {
								return backupcontract.IntegrityAuditCursor{}, false,
									backupartifact.ErrObjectCorrupt
							}
							if currentNamespace == priorNamespace {
								if priorHead.Sequence > head.Sequence {
									return backupcontract.IntegrityAuditCursor{}, false,
										backupartifact.ErrObjectCorrupt
								}
								stopSequence = priorHead.Sequence
								stopSHA256 = priorHead.CommitSHA256
							}
						}
					}
					if head.Sequence == stopSequence {
						if head.CommitSHA256 != stopSHA256 {
							return backupcontract.IntegrityAuditCursor{}, false,
								backupartifact.ErrObjectCorrupt
						}
						continue
					}
					position.HashSlot = uint16(slotIndex)
					position.Generation =
						catalogSegmentAuditErasureGeneration
					position.Stream = uint8(streamIndex)
					position.ArtifactKind =
						string(IntegrityAuditArtifactErasure)
					position.Reference = backupartifact.SegmentReference{}
					position.Stop = nil
					position.Partition = backupartifact.PartitionReference{}
					position.PartitionStop = nil
					position.PartitionObjectIndex = 0
					position.PartitionDepth = 0
					position.ErasureArtifact =
						string(ErasureIntegrityArtifactCommit)
					position.ErasureSequence = head.Sequence
					position.ErasureStopSequence = stopSequence
					position.ErasureStopSHA256 = stopSHA256
					position.ErasureCommitKey = head.CommitKey
					position.ErasureCommitSHA256 = head.CommitSHA256
					position.ErasureEventID = ""
					position.ErasureRecordKey = ""
					position.ErasureRecordSHA256 = ""
					position.ErasurePreviousSHA = ""
					position.DebtObjects = saturatingAuditDebtAdd(
						position.DebtObjects,
						saturatingAuditDebtMultiply(
							head.Sequence-stopSequence, 4,
						),
					)
					cursor, err := p.cursor(position)
					return cursor, true, err
				}
				reference := catalogCheckpointSegmentReference(slot, streamIndex)
				if reference == nil {
					continue
				}
				var stop *backupartifact.SegmentReference
				if prior != nil && len(prior.Slots) == int(p.hashSlotCount) &&
					prior.Slots[slotIndex].HashSlot == uint16(slotIndex) &&
					prior.Slots[slotIndex].Generation == slot.Generation {
					stop = catalogCheckpointSegmentReference(
						prior.Slots[slotIndex], streamIndex,
					)
				}
				if segmentAuditReferencesEqual(reference, stop) {
					continue
				}
				position.HashSlot = uint16(slotIndex)
				position.Generation = slot.Generation
				position.Stream = uint8(streamIndex)
				position.ArtifactKind = string(IntegrityAuditArtifactSegment)
				position.Reference = *reference
				position.Stop = cloneAuditSegmentReference(stop)
				position.Partition = backupartifact.PartitionReference{}
				position.PartitionStop = nil
				position.PartitionObjectIndex = 0
				position.PartitionDepth = 0
				cursor, err := p.cursor(position)
				return cursor, true, err
			}
		}
		if position.PriorPage == nil ||
			position.PriorPage.Sequence <= position.LowerSequence {
			return backupcontract.IntegrityAuditCursor{}, false, nil
		}
		position.Page = *position.PriorPage
		position.Mode = catalogSegmentAuditModeSelect
		position.Current = nil
		position.PriorPage = nil
		position.Prior = nil
		position.Generation = catalogSegmentAuditNavigationGeneration
		position.ArtifactKind = ""
		position.Reference = backupartifact.SegmentReference{}
		position.Stop = nil
		position.Partition = backupartifact.PartitionReference{}
		position.PartitionStop = nil
		position.PartitionObjectIndex = 0
		position.PartitionDepth = 0
		cursor, err := p.cursor(position)
		return cursor, true, err
	default:
		return backupcontract.IntegrityAuditCursor{}, false, backupartifact.ErrObjectCorrupt
	}
}

func (p *CatalogSegmentIntegrityAuditPlan) advancePartitionArtifact(
	ctx context.Context,
	position catalogSegmentAuditPosition,
	report backupartifact.PartitionArtifactAuditReport,
) (backupcontract.IntegrityAuditCursor, bool, error) {
	navigation := report.Navigation
	if navigation.Format == "" {
		next, found, err := p.seek(
			ctx, position, int(position.HashSlot), int(position.Stream)+1,
		)
		if err != nil {
			return backupcontract.IntegrityAuditCursor{}, false, err
		}
		return next, found, nil
	}
	if navigation.Format != backupartifact.PartitionManifestFormat ||
		navigation.HashSlot != position.HashSlot ||
		navigation.ObjectCount != position.Partition.ObjectCount {
		return backupcontract.IntegrityAuditCursor{}, false, fmt.Errorf(
			"%w: partition audit identity mismatch",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if position.PartitionObjectIndex < 0 {
		position.PartitionObjectIndex = 0
		cursor, err := p.cursor(position)
		return cursor, true, err
	}
	if uint64(position.PartitionObjectIndex) >= navigation.ObjectCount {
		return backupcontract.IntegrityAuditCursor{}, false, fmt.Errorf(
			"%w: partition audit object index is invalid",
			backupartifact.ErrObjectCorrupt,
		)
	}
	position.PartitionObjectIndex++
	if uint64(position.PartitionObjectIndex) < navigation.ObjectCount {
		cursor, err := p.cursor(position)
		return cursor, true, err
	}
	next, found, err := p.seek(
		ctx, position, int(position.HashSlot), int(position.Stream)+1,
	)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, false, err
	}
	return next, found, nil
}

func (p *CatalogSegmentIntegrityAuditPlan) advanceErasureArtifact(
	ctx context.Context,
	position catalogSegmentAuditPosition,
	report ErasureIntegrityAuditReport,
) (backupcontract.IntegrityAuditCursor, bool, error) {
	switch ErasureIntegrityArtifactKind(position.ErasureArtifact) {
	case ErasureIntegrityArtifactCommit:
		commit := report.Commit
		if commit == (backupartifact.ErasureLedgerCommit{}) {
			return p.advancePastErasureStream(ctx, position)
		}
		if commit.HashSlot != position.HashSlot ||
			commit.Sequence != position.ErasureSequence ||
			commit.EventID == "" || commit.RecordKey == "" ||
			commit.RecordSHA256 == "" {
			return backupcontract.IntegrityAuditCursor{}, false, fmt.Errorf(
				"%w: erasure commit escapes audit cursor",
				backupartifact.ErrObjectCorrupt,
			)
		}
		position.ErasureEventID = commit.EventID
		position.ErasureRecordKey = commit.RecordKey
		position.ErasureRecordSHA256 = commit.RecordSHA256
		position.ErasurePreviousSHA = commit.PreviousCommitSHA256
		position.ErasureArtifact =
			string(ErasureIntegrityArtifactReceipt)
	case ErasureIntegrityArtifactReceipt:
		position.ErasureArtifact =
			string(ErasureIntegrityArtifactRecord)
	case ErasureIntegrityArtifactRecord:
		record := report.Record
		if record.EventID == "" {
			return p.advanceErasureSequence(ctx, position)
		}
		if record.HashSlot != position.HashSlot ||
			record.EventID != position.ErasureEventID ||
			record.Object.Key == "" {
			return backupcontract.IntegrityAuditCursor{}, false, fmt.Errorf(
				"%w: erasure record escapes audit cursor",
				backupartifact.ErrObjectCorrupt,
			)
		}
		position.ErasureArtifact =
			string(ErasureIntegrityArtifactEvent)
	case ErasureIntegrityArtifactEvent:
		if report.Event != (backupartifact.ErasureLedgerEvent{}) &&
			(report.Event.HashSlot != position.HashSlot ||
				report.Event.EventID != position.ErasureEventID) {
			return backupcontract.IntegrityAuditCursor{}, false, fmt.Errorf(
				"%w: erasure event escapes audit cursor",
				backupartifact.ErrObjectCorrupt,
			)
		}
		return p.advanceErasureSequence(ctx, position)
	default:
		return backupcontract.IntegrityAuditCursor{}, false,
			backupartifact.ErrObjectCorrupt
	}
	cursor, err := p.cursor(position)
	return cursor, true, err
}

func (p *CatalogSegmentIntegrityAuditPlan) advanceErasureSequence(
	ctx context.Context,
	position catalogSegmentAuditPosition,
) (backupcontract.IntegrityAuditCursor, bool, error) {
	if position.ErasureSequence > position.ErasureStopSequence+1 &&
		position.ErasurePreviousSHA != "" {
		position.ErasureSequence--
		namespace, _, _, err := backupartifact.ParseErasureLedgerCommitKey(
			position.ErasureCommitKey,
		)
		if err != nil {
			return backupcontract.IntegrityAuditCursor{}, false, err
		}
		position.ErasureCommitKey =
			backupartifact.ErasureLedgerCommitKey(
				namespace, position.HashSlot, position.ErasureSequence,
			)
		position.ErasureCommitSHA256 = position.ErasurePreviousSHA
		position.ErasureArtifact =
			string(ErasureIntegrityArtifactCommit)
		position.ErasureEventID = ""
		position.ErasureRecordKey = ""
		position.ErasureRecordSHA256 = ""
		position.ErasurePreviousSHA = ""
		cursor, cursorErr := p.cursor(position)
		return cursor, true, cursorErr
	}
	if position.ErasurePreviousSHA != position.ErasureStopSHA256 {
		return backupcontract.IntegrityAuditCursor{}, false, fmt.Errorf(
			"%w: erasure stream does not join retained boundary",
			backupartifact.ErrObjectCorrupt,
		)
	}
	return p.advancePastErasureStream(ctx, position)
}

func (p *CatalogSegmentIntegrityAuditPlan) advancePastErasureStream(
	ctx context.Context,
	position catalogSegmentAuditPosition,
) (backupcontract.IntegrityAuditCursor, bool, error) {
	clearCatalogErasurePosition(&position)
	next, found, err := p.seek(
		ctx, position, int(position.HashSlot), int(position.Stream)+1,
	)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, false, err
	}
	return next, found, nil
}

func (p *CatalogSegmentIntegrityAuditPlan) cursor(
	position catalogSegmentAuditPosition,
) (backupcontract.IntegrityAuditCursor, error) {
	if catalogSegmentAuditAdministrative(position) {
		position.HashSlot = 0
		position.Generation = catalogSegmentAuditNavigationGeneration
	}
	encoded, err := encodeCatalogSegmentAuditPosition(position)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, err
	}
	return backupcontract.IntegrityAuditCursor{
		CycleID: fmt.Sprintf(
			"catalog-segments-%020d-%020d-%020d-%.12s",
			position.ScrubEpoch, position.LowerSequence, position.UpperSequence,
			position.SelectionID,
		),
		ScrubEpoch:          position.ScrubEpoch,
		CatalogSequence:     position.UpperSequence,
		CatalogRootSequence: position.RetainedRootSequence,
		HashSlot:            position.HashSlot,
		Generation:          position.Generation,
		Position:            encoded,
		Phase:               backupcontract.IntegrityAuditPhaseInspect,
	}, nil
}

func catalogCheckpointSegmentReference(
	slot backupartifact.CheckpointSlot,
	stream int,
) *backupartifact.SegmentReference {
	switch stream {
	case 0:
		return cloneAuditSegmentReference(slot.Metadata.Head)
	case 1:
		return cloneAuditSegmentReference(slot.Messages.Head)
	case 2:
		return cloneAuditSegmentReference(slot.Messages.CursorHead)
	case 3:
		if slot.Baseline != nil {
			return cloneAuditSegmentReference(&slot.Baseline.MessageCursor)
		}
	}
	return nil
}

func catalogCheckpointPartitionReference(
	slot backupartifact.CheckpointSlot,
) *backupartifact.PartitionReference {
	if slot.Baseline == nil {
		return nil
	}
	return cloneAuditPartitionReference(&slot.Baseline.Partition)
}

func catalogCheckpointErasureHead(
	heads []backupartifact.ErasureStreamHead,
	hashSlot uint16,
) *backupartifact.ErasureStreamHead {
	index := sort.Search(len(heads), func(index int) bool {
		return heads[index].HashSlot >= hashSlot
	})
	if index >= len(heads) || heads[index].HashSlot != hashSlot {
		return nil
	}
	head := heads[index]
	return &head
}

func encodeCatalogSegmentAuditPosition(
	position catalogSegmentAuditPosition,
) (string, error) {
	body, err := json.Marshal(position)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeCatalogSegmentAuditPosition(
	encoded string,
) (catalogSegmentAuditPosition, error) {
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return catalogSegmentAuditPosition{}, backupartifact.ErrInvalidObject
	}
	var position catalogSegmentAuditPosition
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&position); err != nil ||
		!errors.Is(decoder.Decode(&struct{}{}), io.EOF) ||
		position.Version != catalogSegmentAuditPositionVersion ||
		position.ScrubEpoch == 0 ||
		position.SelectionAtUnixMillis <= 0 ||
		!validCatalogAuditSHA256(position.SelectionID) ||
		len(position.ActiveRestoreCheckpointID) > 256 ||
		!validCatalogSegmentAuditMode(position.Mode) ||
		position.Head.Sequence != position.UpperSequence ||
		position.Page.Sequence == 0 ||
		position.UpperSequence < position.Page.Sequence ||
		position.UpperSequence <= position.LowerSequence ||
		position.RetainedRootSequence == 0 ||
		position.RetainedRootSequence > position.Page.Sequence ||
		position.Generation == "" ||
		position.Stream >= catalogSegmentAuditStreamCount {
		return catalogSegmentAuditPosition{}, backupartifact.ErrInvalidObject
	}
	if !catalogSegmentAuditAdministrative(position) {
		switch IntegrityAuditArtifactKind(position.ArtifactKind) {
		case IntegrityAuditArtifactSegment:
			if position.Reference.SegmentID == "" {
				return catalogSegmentAuditPosition{}, backupartifact.ErrInvalidObject
			}
		case IntegrityAuditArtifactPartition:
			if position.Partition.Key == "" ||
				position.Partition.HashSlot != position.HashSlot ||
				position.PartitionDepth >= catalogSegmentAuditMaxPartitionDepth ||
				position.PartitionObjectIndex < -1 ||
				(position.PartitionObjectIndex >= 0 &&
					uint64(position.PartitionObjectIndex) >=
						position.Partition.ObjectCount) {
				return catalogSegmentAuditPosition{}, backupartifact.ErrInvalidObject
			}
		case IntegrityAuditArtifactErasure:
			_, hashSlot, sequence, parseErr :=
				backupartifact.ParseErasureLedgerCommitKey(
					position.ErasureCommitKey,
				)
			if position.Stream != 5 ||
				position.Generation != catalogSegmentAuditErasureGeneration ||
				position.ErasureSequence == 0 ||
				position.ErasureSequence <=
					position.ErasureStopSequence ||
				(position.ErasureStopSequence > 0 &&
					!validCatalogAuditSHA256(
						position.ErasureStopSHA256,
					)) ||
				(position.ErasureStopSequence == 0 &&
					position.ErasureStopSHA256 != "") ||
				parseErr != nil || hashSlot != position.HashSlot ||
				sequence != position.ErasureSequence ||
				!validCatalogAuditSHA256(
					position.ErasureCommitSHA256,
				) ||
				!validErasureIntegrityArtifactPosition(position) {
				return catalogSegmentAuditPosition{},
					backupartifact.ErrInvalidObject
			}
		default:
			return catalogSegmentAuditPosition{}, backupartifact.ErrInvalidObject
		}
	}
	return position, nil
}

func catalogSegmentAuditSelectionTime(
	scrubEpoch uint64,
	interval time.Duration,
) (time.Time, error) {
	intervalMillis := interval.Milliseconds()
	if scrubEpoch == 0 || intervalMillis <= 0 ||
		scrubEpoch-1 > uint64(math.MaxInt64/intervalMillis) {
		return time.Time{}, backupartifact.ErrInvalidObject
	}
	return time.UnixMilli(
		int64(scrubEpoch-1) * intervalMillis,
	).UTC(), nil
}

func (p *CatalogSegmentIntegrityAuditPlan) installRetentionSelection(
	head backupartifact.CatalogPageReference,
	at time.Time,
	selection IntegrityAuditRetentionSelection,
) error {
	if !validCatalogAuditSHA256(selection.ID) ||
		len(selection.Checkpoints) == 0 {
		return backupartifact.ErrObjectCorrupt
	}
	retainedIDs := make(map[string]struct{}, len(selection.Checkpoints))
	for _, reference := range selection.Checkpoints {
		if reference.ID == "" {
			return backupartifact.ErrObjectCorrupt
		}
		if _, duplicate := retainedIDs[reference.ID]; duplicate {
			return backupartifact.ErrObjectCorrupt
		}
		retainedIDs[reference.ID] = struct{}{}
	}
	expectedID, err := integrityAuditRetentionSelectionID(
		IntegrityAuditRetentionSelectionRequest{Head: head, At: at},
		selection,
	)
	if err != nil || expectedID != selection.ID {
		return backupartifact.ErrObjectCorrupt
	}
	p.cacheMu.Lock()
	p.selectionID = selection.ID
	p.retainedIDs = retainedIDs
	p.cacheMu.Unlock()
	return nil
}

func (p *CatalogSegmentIntegrityAuditPlan) ensureRetentionSelection(
	ctx context.Context,
	position catalogSegmentAuditPosition,
) (map[string]struct{}, error) {
	p.cacheMu.Lock()
	if p.selectionID == position.SelectionID {
		retainedIDs := p.retainedIDs
		p.cacheMu.Unlock()
		return retainedIDs, nil
	}
	p.cacheMu.Unlock()
	activeRestoreCheckpointID := position.ActiveRestoreCheckpointID
	at := time.UnixMilli(position.SelectionAtUnixMillis).UTC()
	selection, err := p.selection.LoadIntegrityAuditRetentionSelection(
		ctx,
		IntegrityAuditRetentionSelectionRequest{
			Head: position.Head, At: at,
			ActiveRestoreCheckpointID: &activeRestoreCheckpointID,
		},
	)
	if err != nil {
		return nil, err
	}
	if selection.ID != position.SelectionID ||
		selection.ActiveRestoreCheckpointID !=
			position.ActiveRestoreCheckpointID {
		return nil, fmt.Errorf(
			"%w: integrity audit retention selection changed",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if err := p.installRetentionSelection(
		position.Head, at, selection,
	); err != nil {
		return nil, err
	}
	p.cacheMu.Lock()
	retainedIDs := p.retainedIDs
	p.cacheMu.Unlock()
	return retainedIDs, nil
}

func catalogSegmentAuditAdministrative(
	position catalogSegmentAuditPosition,
) bool {
	return position.Mode != catalogSegmentAuditModeSegments ||
		position.ArtifactKind == ""
}

func validCatalogSegmentAuditMode(mode string) bool {
	switch mode {
	case catalogSegmentAuditModeSelect,
		catalogSegmentAuditModeFindPrior,
		catalogSegmentAuditModeSegments:
		return true
	default:
		return false
	}
}

func validErasureIntegrityArtifactPosition(
	position catalogSegmentAuditPosition,
) bool {
	switch ErasureIntegrityArtifactKind(position.ErasureArtifact) {
	case ErasureIntegrityArtifactCommit:
		return position.ErasureEventID == "" &&
			position.ErasureRecordKey == "" &&
			position.ErasureRecordSHA256 == ""
	case ErasureIntegrityArtifactReceipt,
		ErasureIntegrityArtifactRecord:
		return validCatalogAuditSHA256(position.ErasureEventID) &&
			position.ErasureRecordKey != "" &&
			validCatalogAuditSHA256(position.ErasureRecordSHA256)
	case ErasureIntegrityArtifactEvent:
		return validCatalogAuditSHA256(position.ErasureEventID) &&
			position.ErasureRecordKey != "" &&
			validCatalogAuditSHA256(position.ErasureRecordSHA256)
	default:
		return false
	}
}

func validCatalogAuditSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func (p *CatalogSegmentIntegrityAuditPlan) advanceCatalogAuditPage(
	position catalogSegmentAuditPosition,
	previous *backupartifact.CatalogPageReference,
) (backupcontract.IntegrityAuditCursor, bool, error) {
	if previous == nil || previous.Sequence <= position.LowerSequence {
		return backupcontract.IntegrityAuditCursor{}, false, nil
	}
	position.Page = *previous
	position.Mode = catalogSegmentAuditModeSelect
	position.Current = nil
	position.PriorPage = nil
	position.Prior = nil
	position.ArtifactKind = ""
	position.Reference = backupartifact.SegmentReference{}
	position.Stop = nil
	position.Partition = backupartifact.PartitionReference{}
	position.PartitionStop = nil
	position.PartitionObjectIndex = 0
	position.PartitionDepth = 0
	cursor, err := p.cursor(position)
	return cursor, true, err
}

func (p *CatalogSegmentIntegrityAuditPlan) loadPageCached(
	ctx context.Context,
	reference backupartifact.CatalogPageReference,
) (backupartifact.CatalogPage, error) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	for _, entry := range p.pageCache {
		if entry.reference == reference {
			return entry.page, nil
		}
	}
	page, err := p.catalog.LoadPageForIntegrityAudit(ctx, reference)
	if err != nil {
		return backupartifact.CatalogPage{}, err
	}
	p.pageCache = appendBoundedCatalogAuditPageCache(
		p.pageCache,
		catalogSegmentAuditPageCacheEntry{reference: reference, page: page},
	)
	return page, nil
}

func (p *CatalogSegmentIntegrityAuditPlan) loadCheckpointCached(
	ctx context.Context,
	reference backupartifact.CatalogCheckpointReference,
) (backupartifact.Checkpoint, error) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	for _, entry := range p.checkpointCache {
		if entry.reference == reference {
			return entry.checkpoint, nil
		}
	}
	checkpoint, err := p.catalog.LoadCheckpointForIntegrityAudit(ctx, reference)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	p.checkpointCache = appendBoundedCatalogAuditCheckpointCache(
		p.checkpointCache,
		catalogSegmentAuditCheckpointCacheEntry{
			reference: reference, checkpoint: checkpoint,
		},
	)
	return checkpoint, nil
}

func appendBoundedCatalogAuditPageCache(
	cache []catalogSegmentAuditPageCacheEntry,
	entry catalogSegmentAuditPageCacheEntry,
) []catalogSegmentAuditPageCacheEntry {
	if len(cache) == catalogSegmentAuditCacheEntries {
		copy(cache, cache[1:])
		cache = cache[:len(cache)-1]
	}
	return append(cache, entry)
}

func appendBoundedCatalogAuditCheckpointCache(
	cache []catalogSegmentAuditCheckpointCacheEntry,
	entry catalogSegmentAuditCheckpointCacheEntry,
) []catalogSegmentAuditCheckpointCacheEntry {
	if len(cache) == catalogSegmentAuditCacheEntries {
		copy(cache, cache[1:])
		cache = cache[:len(cache)-1]
	}
	return append(cache, entry)
}

func cloneAuditCatalogPageReference(
	reference *backupartifact.CatalogPageReference,
) *backupartifact.CatalogPageReference {
	if reference == nil {
		return nil
	}
	out := *reference
	return &out
}

func completeCatalogSegmentAuditCursor(
	sequence uint64,
	rootSequence uint64,
	scrubEpoch uint64,
) backupcontract.IntegrityAuditCursor {
	return backupcontract.IntegrityAuditCursor{
		CycleID: fmt.Sprintf(
			"catalog-segments-complete-%020d-%020d",
			scrubEpoch, sequence,
		),
		ScrubEpoch:          scrubEpoch,
		CatalogSequence:     sequence,
		CatalogRootSequence: rootSequence,
		Generation:          "catalog-segments-complete",
		Position:            "complete",
		Phase:               backupcontract.IntegrityAuditPhaseComplete,
	}
}

func completeAuditPlanCursor(
	cursor backupcontract.IntegrityAuditCursor,
) backupcontract.IntegrityAuditCursor {
	cursor.Position = "complete"
	cursor.Phase = backupcontract.IntegrityAuditPhaseComplete
	cursor.Repository = ""
	cursor.Category = ""
	return cursor
}

func catalogSegmentAuditDebtUpperBound(
	pages uint64,
	hashSlotCount uint16,
) uint64 {
	perPage := uint64(hashSlotCount) * catalogSegmentAuditStreamCount *
		backupruntime.DefaultGenerationMaxSegments
	if perPage != 0 && pages > math.MaxUint64/perPage {
		return math.MaxUint64
	}
	return pages * perPage
}

func saturatingAuditDebtAdd(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

func saturatingAuditDebtMultiply(left, right uint64) uint64 {
	if left != 0 && right > math.MaxUint64/left {
		return math.MaxUint64
	}
	return left * right
}

func clearCatalogErasurePosition(position *catalogSegmentAuditPosition) {
	if position == nil {
		return
	}
	position.ErasureArtifact = ""
	position.ErasureSequence = 0
	position.ErasureStopSequence = 0
	position.ErasureStopSHA256 = ""
	position.ErasureCommitKey = ""
	position.ErasureCommitSHA256 = ""
	position.ErasureEventID = ""
	position.ErasureRecordKey = ""
	position.ErasureRecordSHA256 = ""
	position.ErasurePreviousSHA = ""
}

func cloneAuditSegmentReference(
	reference *backupartifact.SegmentReference,
) *backupartifact.SegmentReference {
	if reference == nil {
		return nil
	}
	out := *reference
	return &out
}

func cloneAuditPartitionReference(
	reference *backupartifact.PartitionReference,
) *backupartifact.PartitionReference {
	if reference == nil {
		return nil
	}
	out := *reference
	return &out
}

func partitionAuditReferencesEqual(
	left *backupartifact.PartitionReference,
	right *backupartifact.PartitionReference,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

var _ GenerationGCIntegrityAuditProtectionSource = (*CatalogSegmentIntegrityAuditPlan)(nil)

func segmentAuditReferencesEqual(
	left *backupartifact.SegmentReference,
	right *backupartifact.SegmentReference,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
