package cluster

import (
	"time"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type ChannelListener struct {
	channels *channelQueue
	readyCh  chan struct{}
	stopper  *syncutil.Stopper
	// 已准备的频道
	readyChannels *readyChannelQueue
	opts          *Options
	wklog.Log
}

func NewChannelListener(opts *Options) *ChannelListener {
	return &ChannelListener{
		channels:      newChannelQueue(),
		readyChannels: newReadyChannelQueue(),
		readyCh:       make(chan struct{}),
		stopper:       syncutil.NewStopper(),
		opts:          opts,
		Log:           wklog.NewWKLog("ChannelListener"),
	}
}

func (c *ChannelListener) wait() channelReady {
	if c.readyChannels.len() > 0 {
		return c.readyChannels.pop()
	}
	select {
	case <-c.readyCh:
	case <-c.stopper.ShouldStop():
		return channelReady{}
	}
	if c.readyChannels.len() > 0 {
		return c.readyChannels.pop()
	}
	return channelReady{}
}

func (c *ChannelListener) start() error {
	c.stopper.RunWorker(c.loopEvent)
	return nil
}

func (c *ChannelListener) stop() {
	c.stopper.Stop()
}

func (c *ChannelListener) Add(ch *channel) {
	c.channels.add(ch)
}

func (c *ChannelListener) Remove(ch *channel) {
	c.channels.remove(ch)
}

func (c *ChannelListener) Exist(channelID string, channelType uint8) bool {
	return c.channels.exist(channelID, channelType)
}

func (c *ChannelListener) Get(channelID string, channelType uint8) *channel {
	return c.channels.get(channelID, channelType)
}

func (c *ChannelListener) loopEvent() {
	tick := time.NewTicker(time.Millisecond * 10)
	hasEvent := false
	for {
		select {
		case <-tick.C:
			c.channels.foreach(func(ch *channel) {
				if ch.isDestroy() {
					return
				}
				if ch.hasReady() {
					rd := ch.ready()
					if replica.IsEmptyReady(rd) {
						return
					}
					c.readyChannels.add(channelReady{
						channel: ch,
						Ready:   rd,
					})
					hasEvent = true

				} else {
					if c.isInactiveChannel(ch) { // 频道不活跃，移除，等待频道再此收到消息时，重新加入
						c.Remove(ch)
						c.Info("remove inactive channel", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
					}
				}
			})
			if hasEvent {
				hasEvent = false
				select {
				case c.readyCh <- struct{}{}:
				case <-c.stopper.ShouldStop():
					return
				}
			}

		case <-c.stopper.ShouldStop():
			return
		}
	}
}

// 判断是否是不活跃的频道
func (c *ChannelListener) isInactiveChannel(channel *channel) bool {
	return channel.isDestroy() || channel.lastActivity.Add(c.opts.ChannelInactiveTimeout).Before(time.Now())
}
