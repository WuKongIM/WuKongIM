package clusterevent

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (c *ClusterEventManager) loop() {
	tick := time.NewTicker(c.opts.Heartbeat)
	updateCount := 0
	maxCfgCheckCount := 4  // 集群配置版本最大检查次数
	currCfgCheckCount := 0 // 当前已检查次数
	for {
		select {
		case <-c.stopper.ShouldStop():
			return
		case <-tick.C:

			if c.IsNodeLeader() {
				// 只有过半的节点同步过配置后，主节点才会继续检查配置
				updateCount = 0
				currCfgCheckCount++
				if len(c.clusterconfig.Nodes) > 1 && c.GetClusterConfigVersion() > 0 {
					c.othersNodeConfigVersionMapLock.RLock()
					for _, cfgVersion := range c.othersNodeConfigVersionMap {
						if cfgVersion >= c.GetClusterConfigVersion() {
							updateCount++
						}
					}
					c.othersNodeConfigVersionMapLock.RUnlock()
					if updateCount < len(c.clusterconfig.Nodes)/2 {
						if currCfgCheckCount > maxCfgCheckCount {
							c.Warn("过半的节点没有同步领导的配置。", zap.Uint32("leaderConfigVersion", c.GetClusterConfigVersion()), zap.Any("othersConfigVersion", c.othersNodeConfigVersionMap))
							currCfgCheckCount = 0
						}
						break
					}
				}
				currCfgCheckCount = 0
			}

			// 检查节点配置
			clusterEvent := c.checkNodes()
			if !IsEmptyClusterEvent(clusterEvent) {
				c.watchCh <- clusterEvent
				break
			}

			// 检查slots配置
			clusterEvent = c.checkSlots()
			if !IsEmptyClusterEvent(clusterEvent) {
				c.watchCh <- clusterEvent
				break
			}
		}
	}
}

func (c *ClusterEventManager) checkNodes() ClusterEvent {

	return EmptyClusterEvent
}

func (c *ClusterEventManager) checkSlots() ClusterEvent {

	if !c.IsNodeLeader() {
		return EmptyClusterEvent
	}

	c.clusterconfigLock.Lock()
	defer c.clusterconfigLock.Unlock()
	if len(c.clusterconfig.Nodes) == 0 {
		return EmptyClusterEvent
	}

	if len(c.clusterconfig.Slots) == 0 {
		initNodes := c.getInitNodes()
		if len(initNodes) == 0 {
			return EmptyClusterEvent
		}
		nodeOffsetIndex := 0
		slots := make([]*pb.Slot, 0, c.clusterconfig.SlotCount)
		for i := 0; i < int(c.clusterconfig.SlotCount); i++ {
			if nodeOffsetIndex >= len(initNodes) {
				nodeOffsetIndex = 0
			}
			replicas := make([]uint64, 0, c.opts.SlotReplicaCount)

			if len(initNodes) <= int(c.opts.SlotReplicaCount) {
				for _, node := range initNodes {
					replicas = append(replicas, node.Id)
				}
			} else {
				offset := nodeOffsetIndex
				for j := 0; j < int(c.opts.SlotReplicaCount); j++ {
					replicas = append(replicas, initNodes[offset].Id)
					offset++
					if offset >= len(initNodes) {
						offset = 0
					}
				}
			}

			slots = append(slots, &pb.Slot{
				Id:           uint32(i),
				ReplicaCount: c.opts.SlotReplicaCount,
				Replicas:     replicas,
				Leader:       initNodes[nodeOffsetIndex].Id,
			})
			nodeOffsetIndex++

		}
		return ClusterEvent{
			SlotEvent: &pb.SlotEvent{
				Slots:     slots,
				EventType: pb.SlotEventType_SlotEventTypeInit,
			},
		}
	}
	offlineNodeIDs := c.offlineNodeIDs()
	if len(offlineNodeIDs) > 0 {
		slotIDs := make([]uint32, 0)
		for _, slot := range c.clusterconfig.Slots {
			if wkutil.ArrayContainsUint64(offlineNodeIDs, slot.Leader) {
				slotIDs = append(slotIDs, slot.Id)
			}
		}
		if len(slotIDs) > 0 {
			return ClusterEvent{
				SlotEvent: &pb.SlotEvent{
					EventType: pb.SlotEventType_SlotEventTypeElection, // 选举
					SlotIDs:   slotIDs,
				},
			}
		}
	}

	return EmptyClusterEvent
}

// 获取初始节点
func (c *ClusterEventManager) getInitNodes() []*pb.Node {
	initNodes := make([]*pb.Node, 0, len(c.clusterconfig.Nodes))
	for _, n := range c.clusterconfig.Nodes {
		if !n.Join {
			initNodes = append(initNodes, n)
		}

	}
	return initNodes
}

// 获取离线的节点
func (c *ClusterEventManager) offlineNodeIDs() []uint64 {
	nodeIDs := make([]uint64, 0, len(c.clusterconfig.Nodes))
	for _, node := range c.clusterconfig.Nodes {
		if !node.Online {
			nodeIDs = append(nodeIDs, node.Id)
		}
	}
	return nodeIDs
}
