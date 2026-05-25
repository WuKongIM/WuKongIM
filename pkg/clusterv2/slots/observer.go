package slots

import "github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"

// StatusReader reads local Multi-Raft Slot status.
type StatusReader interface {
	// Status returns local Slot status for slotID.
	Status(multiraft.SlotID) (multiraft.Status, error)
}

// StatusSnapshot maps local Multi-Raft statuses into clusterv2 Slot statuses.
func StatusSnapshot(reader StatusReader, slotIDs []uint32) []Status {
	out := make([]Status, 0, len(slotIDs))
	if reader == nil {
		return out
	}
	for _, slotID := range slotIDs {
		status, err := reader.Status(multiraft.SlotID(slotID))
		if err != nil {
			continue
		}
		item := Status{SlotID: uint32(status.SlotID), Leader: uint64(status.LeaderID), Peers: make([]uint64, 0, len(status.CurrentVoters))}
		for _, peer := range status.CurrentVoters {
			item.Peers = append(item.Peers, uint64(peer))
		}
		out = append(out, item)
	}
	return out
}
