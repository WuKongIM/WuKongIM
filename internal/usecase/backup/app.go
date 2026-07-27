package backup

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxStateRetries = 8

// Options configures the entry-independent continuous-backup facade.
type Options struct {
	// Enabled admits backup operations when true.
	Enabled bool
	// HashSlotCount is the immutable logical partition count.
	HashSlotCount uint16
	// Store persists the bounded continuous coordination state.
	Store StateStore
	// CatalogBrowser reads immutable checkpoint history through a derived index.
	CatalogBrowser CheckpointCatalogBrowser
	// CatalogRetention appends immutable operator hold/release decisions.
	CatalogRetention CheckpointCatalogRetention
	// Checkpoints publishes complete continuous-capture vector cuts.
	Checkpoints *CheckpointCoordinator
	// SourceClusterID and SourceGeneration identify the incarnation that may be fenced.
	SourceClusterID  string
	SourceGeneration string
	// SourceFenceConvergence proves every active data node has applied the fence.
	SourceFenceConvergence SourceFenceConvergence
	// SourceFenceSigner issues the Controller-backed receipt.
	SourceFenceSigner backupartifact.ManifestSigner
	// NewSourceFenceID allocates the immutable fence identity.
	NewSourceFenceID func() string
	// Now returns the current UTC time.
	Now func() time.Time
	// MaxCheckpointAge is the checkpoint-age health threshold.
	MaxCheckpointAge time.Duration
}

// App is the narrow continuous-backup facade shared by Manager and runtime.
type App struct {
	enabled           bool
	hashSlotCount     uint16
	store             StateStore
	catalogBrowser    CheckpointCatalogBrowser
	catalogRetention  CheckpointCatalogRetention
	checkpoints       *CheckpointCoordinator
	sourceClusterID   string
	sourceGeneration  string
	sourceFence       SourceFenceConvergence
	sourceFenceSigner backupartifact.ManifestSigner
	newSourceFenceID  func() string
	now               func() time.Time
	maxCheckpointAge  time.Duration
}

// NewApp creates the continuous-backup facade.
func NewApp(options Options) (*App, error) {
	if options.HashSlotCount == 0 || options.Store == nil ||
		options.Now == nil {
		return nil, fmt.Errorf(
			"%w: continuous backup dependencies are incomplete",
			ErrInvalidRequest,
		)
	}
	maxCheckpointAge := options.MaxCheckpointAge
	if maxCheckpointAge == 0 {
		maxCheckpointAge = 5 * time.Minute
	}
	if maxCheckpointAge < 0 {
		return nil, fmt.Errorf(
			"%w: max checkpoint age must be positive",
			ErrInvalidRequest,
		)
	}
	return &App{
		enabled:           options.Enabled,
		hashSlotCount:     options.HashSlotCount,
		store:             options.Store,
		catalogBrowser:    options.CatalogBrowser,
		catalogRetention:  options.CatalogRetention,
		checkpoints:       options.Checkpoints,
		sourceClusterID:   strings.TrimSpace(options.SourceClusterID),
		sourceGeneration:  strings.TrimSpace(options.SourceGeneration),
		sourceFence:       options.SourceFenceConvergence,
		sourceFenceSigner: options.SourceFenceSigner,
		newSourceFenceID:  options.NewSourceFenceID,
		now:               options.Now,
		maxCheckpointAge:  maxCheckpointAge,
	}, nil
}

// Status returns the bounded durable continuous-backup projection.
func (a *App) Status(ctx context.Context) (StatusSnapshot, error) {
	if a == nil || !a.enabled {
		return StatusSnapshot{
			Enabled: false,
			Health:  HealthDisabled,
		}, nil
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	snapshot := StatusSnapshot{
		Enabled:                 true,
		Health:                  HealthUnknown,
		MaxCheckpointAgeSeconds: int64(a.maxCheckpointAge / time.Second),
		CaptureLeases:           captureLeaseSnapshots(state.SlotFrontiers),
		ErasureStreams:          erasureStreamProgress(state.ErasureStreams),
	}
	if state.CatalogHead == nil || a.catalogBrowser == nil {
		return snapshot, nil
	}
	latest, err := a.catalogBrowser.Get(
		ctx, *state.CatalogHead,
		state.CatalogHead.LatestCheckpointID,
	)
	if err != nil {
		return StatusSnapshot{}, err
	}
	summary := latest.CheckpointSummary
	snapshot.LatestCheckpoint = &summary
	age := a.now().UTC().Unix() -
		time.UnixMilli(latest.EffectiveAtUnixMillis).UTC().Unix()
	if age < 0 {
		age = 0
	}
	snapshot.CheckpointAgeSeconds = &age
	snapshot.Health = HealthHealthy
	if time.Duration(age)*time.Second > a.maxCheckpointAge {
		snapshot.Health = HealthDegraded
	}
	return snapshot, nil
}

func captureLeaseSnapshots(
	frontiers []SlotFrontier,
) []CaptureLeaseSnapshot {
	result := make([]CaptureLeaseSnapshot, len(frontiers))
	for index, frontier := range frontiers {
		snapshot := CaptureLeaseSnapshot{
			HashSlot:                     frontier.HashSlot,
			SlotID:                       frontier.Lease.SlotID,
			SourceSlotID:                 frontier.SourceSlotID,
			HolderNodeID:                 frontier.Lease.HolderNodeID,
			LeaderTerm:                   frontier.Lease.LeaderTerm,
			ConfigEpoch:                  frontier.Lease.ConfigEpoch,
			Generation:                   frontier.Generation,
			LeaseSequence:                frontier.Lease.Sequence,
			FrontierRevision:             frontier.Revision,
			MetadataSourceWatermark:      frontier.Metadata.SourceHighWatermark,
			MessageSourceWatermark:       frontier.Messages.SourceHighWatermark,
			AcquiredAtUnixMillis:         frontier.Lease.AcquiredAtUnixMillis,
			SourcePinStartedAtUnixMillis: frontier.SourcePinStartedAtUnixMillis,
			FrontierUpdatedUnixMillis:    frontier.UpdatedAtUnixMillis,
		}
		if frontier.LastPromotion != nil {
			snapshot.LastPromotionPreviousGeneration =
				frontier.LastPromotion.PreviousGeneration
			snapshot.LastPromotionReason = frontier.LastPromotion.Reason
			snapshot.LastPromotionAtUnixMillis =
				frontier.LastPromotion.PromotedAtUnixMillis
		}
		result[index] = snapshot
	}
	return result
}

func erasureStreamProgress(
	streams []ErasureStreamState,
) []ErasureStreamProgress {
	result := make([]ErasureStreamProgress, len(streams))
	for index, stream := range streams {
		result[index] = ErasureStreamProgress{
			HashSlot: stream.HashSlot,
			Pending:  stream.Pending != nil,
		}
		if stream.Head != nil {
			result[index].Sequence = stream.Head.Sequence
		}
	}
	return result
}

// CoordinationState returns a detached state snapshot for continuous runtime
// and infrastructure coordination.
func (a *App) CoordinationState(ctx context.Context) (State, error) {
	if a == nil || !a.enabled {
		return State{}, ErrDisabled
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return State{}, err
	}
	return state.Clone(), nil
}

// ReserveErasureLedgerCommit allocates the next contiguous sequence in one
// Hash Slot stream while keeping Controller state bounded.
func (a *App) ReserveErasureLedgerCommit(
	ctx context.Context,
	reference ErasureLedgerRecordReference,
) (ErasureLedgerRecordReference, error) {
	if a == nil || !a.enabled {
		return ErasureLedgerRecordReference{}, ErrDisabled
	}
	reference.EventID = strings.TrimSpace(reference.EventID)
	reference.RecordKey = strings.TrimSpace(reference.RecordKey)
	reference.RecordSHA256 = strings.TrimSpace(reference.RecordSHA256)
	if reference.Sequence != 0 ||
		!validErasureLedgerRecordReference(reference) {
		return ErasureLedgerRecordReference{}, fmt.Errorf(
			"%w: invalid erasure ledger record reference",
			ErrInvalidRequest,
		)
	}
	var reserved ErasureLedgerRecordReference
	err := a.mutate(ctx, func(state *State) error {
		stream := ensureErasureStream(state, reference.HashSlot)
		if stream.Pending != nil {
			pending := *stream.Pending
			candidate := reference
			candidate.Sequence = pending.Sequence
			if pending == candidate {
				reserved = pending
				return nil
			}
			return ErrErasureLedgerPending
		}
		if stream.LastCommitted != nil {
			committed := *stream.LastCommitted
			candidate := reference
			candidate.Sequence = committed.Sequence
			if committed == candidate {
				reserved = committed
				return nil
			}
		}
		if erasureLedgerReservationCount(state.ErasureStreams) >=
			backupartifact.MaxErasureLedgerEvents {
			return fmt.Errorf(
				"%w: erasure ledger event capacity is exhausted",
				ErrStateConflict,
			)
		}
		var boundary uint64
		if stream.Head != nil {
			boundary = stream.Head.Sequence
		}
		if boundary == ^uint64(0) {
			return fmt.Errorf(
				"%w: erasure ledger sequence exhausted",
				ErrStateConflict,
			)
		}
		reserved = reference
		reserved.Sequence = boundary + 1
		stream.Pending = &reserved
		return nil
	})
	return reserved, err
}

// CommitErasureLedgerCommit advances one stream head after both repositories
// durably contain the exact signed commit marker.
func (a *App) CommitErasureLedgerCommit(
	ctx context.Context,
	head backupartifact.ErasureStreamHead,
	eventID string,
) error {
	if a == nil || !a.enabled {
		return ErrDisabled
	}
	eventID = strings.TrimSpace(eventID)
	if backupartifact.ValidateErasureStreamHead(head) != nil ||
		!validLowerSHA256(eventID) {
		return fmt.Errorf(
			"%w: invalid erasure ledger commit identity",
			ErrInvalidRequest,
		)
	}
	return a.mutate(ctx, func(state *State) error {
		stream, found := findErasureStream(
			state.ErasureStreams, head.HashSlot,
		)
		if !found {
			return fmt.Errorf(
				"%w: erasure ledger commit is not pending",
				ErrStateConflict,
			)
		}
		if stream.Head != nil && head.Sequence <= stream.Head.Sequence {
			if head.Sequence == stream.Head.Sequence &&
				*stream.Head != head {
				return fmt.Errorf(
					"%w: erasure ledger committed head mismatch",
					ErrStateConflict,
				)
			}
			return nil
		}
		if stream.Pending == nil {
			return fmt.Errorf(
				"%w: erasure ledger commit is not pending",
				ErrStateConflict,
			)
		}
		var boundary uint64
		if stream.Head != nil {
			boundary = stream.Head.Sequence
		}
		pending := stream.Pending
		if pending.HashSlot != head.HashSlot ||
			pending.Sequence != head.Sequence ||
			pending.EventID != eventID ||
			head.Sequence != boundary+1 {
			return fmt.Errorf(
				"%w: erasure ledger commit fence mismatch",
				ErrStateConflict,
			)
		}
		committed := *pending
		committedHead := head
		stream.Head = &committedHead
		stream.Pending = nil
		stream.LastCommitted = &committed
		return nil
	})
}

func validErasureLedgerRecordReference(
	reference ErasureLedgerRecordReference,
) bool {
	return validLowerSHA256(reference.EventID) &&
		validLowerSHA256(reference.RecordSHA256) &&
		backupartifact.ValidateErasureLedgerRecordKey(
			reference.RecordKey, reference.EventID,
		) == nil &&
		strings.HasPrefix(
			reference.RecordKey,
			fmt.Sprintf(
				"erasure-ledger/events/%04x/",
				reference.HashSlot,
			),
		)
}

func ensureErasureStream(
	state *State,
	hashSlot uint16,
) *ErasureStreamState {
	index := sort.Search(
		len(state.ErasureStreams),
		func(index int) bool {
			return state.ErasureStreams[index].HashSlot >= hashSlot
		},
	)
	if index < len(state.ErasureStreams) &&
		state.ErasureStreams[index].HashSlot == hashSlot {
		return &state.ErasureStreams[index]
	}
	state.ErasureStreams = append(
		state.ErasureStreams, ErasureStreamState{},
	)
	copy(
		state.ErasureStreams[index+1:],
		state.ErasureStreams[index:],
	)
	state.ErasureStreams[index] =
		ErasureStreamState{HashSlot: hashSlot}
	return &state.ErasureStreams[index]
}

func findErasureStream(
	streams []ErasureStreamState,
	hashSlot uint16,
) (*ErasureStreamState, bool) {
	index := sort.Search(
		len(streams),
		func(index int) bool {
			return streams[index].HashSlot >= hashSlot
		},
	)
	if index >= len(streams) ||
		streams[index].HashSlot != hashSlot {
		return nil, false
	}
	return &streams[index], true
}

func erasureLedgerReservationCount(
	streams []ErasureStreamState,
) uint64 {
	var total uint64
	for _, stream := range streams {
		if stream.Head != nil {
			if stream.Head.Sequence >=
				uint64(backupartifact.MaxErasureLedgerEvents)-total {
				return backupartifact.MaxErasureLedgerEvents
			}
			total += stream.Head.Sequence
		}
		if stream.Pending != nil {
			if total >= backupartifact.MaxErasureLedgerEvents {
				return backupartifact.MaxErasureLedgerEvents
			}
			total++
		}
	}
	return total
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (a *App) mutate(
	ctx context.Context,
	update func(*State) error,
) error {
	for attempt := 0; attempt < maxStateRetries; attempt++ {
		state, err := a.store.Load(ctx)
		if err != nil {
			return err
		}
		next := state.Clone()
		if err := update(&next); err != nil {
			return err
		}
		if err := a.store.CompareAndSwap(
			ctx, state.Revision, next,
		); err != nil {
			if errors.Is(err, ErrStateConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrStateConflict
}
