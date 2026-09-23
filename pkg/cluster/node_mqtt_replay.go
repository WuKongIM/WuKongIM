package cluster

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// PrepareChannelMQTTReplay routes shared-content preparation while preserving
// foreground admission and caller-selected fences. It returns no quorum receipt.
func (n *Node) PrepareChannelMQTTReplay(ctx context.Context, req ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if n.channels == nil {
		return ch.MQTTReplayPage{}, ErrNotStarted
	}
	preparer, ok := n.channels.(ch.MQTTReplayPreparer)
	if !ok {
		return ch.MQTTReplayPage{}, ch.ErrInvalidConfig
	}
	return preparer.PrepareMQTTReplay(ctx, req)
}
