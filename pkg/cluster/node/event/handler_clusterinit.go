package event

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"go.uber.org/zap"
)

// 分布式初始化事件
func (h *handler) handleClusterInit() {

	cfg := &types.Config{
		SlotCount:           h.cfgOptions.SlotCount,
		SlotReplicaCount:    h.cfgOptions.SlotMaxReplicaCount,
		ChannelReplicaCount: h.cfgOptions.ChannelMaxReplicaCount,
	}

	nodes := make([]*types.Node, 0, len(h.cfgOptions.InitNodes))

	opts := h.cfgOptions

	var replicas []uint64

	if len(opts.InitNodes) > 0 {
		for nodeId, addr := range opts.InitNodes {
			apiAddr := ""
			if nodeId == opts.NodeId {
				apiAddr = opts.ApiServerAddr
			}
			nodes = append(nodes, &types.Node{
				Id:            nodeId,
				ClusterAddr:   addr,
				ApiServerAddr: apiAddr,
				Online:        true,
				AllowVote:     true,
				Role:          types.NodeRole_NodeRoleReplica,
				Status:        types.NodeStatus_NodeStatusJoined,
				CreatedAt:     time.Now().Unix(),
			})
			replicas = append(replicas, nodeId)
		}
	} else { // 没有initNodes,则认为是单节点模式
		nodes = append(nodes, &types.Node{
			Id:            opts.NodeId,
			ApiServerAddr: opts.ApiServerAddr,
			Online:        true,
			AllowVote:     true,
			Role:          types.NodeRole_NodeRoleReplica,
			Status:        types.NodeStatus_NodeStatusJoined,
			CreatedAt:     time.Now().Unix(),
		})
		replicas = append(replicas, opts.NodeId)
	}

	cfg.Nodes = nodes

	if len(replicas) > 0 {
		offset := 0
		replicaCount := opts.SlotMaxReplicaCount
		for i := uint32(0); i < opts.SlotCount; i++ {
			slot := &types.Slot{
				Id: i,
			}
			if len(replicas) <= int(replicaCount) {
				slot.Replicas = replicas
			} else {
				slot.Replicas = make([]uint64, 0, replicaCount)
				for i := uint32(0); i < replicaCount; i++ {
					idx := (offset + int(i)) % len(replicas)
					slot.Replicas = append(slot.Replicas, replicas[idx])
				}
			}
			offset++
			// 随机选举一个领导者
			randomIndex := globalRand.Intn(len(slot.Replicas))
			slot.Term = 1
			slot.Leader = slot.Replicas[randomIndex]
			cfg.Slots = append(cfg.Slots, slot)
		}
	}

	// 自动均衡槽领导
	newSlots := h.autoBalanceSlotLeaders(cfg)
	for _, newSlot := range newSlots {
		for _, oldSlot := range cfg.Slots {
			if newSlot.Id == oldSlot.Id {
				if newSlot.MigrateFrom != 0 && newSlot.MigrateTo != 0 {
					oldSlot.Leader = newSlot.MigrateTo
				}
				break
			}
		}
	}

	// 提案初始配置
	err := h.cfgServer.ProposeConfig(cfg)
	if err != nil {
		h.Error("ProposeConfigInit failed", zap.Error(err))
	}
}
