package app

import (
	"context"

	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type strictNodeLivenessSource interface {
	ListNodesStrict(ctx context.Context) ([]controllermeta.ClusterNode, error)
}

// UpdateNodeLiveness stores the latest known controller-observed status for a node.
func (s *channelMetaSync) UpdateNodeLiveness(nodeID uint64, status controllermeta.NodeStatus) {
	if s == nil {
		return
	}
	if s.liveness.Update(nodeID, status) {
		s.scheduleLeaderHealthRefresh(nodeID)
	}
}

func (s *channelMetaSync) nodeLivenessStatus(nodeID uint64) (controllermeta.NodeStatus, bool) {
	if s == nil {
		return controllermeta.NodeStatusUnknown, false
	}
	return s.liveness.Status(nodeID)
}

func (s *channelMetaSync) warmNodeLiveness(ctx context.Context, nodeID uint64) {
	if s == nil {
		return
	}
	s.liveness.Warm(ctx, nodeID, s.nodeLivenessSource())
}

func (s *channelMetaSync) nodeLivenessSource() runtimechannelmeta.LivenessSource {
	if s == nil || s.bootstrap == nil || s.bootstrap.cluster == nil {
		return nil
	}
	source, _ := s.bootstrap.cluster.(strictNodeLivenessSource)
	return source
}

// scheduleLeaderHealthRefresh re-reads affected active local channels when the
// controller observes their current leader as dead or draining.
func (s *channelMetaSync) scheduleLeaderHealthRefresh(nodeID uint64) {
	if s == nil || nodeID == 0 {
		return
	}
	applied := s.snapshotAppliedLocal()
	if len(applied) == 0 {
		return
	}
	affectedSlots := make(map[multiraft.SlotID]struct{})
	for key := range applied {
		if s.localRuntime != nil {
			handle, ok := s.localRuntime.Channel(key)
			if !ok || uint64(handle.Meta().Leader) != nodeID {
				continue
			}
		}
		slotID, ok := s.slotForChannelKey(key)
		if !ok {
			continue
		}
		affectedSlots[slotID] = struct{}{}
	}
	for slotID := range affectedSlots {
		s.scheduleSlotLeaderRefresh(slotID)
	}
}

func (s *channelMetaSync) needsLeaderRepair(meta metadb.ChannelRuntimeMeta) (bool, string) {
	if meta.Status != uint8(channel.StatusActive) {
		return false, ""
	}
	if meta.Leader == 0 {
		return true, channel.LeaderRepairReasonLeaderMissing.String()
	}
	if !containsUint64(meta.Replicas, meta.Leader) {
		return true, channel.LeaderRepairReasonLeaderNotReplica.String()
	}
	status, ok := s.nodeLivenessStatus(meta.Leader)
	if ok {
		switch status {
		case controllermeta.NodeStatusDead:
			return true, channel.LeaderRepairReasonLeaderDead.String()
		case controllermeta.NodeStatusDraining:
			return true, channel.LeaderRepairReasonLeaderDraining.String()
		}
	}
	if reason := s.localRuntimeLeaderRepairReason(meta); reason != "" {
		return true, reason
	}
	if runtimechannelmeta.MetaLeaseNeedsRenewal(meta.LeaseUntilMS, s.now().UTC(), 0) {
		return true, channel.LeaderRepairReasonLeaderLeaseExpired.String()
	}
	return false, ""
}

func (s *channelMetaSync) localRuntimeLeaderRepairReason(meta metadb.ChannelRuntimeMeta) string {
	if s == nil || s.localRuntime == nil {
		return ""
	}
	key := channelhandler.KeyFromChannelID(channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)})
	handle, ok := s.localRuntime.Channel(key)
	if !ok {
		return ""
	}
	return observedLeaderRepairReason(handle.Meta(), handle.Status())
}

func observedLeaderRepairReason(meta channel.Meta, state channel.ReplicaState) string {
	return runtimechannelmeta.ObservedLeaderRepairReason(meta, state)
}

func (s *channelMetaSync) scheduleLeaderRepairForMeta(meta channel.Meta) {
	if s == nil || s.repairer == nil || meta.Status != channel.StatusActive || meta.Leader == 0 {
		return
	}
	status, ok := s.nodeLivenessStatus(uint64(meta.Leader))
	if !ok || (status != controllermeta.NodeStatusDead && status != controllermeta.NodeStatusDraining) {
		return
	}
	slotID, ok := s.slotForChannelKey(meta.Key)
	if !ok {
		return
	}
	s.scheduleSlotLeaderRefresh(slotID)
}
