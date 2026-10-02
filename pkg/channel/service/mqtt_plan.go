package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
)

// PlanMQTTReplay reserves the runtime until a fenced captured-HW read completes.
// A cluster entry must surround this local facade with fresh Slot authority.
func (c *cluster) PlanMQTTReplay(ctx context.Context, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !q.Valid() {
		return ch.MQTTReplayPlan{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	key := ch.ChannelKeyForID(q.ChannelID)
	release, err := c.group.ReserveAppend(key)
	if err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	defer release()
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventMQTTPlan, Key: key, Context: ctx, MQTTPlan: q})
	if err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	return result.MQTTPlan, nil
}

var _ ch.MQTTReplayPlanner = (*cluster)(nil)
