package cluster

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// RepairChannelMQTTReplay preserves foreground admission before exact-replica
// recovery. It does not publish source release or migration-readiness proof.
func (n *Node) RepairChannelMQTTReplay(ctx context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPrefix, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTReplayPrefix{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTReplayPrefix{}, err
	}
	if n.channels == nil {
		return ch.MQTTReplayPrefix{}, ErrNotStarted
	}
	repair, ok := n.channels.(ch.MQTTReplayRepairer)
	if !ok {
		return ch.MQTTReplayPrefix{}, ch.ErrInvalidConfig
	}
	return repair.RepairMQTTReplay(ctx, q)
}
