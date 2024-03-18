package cluster

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type ChannelListener struct {
	channels *channelQueue
	readyCh  chan channelReady
	stopper  *syncutil.Stopper
	// 已准备的频道
	opts *Options
	wklog.Log
}

func NewChannelListener(opts *Options) *ChannelListener {
	return &ChannelListener{
		channels: newChannelQueue(),
		readyCh:  make(chan channelReady, 100),
		stopper:  syncutil.NewStopper(),
		opts:     opts,
		Log:      wklog.NewWKLog("ChannelListener"),
	}
}

func (c *ChannelListener) wait() channelReady {
	select {
	case cr := <-c.readyCh:
		return cr
	case <-c.stopper.ShouldStop():
		return channelReady{}
	}

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
	tick := time.NewTicker(time.Millisecond * 51)
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
					// for _, msg := range rd.Messages {
					// 	c.Info("channel ready", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.String("msgType", msg.MsgType.String()), zap.Uint64("to", msg.To), zap.Uint64("index", msg.Index))
					// }

					c.triggerReady(channelReady{
						channel: ch,
						Ready:   rd,
					})
				} else {
					if c.isInactiveChannel(ch) { // 频道不活跃，移除，等待频道再此收到消息时，重新加入
						c.Remove(ch)
						c.Info("remove inactive channel", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
					}
				}

			})

		case <-c.stopper.ShouldStop():
			return
		}
	}
}

func (c *ChannelListener) triggerReady(ready channelReady) {
	select {
	case c.readyCh <- ready:
	case <-c.stopper.ShouldStop():
		return
	}
}

// 判断是否是不活跃的频道
func (c *ChannelListener) isInactiveChannel(channel *channel) bool {
	return channel.isDestroy() || channel.lastActivity.Load().Add(c.opts.ChannelInactiveTimeout).Before(time.Now())
}
