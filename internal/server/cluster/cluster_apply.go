package cluster

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/pb"
	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

// slot消息的应用
func (c *Cluster) Apply(slot uint32, enties []raftpb.Entry) error {

	return nil
}

// 节点消息的应用
func (c *Cluster) nodeApply(entries []raftpb.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		switch entry.Type {
		case raftpb.EntryConfChange:
			var cc raftpb.ConfChange
			err := cc.Unmarshal(entry.Data)
			if err != nil {
				return err
			}
			if cc.Type == raftpb.ConfChangeAddNode {
				resultMap, err := wkutil.JSONToMap(string(cc.Context))
				if err != nil {
					return err
				}
				addr := resultMap["addr"].(string)
				err = c.clusterManager.AddNewNode(cc.NodeID, addr)
				if err != nil {
					return err
				}
			}
		case raftpb.EntryNormal:
			if len(entry.Data) == 0 {
				continue
			}
			var req pb.CMDReq
			err := req.Unmarshal(entry.Data)
			if err != nil {
				return err
			}
			c.handleCMDReq(req)

		}
	}
	lastAppiedIndex := entries[len(entries)-1].Index
	if c.appiedIndex.Load() < lastAppiedIndex {
		err := c.SetApplied(lastAppiedIndex)
		if err != nil {
			c.Warn("set applied error", zap.Error(err))
		}
		c.appiedIndex.Store(lastAppiedIndex)
	}

	return nil
}

func (c *Cluster) handleCMDReq(req pb.CMDReq) {
	switch req.Type {
	case pb.CMDAllocateSlot.Uint32():
		c.handleAllocateSlots(req)
	}
}

func (c *Cluster) handleAllocateSlots(req pb.CMDReq) {
	fmt.Println("handleAllocateSlots---->")
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
				Nodes:  slot.Nodes,
			}
			c.clusterManager.AddSlot(pbslot)
			if !c.slotRaftServer.ExistReplica(slot.Slot) {
				go c.startReplica(pbslot)
			}
		}

	}
	err = c.clusterManager.save()
	if err != nil {
		c.Error("handleAllocateSlots error", zap.Error(err))
		return
	}

}

func (c *Cluster) startReplica(slot *pb.Slot) {

	opts := multiraft.NewReplicaOptions()
	opts.PeerID = c.opts.NodeID
	peers := make([]multiraft.Peer, 0, len(slot.Nodes))
	for _, nodeID := range slot.Nodes {
		for _, node := range c.clusterManager.cluster.Nodes {
			if nodeID == node.NodeID {
				peers = append(peers, multiraft.Peer{ID: nodeID, Addr: node.ServerAddr})
				break
			}
		}
	}
	opts.Peers = peers
	opts.MaxReplicaCount = uint32(c.opts.ReplicaCount)
	opts.LeaderChange = func(newLeaderID uint64, oldLeaderID uint64) {
		if newLeaderID != oldLeaderID {
			c.clusterManager.SetSlotLeader(slot.Slot, newLeaderID)
		}
	}
	_, err := c.slotRaftServer.StartReplica(slot.Slot, opts)
	if err != nil {
		c.Error("startReplica error", zap.Error(err), zap.Uint32("slot", slot.Slot), zap.Uint64("leader", slot.Leader), zap.Uint64s("nodes", slot.Nodes))
		return
	}
}
