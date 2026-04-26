package app

import (
	"time"

	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

// UpdateNodeLiveness stores the latest known controller-observed status for a node.
func (s *channelMetaSync) UpdateNodeLiveness(nodeID uint64, status controllermeta.NodeStatus) {
	if s == nil || s.resolver == nil {
		return
	}
	s.resolver.UpdateNodeLiveness(nodeID, status)
}

func (s *channelMetaSync) nodeLivenessStatus(nodeID uint64) (controllermeta.NodeStatus, bool) {
	if s == nil || s.resolver == nil {
		return controllermeta.NodeStatusUnknown, false
	}
	return s.resolver.NodeLivenessStatus(nodeID)
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
	if s == nil || s.resolver == nil {
		return ""
	}
	return s.resolver.LocalRuntimeLeaderRepairReason(meta)
}

func (s *channelMetaSync) scheduleLeaderRepairForMeta(meta channel.Meta) {
	if s == nil || meta.Status != channel.StatusActive || meta.Leader == 0 {
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

func (s *channelMetaSync) now() time.Time {
	if s == nil || s.resolver == nil {
		return time.Now()
	}
	return s.resolver.Now()
}
