package app

import (
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func (s *channelMetaSync) scheduleSlotLeaderRefresh(slotID multiraft.SlotID) {
	if s == nil || s.resolver == nil {
		return
	}
	s.resolver.ScheduleSlotLeaderRefresh(slotID)
}

func (s *channelMetaSync) enqueueLocalReplicaStateChange(key channel.ChannelKey) {
	if s == nil || s.resolver == nil {
		return
	}
	s.resolver.EnqueueLocalReplicaStateChange(key)
}

func (s *channelMetaSync) slotForChannelKey(key channel.ChannelKey) (multiraft.SlotID, bool) {
	if s == nil || s.resolver == nil {
		return 0, false
	}
	return s.resolver.SlotForChannelKey(key)
}
