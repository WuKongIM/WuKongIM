package cluster

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// GetUserChannelMemberships delegates a bounded exact-key read to the UID Slot authority.
func (n *Node) GetUserChannelMemberships(ctx context.Context, uid string, keys []metadb.ChannelKey) ([]metadb.UserChannelMembership, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotProxy == nil {
		return nil, ErrNotStarted
	}
	return n.defaultSlotProxy.GetUserChannelMemberships(ctx, uid, keys)
}
