package management

import (
	"context"
	"errors"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// SlotRemoveResult is the manager-facing physical slot removal outcome.
type SlotRemoveResult struct {
	// SlotID is the physical slot identifier accepted for removal.
	SlotID uint32
	// Result is the stable manager outcome label.
	Result string
}

// AddSlot creates a new physical slot and returns the latest slot detail.
func (a *App) AddSlot(ctx context.Context) (SlotDetail, error) {
	if a == nil || a.cluster == nil {
		return SlotDetail{}, nil
	}
	if len(a.cluster.GetMigrationStatus()) > 0 {
		return SlotDetail{}, ErrSlotMigrationsInProgress
	}

	slotID, err := a.cluster.AddSlot(ctx)
	if err != nil {
		return SlotDetail{}, a.normalizeSlotMutationError(err)
	}
	return a.GetSlot(ctx, uint32(slotID))
}

// RemoveSlot starts physical slot removal after validating the slot exists.
func (a *App) RemoveSlot(ctx context.Context, slotID uint32) (SlotRemoveResult, error) {
	if a == nil || a.cluster == nil {
		return SlotRemoveResult{}, nil
	}
	if _, err := a.GetSlot(ctx, slotID); err != nil {
		return SlotRemoveResult{}, err
	}
	if len(a.cluster.GetMigrationStatus()) > 0 {
		return SlotRemoveResult{}, ErrSlotMigrationsInProgress
	}
	if err := a.cluster.RemoveSlot(ctx, multiraft.SlotID(slotID)); err != nil {
		if errors.Is(err, raftcluster.ErrSlotNotFound) {
			return SlotRemoveResult{}, controllermeta.ErrNotFound
		}
		return SlotRemoveResult{}, a.normalizeSlotMutationError(err)
	}
	return SlotRemoveResult{
		SlotID: slotID,
		Result: "removal_started",
	}, nil
}

func (a *App) normalizeSlotMutationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, raftcluster.ErrInvalidConfig) || errors.Is(err, controllermeta.ErrInvalidArgument) {
		if len(a.cluster.GetMigrationStatus()) > 0 {
			return ErrSlotMigrationsInProgress
		}
	}
	return err
}
