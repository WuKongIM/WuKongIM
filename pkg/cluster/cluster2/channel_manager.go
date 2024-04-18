package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type channelManager struct {
	channelReactor *reactor.Reactor
	opts           *Options
}

func newChannelManager(opts *Options) *channelManager {
	cm := &channelManager{
		opts: opts,
	}
	cm.channelReactor = reactor.New(reactor.NewOptions(
		reactor.WithNodeId(opts.NodeId),
		reactor.WithSend(cm.onSend),
	))
	return cm
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

func (c *channelManager) get(channelId string, channelType uint8) reactor.IHandler {
	return c.channelReactor.Handler(ChannelToKey(channelId, channelType))
}

func (c *channelManager) exist(channelId string, channelType uint8) bool {
	return c.channelReactor.ExistHandler(ChannelToKey(channelId, channelType))
}

func (c *channelManager) getWithHandleKey(handleKey string) reactor.IHandler {
	return c.channelReactor.Handler(handleKey)
}

func (c *channelManager) proposeAndWait(ctx context.Context, channelId string, channelType uint8, logs []replica.Log) ([]reactor.ProposeResult, error) {
	return c.channelReactor.ProposeAndWait(ctx, ChannelToKey(channelId, channelType), logs)
}

func (c *channelManager) addMessage(m reactor.Message) {
	c.channelReactor.AddMessage(m)
}

func (c *channelManager) onSend(m reactor.Message) {
	c.opts.Send(ShardTypeChannel, m)
}
