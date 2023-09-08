package clusterset

import (
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
)

type node struct {
	nodeID uint64
}

func newNode(nodeID uint64, conn wknet.Conn) *node {
	return &node{
		nodeID: nodeID,
	}
}
