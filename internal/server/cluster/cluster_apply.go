package cluster

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/pb"
	sm "github.com/lni/dragonboat/v4/statemachine"
	"go.uber.org/zap"
)

// 节点消息的应用
func (c *Cluster) onNodeApply(entries []sm.Entry) error {

	// if len(entries) == 0 {
	// 	return nil
	// }
	// for _, entry := range entries {
	// 	switch entry.Type {
	// 	case raftpb.EntryConfChange:
	// 		var cc raftpb.ConfChange
	// 		err := cc.Unmarshal(entry.Data)
	// 		if err != nil {
	// 			return err
	// 		}
	// 		if cc.Type == raftpb.ConfChangeAddNode {
	// 			resultMap, err := wkutil.JSONToMap(string(cc.Context))
	// 			if err != nil {
	// 				return err
	// 			}
	// 			addr := resultMap["addr"].(string)
	// 			err = c.clusterManager.AddNewNode(cc.NodeID, addr)
	// 			if err != nil {
	// 				return err
	// 			}
	// 		}
	// 	case raftpb.EntryNormal:
	// 		if len(entry.Data) == 0 {
	// 			continue
	// 		}
	// 		var req pb.CMDReq
	// 		err := req.Unmarshal(entry.Data)
	// 		if err != nil {
	// 			return err
	// 		}
	// 		c.handleCMDReq(req)

	// 	}
	// }
	// lastAppiedIndex := entries[len(entries)-1].Index
	// if c.appiedIndex.Load() < lastAppiedIndex {
	// 	err := c.SetApplied(lastAppiedIndex)
	// 	if err != nil {
	// 		c.Warn("set applied error", zap.Error(err))
	// 	}
	// 	c.appiedIndex.Store(lastAppiedIndex)
	// }
	for _, entry := range entries {
		var req pb.CMDReq
		err := req.Unmarshal(entry.Cmd)
		if err != nil {
			return err
		}
		c.handleCMDReq(req)
	}

	return nil
}

func (c *Cluster) handleCMDReq(req pb.CMDReq) {
	fmt.Println("onNodeApply------->", pb.CMDType(req.Type).String())
	switch req.Type {
	case pb.CMDAllocateSlot.Uint32():
		c.handleAllocateSlots(req)
	case pb.CMDUpdateClusterConfig.Uint32():
		c.handleUpdateClusterConfig(req)
	case pb.CMDUpdatePeerConfig.Uint32():
		c.handleUpdatePeerConfig(req)
	case pb.CMDUpdateSlotLeaderRelationSet.Uint32():
		c.handleUpdateSlotLeaderRelationSet(req)
	}
}

func (c *Cluster) handleUpdatePeerConfig(req pb.CMDReq) {
	peer := &pb.Peer{}
	err := peer.Unmarshal(req.Param)
	if err != nil {
		c.Error("handleUpdatePeerConfig error", zap.Error(err))
		return
	}
	c.peerGRPCClient.AddOrUpdatePeer(peer)
	c.clusterManager.AddOrUpdatePeerConfig(peer)
}

func (c *Cluster) handleUpdateClusterConfig(req pb.CMDReq) {
	ct := &pb.Cluster{}
	err := ct.Unmarshal(req.Param)
	if err != nil {
		c.Error("handleUpdateClusterConfig error", zap.Error(err))
		return
	}
	for _, peer := range ct.Peers {
		c.peerGRPCClient.AddOrUpdatePeer(peer)
	}
	c.clusterManager.UpdateClusterConfig(ct)

}

func (c *Cluster) handleUpdateSlotLeaderRelationSet(req pb.CMDReq) {
	slotLeaderRelationSet := &pb.SlotLeaderRelationSet{}
	err := slotLeaderRelationSet.Unmarshal(req.Param)
	if err != nil {
		c.Error("handleUpdateSlotLeaderRelationSet error", zap.Error(err))
		return
	}
	if len(slotLeaderRelationSet.SlotLeaderRelations) == 0 {
		return
	}
	for _, slotLeaderRelation := range slotLeaderRelationSet.SlotLeaderRelations {
		c.clusterManager.SetSlotLeader(slotLeaderRelation.Slot, slotLeaderRelation.Leader)
	}

}

func (c *Cluster) handleAllocateSlots(req pb.CMDReq) {
	allocateSlotSet := &pb.AllocateSlotSet{}
	err := allocateSlotSet.Unmarshal(req.Param)
	if err != nil {
		c.Error("handleAllocateSlots error", zap.Error(err))
		return
	}
	if len(allocateSlotSet.AllocateSlots) > 0 {
		for _, slot := range allocateSlotSet.AllocateSlots {
			pbslot := &pb.Slot{
				Slot:   slot.Slot,
				Leader: slot.LeaderID,
				Peers:  slot.Peers,
			}
			c.clusterManager.AddSlot(pbslot)

		}
		err = c.clusterManager.Save()
		if err != nil {
			c.Error("handleAllocateSlots error", zap.Error(err))
			return
		}

		// for _, allocateSlot := range allocateSlotSet.AllocateSlots {
		// 	c.startSlot(allocateSlot)
		// }
	}

}

func (c *Cluster) startSlot(slot *pb.Slot) {

	opts := NewSlotOptions()
	opts.PeerIDs = slot.Peers
	opts.MaxReplicaCount = uint32(c.opts.ReplicaCount)
	err := c.multiRaft.StartSlot(slot.Slot, opts)
	if err != nil {
		c.Error("startReplica error", zap.Error(err), zap.Uint32("slot", slot.Slot), zap.Uint64s("peers", slot.Peers))
		return
	}
}
