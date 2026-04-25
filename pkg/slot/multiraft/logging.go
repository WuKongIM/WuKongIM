package multiraft

import "github.com/WuKongIM/WuKongIM/pkg/wklog"

func newEtcdRaftLogger(logger wklog.Logger, nodeID NodeID, slotID SlotID) *wklog.RaftLogger {
	if logger == nil {
		logger = wklog.NewNop()
	}
	return wklog.NewRaftLogger(
		logger.Named("raft"),
		wklog.RaftScope("slot"),
		wklog.NodeID(uint64(nodeID)),
		wklog.SlotID(uint64(slotID)),
	)
}
