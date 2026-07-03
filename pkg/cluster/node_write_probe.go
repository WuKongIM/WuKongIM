package cluster

import (
	"context"
	"fmt"
	"sort"
	"time"

	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

const maxWriteProbePhysicalSlots = 4

// ProbeWriteReady verifies that Slot metadata writes and new ChannelV2 placement can proceed.
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
	if err := n.probeChannelPlacementReady(); err != nil {
		if refreshErr := n.refreshControlSnapshot(ctx); refreshErr != nil {
			return fmt.Errorf("%w: refresh control snapshot: %v", err, refreshErr)
		}
		snapshot = n.Snapshot()
		if !snapshot.RoutesReady || !snapshot.SlotsReady || snapshot.HashSlotCount == 0 {
			return fmt.Errorf("%w: routes=%t slots=%t hashSlotCount=%d", ErrRouteNotReady, snapshot.RoutesReady, snapshot.SlotsReady, snapshot.HashSlotCount)
		}
		if err := n.probeChannelPlacementReady(); err != nil {
			return err
		}
	}
	slotHashSlots := make(map[uint32]uint16)
	slotLeaders := make(map[uint32]uint64)
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
			slotLeaders[route.SlotID] = route.Leader
		}
	}

	slotIDs := selectWriteProbeSlotIDs(slotHashSlots, slotLeaders, n.cfg.NodeID)

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
	n.markChannelDataPlaneLeaseVisible()
	return nil
}

func (n *Node) markChannelDataPlaneLeaseVisible() {
	if n == nil || n.channels == nil || n.channelDataPlaneLease == nil {
		return
	}
	n.channelDataPlaneLease.MarkVisible(time.Now())
}

func selectWriteProbeSlotIDs(slotHashSlots map[uint32]uint16, slotLeaders map[uint32]uint64, localNodeID uint64) []uint32 {
	slotIDs := make([]uint32, 0, len(slotHashSlots))
	for slotID := range slotHashSlots {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	if len(slotIDs) <= maxWriteProbePhysicalSlots {
		return slotIDs
	}

	selected := make(map[uint32]struct{}, maxWriteProbePhysicalSlots)
	add := func(slotID uint32) {
		if len(selected) >= maxWriteProbePhysicalSlots {
			return
		}
		selected[slotID] = struct{}{}
	}
	for _, slotID := range slotIDs {
		if slotLeaders[slotID] == localNodeID {
			add(slotID)
			break
		}
	}
	for _, slotID := range slotIDs {
		leader := slotLeaders[slotID]
		if leader != 0 && leader != localNodeID {
			add(slotID)
			break
		}
	}
	for _, slotID := range slotIDs {
		add(slotID)
	}

	out := make([]uint32, 0, len(selected))
	for slotID := range selected {
		out = append(out, slotID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (n *Node) probeChannelPlacementReady() error {
	replicaCount := int(n.cfg.Channel.ReplicaCount)
	candidates := n.channelDataNodes.DataNodes()
	seen := make(map[uint64]struct{}, len(candidates))
	for _, nodeID := range candidates {
		seen[nodeID] = struct{}{}
	}
	if len(seen) < replicaCount {
		return fmt.Errorf("%w: channel placement candidates %d below replica count %d", ErrRouteNotReady, len(seen), replicaCount)
	}
	return nil
}
