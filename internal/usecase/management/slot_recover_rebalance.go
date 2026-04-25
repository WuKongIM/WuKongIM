package management

import (
	"context"
	"errors"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// ErrUnsupportedRecoverStrategy reports that the requested recover strategy is not supported.
var ErrUnsupportedRecoverStrategy = errors.New("management: unsupported slot recover strategy")

// ErrSlotMigrationsInProgress reports that a new rebalance cannot start while migrations are active.
var ErrSlotMigrationsInProgress = errors.New("management: slot migrations already in progress")

// SlotRecoverStrategy identifies the manager-exposed slot recover strategy.
type SlotRecoverStrategy string

const (
	// SlotRecoverStrategyLatestLiveReplica keeps manager recover aligned with the
	// only cluster recover strategy currently available.
	SlotRecoverStrategyLatestLiveReplica SlotRecoverStrategy = "latest_live_replica"
)

// SlotRecoverResult is the manager-facing slot recover outcome.
type SlotRecoverResult struct {
	// Strategy is the applied recover strategy.
	Strategy string
	// Result is the recover outcome label.
	Result string
	// Slot contains the latest slot detail after the recover call.
	Slot SlotDetail
}

// SlotRebalancePlanItem is one manager-facing rebalance migration plan item.
type SlotRebalancePlanItem struct {
	// HashSlot is the logical hash slot being migrated.
	HashSlot uint16
	// FromSlotID is the source physical slot.
	FromSlotID uint32
	// ToSlotID is the target physical slot.
	ToSlotID uint32
}

// SlotRebalanceResult is the manager-facing rebalance result body.
type SlotRebalanceResult struct {
	// Total is the number of migration plan items returned.
	Total int
	// Items contains the ordered migration plan items.
	Items []SlotRebalancePlanItem
}

// RecoverSlot runs one strict slot recover flow and returns the latest detail.
func (a *App) RecoverSlot(ctx context.Context, slotID uint32, strategy SlotRecoverStrategy) (SlotRecoverResult, error) {
	if a == nil || a.cluster == nil {
		return SlotRecoverResult{}, nil
	}
	if strategy != SlotRecoverStrategyLatestLiveReplica {
		return SlotRecoverResult{}, ErrUnsupportedRecoverStrategy
	}

	if _, err := a.GetSlot(ctx, slotID); err != nil {
		return SlotRecoverResult{}, err
	}
	if err := a.cluster.RecoverSlotStrict(ctx, slotID, raftcluster.RecoverStrategyLatestLiveReplica); err != nil {
		return SlotRecoverResult{}, err
	}
	slot, err := a.GetSlot(ctx, slotID)
	if err != nil {
		return SlotRecoverResult{}, err
	}
	return SlotRecoverResult{
		Strategy: string(strategy),
		Result:   "quorum_reachable",
		Slot:     slot,
	}, nil
}

// RebalanceSlots starts a leader-sourced slot rebalance and returns the plan.
func (a *App) RebalanceSlots(ctx context.Context) (SlotRebalanceResult, error) {
	if a == nil || a.cluster == nil {
		return SlotRebalanceResult{}, nil
	}
	if _, err := a.cluster.ListSlotAssignmentsStrict(ctx); err != nil {
		return SlotRebalanceResult{}, err
	}
	if len(a.cluster.GetMigrationStatus()) > 0 {
		return SlotRebalanceResult{}, ErrSlotMigrationsInProgress
	}

	plan, err := a.cluster.Rebalance(ctx)
	if err != nil {
		return SlotRebalanceResult{}, err
	}

	items := make([]SlotRebalancePlanItem, 0, len(plan))
	for _, item := range plan {
		items = append(items, SlotRebalancePlanItem{
			HashSlot:   item.HashSlot,
			FromSlotID: uint32(item.From),
			ToSlotID:   uint32(item.To),
		})
	}
	return SlotRebalanceResult{
		Total: len(items),
		Items: items,
	}, nil
}
