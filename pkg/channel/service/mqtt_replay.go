package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
)

var _ ch.MQTTReplayPreparer = (*cluster)(nil)

// PrepareMQTTReplay keeps the Channel reserved through one bounded worker page.
// The caller must supply fresh authority; local content is not a quorum receipt.
func (c *cluster) PrepareMQTTReplay(ctx context.Context, req ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !req.Valid() {
		return ch.MQTTReplayPage{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	key := ch.ChannelKeyForID(req.ChannelID)
	release, err := c.group.ReserveAppend(key)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	defer release()
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventMQTTReplay, Key: key, Context: ctx, MQTTReplay: req})
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	return result.MQTTReplay, nil
}
