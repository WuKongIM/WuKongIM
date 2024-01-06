package clusterevent

import "github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"

// cluster事件
type ClusterEvent struct {
	ClusterEventType pb.ClusterEventType
	SlotEvent        *pb.SlotEvent
	NodeEvent        *pb.NodeEvent
}

var EmptyClusterEvent = ClusterEvent{}

func IsEmptyClusterEvent(event ClusterEvent) bool {
	return event.SlotEvent == nil && event.NodeEvent == nil && event.ClusterEventType == pb.ClusterEventType_ClusterEventTypeNone
}

func (c *ClusterEvent) String() string {
	if c.SlotEvent != nil {
		return c.SlotEvent.String()
	}
	if c.NodeEvent != nil {
		return c.NodeEvent.String()
	}
	return c.ClusterEventType.String()
}
