package backup

import (
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

const (
	maxRestoreInstallNodes    = 1024
	maxRestoreInstallParallel = 8
)

// restoreReplicaPlacement resolves each logical hash slot to the desired
// replica set in one validated target control snapshot.
type restoreReplicaPlacement struct {
	// slotByHashSlot is the validated logical-to-physical Slot routing table.
	slotByHashSlot []uint32
	// peersBySlot contains the validated desired replicas for each physical Slot.
	peersBySlot map[uint32][]uint64
}

func newRestoreReplicaPlacement(snapshot control.Snapshot, hashSlotCount uint16, localNodeID uint64) (*restoreReplicaPlacement, error) {
	if hashSlotCount == 0 || snapshot.HashSlots.Count != hashSlotCount || localNodeID == 0 || len(snapshot.Nodes) == 0 || len(snapshot.Nodes) > maxRestoreInstallNodes {
		return nil, fmt.Errorf("restore target topology is invalid")
	}
	table, err := routing.BuildTable(snapshot)
	if err != nil {
		return nil, fmt.Errorf("restore target topology: %w", err)
	}
	localCurrent := false
	for _, node := range snapshot.Nodes {
		if node.NodeID == localNodeID && node.JoinState != control.NodeJoinStateRemoved {
			localCurrent = true
			break
		}
	}
	if !localCurrent {
		return nil, fmt.Errorf("local node is outside current membership")
	}

	peersBySlot := make(map[uint32][]uint64, len(table.SlotPeers))
	for slotID, desiredPeers := range table.SlotPeers {
		if len(desiredPeers) == 0 || len(desiredPeers) > maxRestoreInstallNodes {
			return nil, fmt.Errorf("restore target contains an invalid Slot assignment")
		}
		peers := append([]uint64(nil), desiredPeers...)
		sort.Slice(peers, func(left, right int) bool { return peers[left] < peers[right] })
		peersBySlot[slotID] = peers
	}
	return &restoreReplicaPlacement{slotByHashSlot: append([]uint32(nil), table.HashToSlot...), peersBySlot: peersBySlot}, nil
}

func (p *restoreReplicaPlacement) nodeIDs(hashSlot uint16) ([]uint64, error) {
	if p == nil || int(hashSlot) >= len(p.slotByHashSlot) {
		return nil, fmt.Errorf("restore hash slot is outside target topology")
	}
	peers := p.peersBySlot[p.slotByHashSlot[hashSlot]]
	if len(peers) == 0 {
		return nil, fmt.Errorf("restore hash slot has no target replicas")
	}
	return peers, nil
}
