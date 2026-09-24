package cluster

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// CommitChannelMQTTReplayRetirement preserves foreground admission before fresh Slot
// routing and reactor-owned control commit. Consumer permission must be established by the product planner.
func (n *Node) CommitChannelMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	if n.channels == nil {
		return ch.MQTTReplayRetirementProof{}, ErrNotStarted
	}
	committer, ok := n.channels.(ch.MQTTReplayRetirementCommitter)
	if !ok {
		return ch.MQTTReplayRetirementProof{}, ch.ErrInvalidConfig
	}
	return committer.CommitMQTTReplayRetirement(ctx, q)
}
