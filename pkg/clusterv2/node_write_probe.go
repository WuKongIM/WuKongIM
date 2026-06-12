package clusterv2

import (
	"context"
	"fmt"
	"sort"

	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

// ProbeWriteReady verifies that each routed physical Slot can accept a metadata write.
func (n *Node) ProbeWriteReady(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := n.ensureForeground(); err != nil {
		return err
	}
	snapshot := n.Snapshot()
	if !snapshot.RoutesReady || !snapshot.SlotsReady || snapshot.HashSlotCount == 0 {
		return fmt.Errorf("%w: routes=%t slots=%t hashSlotCount=%d", ErrRouteNotReady, snapshot.RoutesReady, snapshot.SlotsReady, snapshot.HashSlotCount)
	}

	slotHashSlots := make(map[uint32]uint16)
	for hashSlot := uint16(0); hashSlot < snapshot.HashSlotCount; hashSlot++ {
		route, err := n.RouteHashSlot(hashSlot)
		if err != nil {
			return fmt.Errorf("write probe route hash_slot=%d: %w", hashSlot, err)
		}
		if route.Leader == 0 {
			return fmt.Errorf("write probe route hash_slot=%d slot=%d: %w", hashSlot, route.SlotID, ErrNoSlotLeader)
		}
		if _, ok := slotHashSlots[route.SlotID]; !ok {
			slotHashSlots[route.SlotID] = hashSlot
		}
	}

	slotIDs := make([]uint32, 0, len(slotHashSlots))
	for slotID := range slotHashSlots {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })

	command := metafsm.EncodeNoopCommand()
	for _, slotID := range slotIDs {
		hashSlot := slotHashSlots[slotID]
		err := n.Propose(ctx, ProposeRequest{
			Command: command,
			Target: ProposeTarget{
				HashSlot:    hashSlot,
				HasHashSlot: true,
				SlotID:      slotID,
				HasSlotID:   true,
			},
		})
		if err != nil {
			return fmt.Errorf("write probe slot=%d hash_slot=%d: %w", slotID, hashSlot, err)
		}
	}
	return nil
}
