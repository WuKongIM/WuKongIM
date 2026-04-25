package messageid

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/bwmarrin/snowflake"
)

const MaxNodeID uint64 = 1023

type SnowflakeGenerator struct {
	node *snowflake.Node
}

var _ channel.MessageIDGenerator = (*SnowflakeGenerator)(nil)

func NewSnowflakeGenerator(nodeID uint64) (*SnowflakeGenerator, error) {
	if nodeID > MaxNodeID {
		return nil, fmt.Errorf("messageid: node id %d exceeds snowflake max %d", nodeID, MaxNodeID)
	}

	node, err := snowflake.NewNode(int64(nodeID))
	if err != nil {
		return nil, fmt.Errorf("messageid: create snowflake node: %w", err)
	}
	return &SnowflakeGenerator{node: node}, nil
}

func (g *SnowflakeGenerator) Next() uint64 {
	return uint64(g.node.Generate())
}
