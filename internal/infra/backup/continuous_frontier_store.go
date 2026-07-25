package backup

import (
	"context"
	"errors"
	"fmt"
	"sort"

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
	state CoordinationStateStore
}

// NewControllerSlotFrontierStore creates a frontier adapter over bounded Controller state.
func NewControllerSlotFrontierStore(state CoordinationStateStore) (*ControllerSlotFrontierStore, error) {
	if state == nil {
		return nil, fmt.Errorf("backup frontier store: Controller state is required")
	}
	return &ControllerSlotFrontierStore{state: state}, nil
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

// CompareAndSwap updates one Slot record at its own revision while retrying
// unrelated global Controller revision conflicts against a fresh snapshot.
func (s *ControllerSlotFrontierStore) CompareAndSwap(ctx context.Context, expectedRevision uint64, next backupcontract.SlotFrontier) error {
	if s == nil || s.state == nil {
		return runtimebackup.ErrInvalidCapture
	}
	for attempt := 0; attempt < maxControllerFrontierCASAttempts; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return err
		}
		index, found := findSlotFrontier(state.SlotFrontiers, next.HashSlot)
		if found {
			if state.SlotFrontiers[index].Revision != expectedRevision {
				return runtimebackup.ErrFrontierConflict
			}
			state.SlotFrontiers[index] = backupcontract.CloneSlotFrontier(next)
		} else {
			if expectedRevision != 0 {
				return runtimebackup.ErrFrontierConflict
			}
			state.SlotFrontiers = append(state.SlotFrontiers, backupcontract.CloneSlotFrontier(next))
			sort.Slice(state.SlotFrontiers, func(i, j int) bool {
				return state.SlotFrontiers[i].HashSlot < state.SlotFrontiers[j].HashSlot
			})
		}
		err = s.state.CompareAndSwap(ctx, state.Revision, state)
		if err == nil {
			return nil
		}
		if !errors.Is(err, backupusecase.ErrStateConflict) {
			return err
		}
	}
	return fmt.Errorf("%w: Controller state remained contended", runtimebackup.ErrFrontierConflict)
}

func findSlotFrontier(frontiers []backupcontract.SlotFrontier, hashSlot uint16) (int, bool) {
	index := sort.Search(len(frontiers), func(index int) bool {
		return frontiers[index].HashSlot >= hashSlot
	})
	return index, index < len(frontiers) && frontiers[index].HashSlot == hashSlot
}

var _ runtimebackup.SlotFrontierStore = (*ControllerSlotFrontierStore)(nil)
