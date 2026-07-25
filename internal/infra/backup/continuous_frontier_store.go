package backup

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

const maxControllerFrontierCASAttempts = 16

// CoordinationStateStore is the bounded Controller state seam shared with backup use cases.
type CoordinationStateStore interface {
	// Load returns one detached current coordination snapshot.
	Load(context.Context) (backupcontract.State, error)
	// CompareAndSwap replaces coordination state at the expected global Controller revision.
	CompareAndSwap(context.Context, uint64, backupcontract.State) error
}

// ControllerSlotFrontierStore persists per-Slot frontiers without overwriting
// concurrent job, catalog, verification, retention, or erasure coordination.
type ControllerSlotFrontierStore struct {
	state     CoordinationStateStore
	authority runtimebackup.SlotCaptureAuthoritySource
}

// NewControllerSlotFrontierStore creates a frontier adapter over bounded Controller state.
func NewControllerSlotFrontierStore(state CoordinationStateStore, authority runtimebackup.SlotCaptureAuthoritySource) (*ControllerSlotFrontierStore, error) {
	if state == nil || authority == nil {
		return nil, fmt.Errorf("backup frontier store: Controller state and Slot authority are required")
	}
	return &ControllerSlotFrontierStore{state: state, authority: authority}, nil
}

// Load returns the detached frontier for hashSlot, when present.
func (s *ControllerSlotFrontierStore) Load(ctx context.Context, hashSlot uint16) (runtimebackup.FrontierSnapshot, error) {
	if s == nil || s.state == nil {
		return runtimebackup.FrontierSnapshot{}, runtimebackup.ErrInvalidCapture
	}
	state, err := s.state.Load(ctx)
	if err != nil {
		return runtimebackup.FrontierSnapshot{}, err
	}
	index, found := findSlotFrontier(state.SlotFrontiers, hashSlot)
	if !found {
		return runtimebackup.FrontierSnapshot{}, nil
	}
	return runtimebackup.FrontierSnapshot{
		Frontier: backupcontract.CloneSlotFrontier(state.SlotFrontiers[index]),
		Found:    true,
	}, nil
}

// AcquireLease durably creates or takes over one Slot lease without changing
// either committed stream head. Reacquiring the same authority is read-only,
// which preserves identity across Controller Leader failover.
func (s *ControllerSlotFrontierStore) AcquireLease(ctx context.Context, hashSlot uint16, initialGeneration string, acquiredAtUnixMillis int64) (runtimebackup.FrontierSnapshot, error) {
	if s == nil || s.state == nil || s.authority == nil ||
		!validControllerCaptureGeneration(initialGeneration) || acquiredAtUnixMillis <= 0 {
		return runtimebackup.FrontierSnapshot{}, runtimebackup.ErrInvalidCapture
	}
	authority, err := s.currentAuthority(ctx, hashSlot)
	if err != nil {
		return runtimebackup.FrontierSnapshot{}, err
	}
	for attempt := 0; attempt < maxControllerFrontierCASAttempts; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return runtimebackup.FrontierSnapshot{}, err
		}
		index, found := findSlotFrontier(state.SlotFrontiers, hashSlot)
		if found {
			current := state.SlotFrontiers[index]
			if captureLeaseMatchesAuthority(current.Lease, authority) &&
				current.Lease.Generation == current.Generation {
				if _, err := s.requireAuthority(ctx, hashSlot, authority); err != nil {
					return runtimebackup.FrontierSnapshot{}, err
				}
				return runtimebackup.FrontierSnapshot{
					Frontier: backupcontract.CloneSlotFrontier(current),
					Found:    true,
				}, nil
			}
		}

		next := backupcontract.SlotFrontier{
			HashSlot: hashSlot, Generation: initialGeneration, SourceSlotID: authority.SlotID,
			SourcePinStartedAtUnixMillis:  acquiredAtUnixMillis,
			GenerationStartedAtUnixMillis: acquiredAtUnixMillis,
		}
		var leaseSequence uint64 = 1
		if found {
			next = backupcontract.CloneSlotFrontier(state.SlotFrontiers[index])
			if next.Revision == math.MaxUint64 || next.Lease.Sequence == math.MaxUint64 {
				return runtimebackup.FrontierSnapshot{}, fmt.Errorf("%w: capture lease sequence overflow", runtimebackup.ErrInvalidCapture)
			}
			next.Revision++
			leaseSequence = next.Lease.Sequence + 1
		} else {
			next.Revision = 1
		}
		next.Lease = backupcontract.SlotCaptureLease{
			SlotID: authority.SlotID, LeaderTerm: authority.LeaderTerm,
			ConfigEpoch: authority.ConfigEpoch, HolderNodeID: authority.HolderNodeID,
			Generation: next.Generation, Sequence: leaseSequence,
			AcquiredAtUnixMillis: acquiredAtUnixMillis,
		}
		next.UpdatedAtUnixMillis = acquiredAtUnixMillis
		if next.GenerationStartedAtUnixMillis <= 0 {
			next.GenerationStartedAtUnixMillis = acquiredAtUnixMillis
		}
		if found {
			state.SlotFrontiers[index] = next
		} else {
			state.SlotFrontiers = append(state.SlotFrontiers, next)
			sort.Slice(state.SlotFrontiers, func(i, j int) bool {
				return state.SlotFrontiers[i].HashSlot < state.SlotFrontiers[j].HashSlot
			})
		}
		if _, err := s.requireAuthority(ctx, hashSlot, authority); err != nil {
			return runtimebackup.FrontierSnapshot{}, err
		}
		err = s.state.CompareAndSwap(ctx, state.Revision, state)
		if err == nil {
			return runtimebackup.FrontierSnapshot{
				Frontier:       backupcontract.CloneSlotFrontier(next),
				Found:          true,
				LeaseTakenOver: found,
			}, nil
		}
		if !errors.Is(err, backupusecase.ErrStateConflict) {
			return runtimebackup.FrontierSnapshot{}, err
		}
		if _, err := s.requireAuthority(ctx, hashSlot, authority); err != nil {
			return runtimebackup.FrontierSnapshot{}, err
		}
	}
	return runtimebackup.FrontierSnapshot{}, fmt.Errorf("%w: Controller state remained contended", runtimebackup.ErrFrontierConflict)
}

// CompareAndSwap updates one Slot record at its own revision while retrying
// unrelated global Controller revision conflicts against a fresh snapshot.
func (s *ControllerSlotFrontierStore) CompareAndSwap(ctx context.Context, expectedRevision uint64, expectedLease backupcontract.SlotCaptureLease, next backupcontract.SlotFrontier) error {
	if s == nil || s.state == nil || s.authority == nil ||
		!backupcontract.SlotCaptureLeasesEqual(expectedLease, next.Lease) {
		return runtimebackup.ErrInvalidCapture
	}
	authority, err := s.currentAuthority(ctx, next.HashSlot)
	if err != nil {
		return err
	}
	if !captureLeaseMatchesAuthority(expectedLease, authority) {
		return runtimebackup.ErrCaptureLeaseFenced
	}
	for attempt := 0; attempt < maxControllerFrontierCASAttempts; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return err
		}
		index, found := findSlotFrontier(state.SlotFrontiers, next.HashSlot)
		if found {
			current := state.SlotFrontiers[index]
			if current.Revision != expectedRevision {
				return runtimebackup.ErrFrontierConflict
			}
			if !backupcontract.SlotCaptureLeasesEqual(current.Lease, expectedLease) {
				return runtimebackup.ErrCaptureLeaseFenced
			}
			state.SlotFrontiers[index] = backupcontract.CloneSlotFrontier(next)
		} else {
			return runtimebackup.ErrCaptureLeaseFenced
		}
		if _, err := s.requireAuthority(ctx, next.HashSlot, authority); err != nil {
			return err
		}
		err = s.state.CompareAndSwap(ctx, state.Revision, state)
		if err == nil {
			return nil
		}
		if !errors.Is(err, backupusecase.ErrStateConflict) {
			return err
		}
		if _, err := s.requireAuthority(ctx, next.HashSlot, authority); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w: Controller state remained contended", runtimebackup.ErrFrontierConflict)
}

// PromoteGeneration atomically swaps only a completed pending rebase while
// preserving the exact Raft authority and lease sequence.
func (s *ControllerSlotFrontierStore) PromoteGeneration(
	ctx context.Context,
	expectedRevision uint64,
	expectedLease backupcontract.SlotCaptureLease,
	next backupcontract.SlotFrontier,
) error {
	if s == nil || s.state == nil || s.authority == nil ||
		next.Generation == expectedLease.Generation ||
		next.Lease.Generation != next.Generation ||
		next.Lease.SlotID != expectedLease.SlotID ||
		next.Lease.LeaderTerm != expectedLease.LeaderTerm ||
		next.Lease.ConfigEpoch != expectedLease.ConfigEpoch ||
		next.Lease.HolderNodeID != expectedLease.HolderNodeID ||
		next.Lease.Sequence != expectedLease.Sequence ||
		next.SourceSlotID != expectedLease.SlotID ||
		next.GenerationStartedAtUnixMillis != next.Lease.AcquiredAtUnixMillis ||
		next.SourcePinStartedAtUnixMillis != next.Lease.AcquiredAtUnixMillis ||
		next.Lease.AcquiredAtUnixMillis < expectedLease.AcquiredAtUnixMillis ||
		next.Lease.AcquiredAtUnixMillis != next.UpdatedAtUnixMillis ||
		next.Rebase != nil || next.Baseline == nil {
		return runtimebackup.ErrInvalidCapture
	}
	authority, err := s.currentAuthority(ctx, next.HashSlot)
	if err != nil {
		return err
	}
	if !captureLeaseMatchesAuthority(expectedLease, authority) {
		return runtimebackup.ErrCaptureLeaseFenced
	}
	for attempt := 0; attempt < maxControllerFrontierCASAttempts; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return err
		}
		index, found := findSlotFrontier(state.SlotFrontiers, next.HashSlot)
		if !found {
			return runtimebackup.ErrCaptureLeaseFenced
		}
		current := state.SlotFrontiers[index]
		if current.Revision != expectedRevision {
			return runtimebackup.ErrFrontierConflict
		}
		if !backupcontract.SlotCaptureLeasesEqual(current.Lease, expectedLease) {
			return runtimebackup.ErrCaptureLeaseFenced
		}
		if current.Rebase == nil ||
			current.Rebase.TargetGeneration != next.Generation ||
			current.Generation != expectedLease.Generation {
			return runtimebackup.ErrInvalidCapture
		}
		state.SlotFrontiers[index] = backupcontract.CloneSlotFrontier(next)
		if _, err := s.requireAuthority(ctx, next.HashSlot, authority); err != nil {
			return err
		}
		err = s.state.CompareAndSwap(ctx, state.Revision, state)
		if err == nil {
			return nil
		}
		if !errors.Is(err, backupusecase.ErrStateConflict) {
			return err
		}
		if _, err := s.requireAuthority(ctx, next.HashSlot, authority); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w: Controller state remained contended", runtimebackup.ErrFrontierConflict)
}

func (s *ControllerSlotFrontierStore) currentAuthority(ctx context.Context, hashSlot uint16) (runtimebackup.SlotCaptureAuthority, error) {
	authority, err := s.authority.CurrentCaptureAuthority(ctx, hashSlot)
	if err != nil {
		return runtimebackup.SlotCaptureAuthority{}, err
	}
	if authority.SlotID == 0 || authority.LeaderTerm == 0 ||
		authority.ConfigEpoch == 0 || authority.HolderNodeID == 0 {
		return runtimebackup.SlotCaptureAuthority{}, runtimebackup.ErrCaptureNotLeader
	}
	return authority, nil
}

func (s *ControllerSlotFrontierStore) requireAuthority(ctx context.Context, hashSlot uint16, expected runtimebackup.SlotCaptureAuthority) (runtimebackup.SlotCaptureAuthority, error) {
	current, err := s.currentAuthority(ctx, hashSlot)
	if err != nil {
		return runtimebackup.SlotCaptureAuthority{}, err
	}
	if current != expected {
		return runtimebackup.SlotCaptureAuthority{}, runtimebackup.ErrCaptureLeaseFenced
	}
	return current, nil
}

func captureLeaseMatchesAuthority(lease backupcontract.SlotCaptureLease, authority runtimebackup.SlotCaptureAuthority) bool {
	return lease.SlotID == authority.SlotID &&
		lease.LeaderTerm == authority.LeaderTerm &&
		lease.ConfigEpoch == authority.ConfigEpoch &&
		lease.HolderNodeID == authority.HolderNodeID
}

func validControllerCaptureGeneration(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' ||
			(char == '.' && index > 0) {
			continue
		}
		return false
	}
	return !strings.Contains(value, "..")
}

func findSlotFrontier(frontiers []backupcontract.SlotFrontier, hashSlot uint16) (int, bool) {
	index := sort.Search(len(frontiers), func(index int) bool {
		return frontiers[index].HashSlot >= hashSlot
	})
	return index, index < len(frontiers) && frontiers[index].HashSlot == hashSlot
}

var _ runtimebackup.SlotFrontierStore = (*ControllerSlotFrontierStore)(nil)
var _ runtimebackup.SlotGenerationPromoter = (*ControllerSlotFrontierStore)(nil)
