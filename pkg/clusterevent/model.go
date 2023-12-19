package clusterevent

import "github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"

// cluster事件
type ClusterEvent struct {
	SlotEvent *pb.SlotEvent
	NodeEvent *pb.NodeEvent
}
