package management

import (
	"context"
	"errors"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// ErrTargetNodeNotAssigned reports that the requested leader target is outside
// the slot's desired peer set.
var ErrTargetNodeNotAssigned = errors.New("management: target node is not assigned to slot")

// TransferSlotLeader transfers one slot leader and returns the latest slot detail.
func (a *App) TransferSlotLeader(ctx context.Context, slotID uint32, targetNodeID uint64) (SlotDetail, error) {
	if a == nil || a.cluster == nil {
		return SlotDetail{}, nil
	}

	detail, err := a.GetSlot(ctx, slotID)
	if err != nil {
		return SlotDetail{}, err
	}
	if !containsUint64(detail.Assignment.DesiredPeers, targetNodeID) {
		return SlotDetail{}, ErrTargetNodeNotAssigned
	}
	if detail.Runtime.LeaderID == targetNodeID {
		return detail, nil
	}

	if err := a.cluster.TransferSlotLeader(ctx, slotID, multiraft.NodeID(targetNodeID)); err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			return SlotDetail{}, err
		}
		return SlotDetail{}, err
	}
	return a.GetSlot(ctx, slotID)
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
