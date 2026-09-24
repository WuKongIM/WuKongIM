package cluster

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// SelectChannelMQTTReplayRetirement preserves foreground admission before fresh
// Slot routing and bounded immutable journal reads. It grants no retirement permit.
func (n *Node) SelectChannelMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementSelectionRequest) (ch.MQTTReplayRetirementSelection, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTReplayRetirementSelection{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTReplayRetirementSelection{}, err
	}
	if n.channels == nil {
		return ch.MQTTReplayRetirementSelection{}, ErrNotStarted
	}
	p, ok := n.channels.(ch.MQTTReplayRetirementSelector)
	if !ok {
		return ch.MQTTReplayRetirementSelection{}, ch.ErrInvalidConfig
	}
	return p.SelectMQTTReplayRetirement(ctx, q)
}
