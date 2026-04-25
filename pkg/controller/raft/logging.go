package raft

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func newEtcdRaftLogger(logger wklog.Logger, nodeID uint64) *wklog.RaftLogger {
	if logger == nil {
		logger = wklog.NewNop()
	}
	return wklog.NewRaftLogger(
		logger.Named("raft"),
		wklog.RaftScope("controller"),
		wklog.NodeID(nodeID),
	)
}
