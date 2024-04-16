package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type channelManager struct {
	channelReactor *reactor.Reactor
}

func newChannelManager(opts *Options) *channelManager {
	channelReactor := reactor.New(reactor.NewOptions(reactor.WithNodeId(opts.NodeId)))
	return &channelManager{
		channelReactor: channelReactor,
	}
}

func (c *channelManager) start() error {
	return c.channelReactor.Start()
}

func (c *channelManager) stop() {
	c.channelReactor.Stop()
}

func (c *channelManager) add(ch *channel) {
	c.channelReactor.AddHandler(ch.key, ch)
}

func (c *channelManager) remove(ch *channel) {
	c.channelReactor.RemoveHandler(ch.key)
}

func (c *channelManager) get(channelKey string) reactor.IHandler {
	return c.channelReactor.Handler(channelKey)
}

func (c *channelManager) propose(ctx context.Context, channelKey string, logs []replica.Log) ([]*reactor.ProposeResult, error) {
	return c.channelReactor.ProposeAndWait(ctx, channelKey, logs)
}
