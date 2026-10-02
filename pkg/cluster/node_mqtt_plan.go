package cluster

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// PlanChannelMQTTReplay preserves foreground admission before fresh Slot routing
// and a reactor-owned coherent read. It returns no source-release authority.
func (n *Node) PlanChannelMQTTReplay(ctx context.Context, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	if n.channels == nil {
		return ch.MQTTReplayPlan{}, ErrNotStarted
	}
	p, ok := n.channels.(ch.MQTTReplayPlanner)
	if !ok {
		return ch.MQTTReplayPlan{}, ch.ErrInvalidConfig
	}
	return p.PlanMQTTReplay(ctx, q)
}
