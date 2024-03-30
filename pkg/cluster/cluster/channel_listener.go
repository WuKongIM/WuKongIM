package cluster

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type ChannelListener struct {
	channels  *channelQueue
	readyCh   chan channelReady
	advanceCh chan struct{}
	stopper   *syncutil.Stopper
	// 已准备的频道
	opts    *Options
	onReady func(channelReady)
	wklog.Log
}

func NewChannelListener(onReady func(channelReady), opts *Options) *ChannelListener {
	return &ChannelListener{
		channels:  newChannelQueue(),
		onReady:   onReady,
		readyCh:   make(chan channelReady, 100),
		advanceCh: make(chan struct{}, 1),
		stopper:   syncutil.NewStopper(),
		opts:      opts,
		Log:       wklog.NewWKLog("ChannelListener"),
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
	tick := time.NewTicker(time.Millisecond * 20)
	for {
		c.ready()
		select {
		case <-tick.C:
		case <-c.advanceCh:
		case <-c.stopper.ShouldStop():
			return
		}
	}
}

func (c *ChannelListener) ready() {
	hasEvent := true
	var err error
	for hasEvent {
		hasEvent = false
		c.channels.foreach(func(ch *channel) {
			start := time.Now()
			if ch.isDestroy() {
				return
			}
			event := false
			if event, err = ch.handleEvents(); err != nil {
				c.Warn("loopEvent: handleReceivedMessages error", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.Error(err))
			}
			if event {
				hasEvent = true
			}
			event = c.handleReady(ch)
			if event {
				hasEvent = true
			}
			if hasEvent {
				if time.Since(start) > time.Millisecond {
					c.Debug("loopEvent end...", zap.Duration("cost", time.Since(start)), zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
				}
			}

			if c.isInactiveChannel(ch) { // 频道不活跃，移除，等待频道再此收到消息时，重新加入
				c.Remove(ch)
				c.Info("remove inactive channel", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
			}

		})
	}
}

func (c *ChannelListener) handleReady(ch *channel) bool {
	if ch.hasReady() {
		rd := ch.ready()
		if replica.IsEmptyReady(rd) {
			return false
		}
		c.triggerReady(channelReady{
			channel: ch,
			Ready:   rd,
		})
		return true
	}
	return false
}

func (c *ChannelListener) advance() {
	select {
	case c.advanceCh <- struct{}{}:
	case <-c.stopper.ShouldStop():
	default:
	}
}

func (c *ChannelListener) triggerReady(ready channelReady) {
	// select {
	// case c.readyCh <- ready:
	// case <-c.stopper.ShouldStop():
	// 	return
	// }
	if c.onReady != nil {
		c.onReady(ready)
	}
}

// 判断是否是不活跃的频道
func (c *ChannelListener) isInactiveChannel(channel *channel) bool {
	return channel.isDestroy() || channel.lastActivity.Load().Add(c.opts.ChannelInactiveTimeout).Before(time.Now())
}
