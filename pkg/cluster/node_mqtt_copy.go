package cluster

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// CopyChannelMQTTReplay confirms current-quorum shared durability under normal
// foreground admission. The receipt alone never authorizes source release.
func (n *Node) CopyChannelMQTTReplay(ctx context.Context, q ch.MQTTReplayRequest) (ch.MQTTReplayCopyReceipt, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTReplayCopyReceipt{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTReplayCopyReceipt{}, err
	}
	if n.channels == nil {
		return ch.MQTTReplayCopyReceipt{}, ErrNotStarted
	}
	copier, ok := n.channels.(ch.MQTTReplayCopier)
	if !ok {
		return ch.MQTTReplayCopyReceipt{}, ch.ErrInvalidConfig
	}
	return copier.CopyMQTTReplay(ctx, q)
}
