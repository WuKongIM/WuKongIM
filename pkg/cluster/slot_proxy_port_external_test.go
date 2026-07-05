package cluster_test

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

var _ slotproxy.Cluster = (*cluster.Node)(nil)
var _ interface {
	ProposeWithHashSlot(context.Context, multiraft.SlotID, uint16, []byte) error
	ProposeLocalWithHashSlot(context.Context, multiraft.SlotID, uint16, []byte) error
} = (*cluster.Node)(nil)
