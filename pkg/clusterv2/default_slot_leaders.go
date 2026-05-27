package clusterv2

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/slots"
)

// startSlotLeaderLoop publishes local default Slot leadership into the foreground router.
func (n *Node) startSlotLeaderLoop() {
	if n == nil || n.defaultSlotRuntime == nil || n.slotLeaderCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.slotLeaderCancel = cancel
	n.slotLeaderWG.Add(1)
	go func() {
		defer n.slotLeaderWG.Done()
		ticker := time.NewTicker(defaultSlotLeaderPollInterval)
		defer ticker.Stop()
		for {
			n.refreshDefaultSlotLeaders()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// stopSlotLeaderLoop stops the default Slot leadership publisher.
func (n *Node) stopSlotLeaderLoop() {
	if n == nil || n.slotLeaderCancel == nil {
		return
	}
	n.slotLeaderCancel()
	n.slotLeaderWG.Wait()
	n.slotLeaderCancel = nil
}

// refreshDefaultSlotLeaders maps local Multi-Raft status into routing slot leaders.
func (n *Node) refreshDefaultSlotLeaders() {
	if n == nil || n.defaultSlotRuntime == nil || n.router == nil {
		return
	}
	slotIDs := n.currentSlotIDs()
	if len(slotIDs) == 0 {
		return
	}
	n.router.UpdateSlotLeaders(routingSlotStatuses(slots.StatusSnapshot(n.defaultSlotRuntime, slotIDs)))
}

func routingSlotStatuses(statuses []slots.Status) []routing.SlotStatus {
	out := make([]routing.SlotStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, routing.SlotStatus{SlotID: status.SlotID, Leader: status.Leader})
	}
	return out
}

// currentSlotIDs returns the physical Slots visible in the latest local control snapshot.
func (n *Node) currentSlotIDs() []uint32 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]uint32, 0, len(n.controlSnapshot.Slots))
	for _, slot := range n.controlSnapshot.Slots {
		out = append(out, slot.SlotID)
	}
	return out
}
