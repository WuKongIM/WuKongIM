package cluster

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
)

type ChannelManager struct {
	stopper *syncutil.Stopper

	sendSyncNotifyC chan *Channel
	syncNotifyC     chan *replica.SyncNotify
	syncC           chan channelSyncNotify

	channelQueue *channelQueue // 进行中的频道
	wklog.Log

	eachOfPopSize int // 每次取出的频道数量
	s             *Server
}

func NewChannelManager(s *Server) *ChannelManager {
	return &ChannelManager{
		sendSyncNotifyC: make(chan *Channel, 100),
		syncNotifyC:     make(chan *replica.SyncNotify, 100),
		stopper:         syncutil.NewStopper(),
		channelQueue:    newChannelQueue(),
		Log:             wklog.NewWKLog("ChannelManager"),
		eachOfPopSize:   10,
		s:               s,
	}
}

func (c *ChannelManager) Start() error {
	c.stopper.RunWorker(c.loop)
	return nil
}

func (c *ChannelManager) Stop() {
	c.stopper.Stop()

}

func (c *ChannelManager) GetChannel(channelID string, channelType uint8) *Channel {
	channel := c.channelQueue.Get(channelID, channelType)
	if channel == nil {
		channel = NewChannel(channelID, channelType, c.s)
	}
	return channel
}

func (c *ChannelManager) loop() {
	tick := time.NewTicker(time.Second)
	for {
		select {
		case channel := <-c.sendSyncNotifyC: // 领导节点通知副本去同步
			c.channelQueue.Push(channel)
			c.triggerSendNotifySync(channel)
		case <-tick.C: // 定时触发为通知成功的频道
			channels := c.channelQueue.PeekAndBack(c.eachOfPopSize)
			for _, channel := range channels {
				c.triggerSendNotifySync(channel)
			}
		case req := <-c.syncC: // 副本收到频道同步日志的通知
			c.handleSyncNotify(req.channel, req.req)

		case <-c.stopper.ShouldStop():
			return
		}
	}
}

// 领导节点通知所有副本去同步
func (c *ChannelManager) triggerSendNotifySync(ch *Channel) {
	_, _ = ch.SendNotifySyncToAll()
}

// 副本处理领导节点的通知
func (c *ChannelManager) handleSyncNotify(ch *Channel, req *replica.SyncNotify) {
	ch.r.RequestSyncLogs()
}

// 副本节点收到领导节点的通知，触发同步日志请求
func (c *ChannelManager) triggerHandleSyncNotify(channel *Channel, req *replica.SyncNotify) {
	select {
	case c.syncC <- channelSyncNotify{
		channel: channel,
		req:     req,
	}:
	case <-c.stopper.ShouldStop():
		return
	}
}

type channelSyncNotify struct {
	channel *Channel
	req     *replica.SyncNotify
}

type channelQueue struct {
	channelIndexMap map[string]int
	channels        []*Channel
	resetCount      int
}

func newChannelQueue() *channelQueue {
	return &channelQueue{
		channelIndexMap: make(map[string]int),
		channels:        make([]*Channel, 0),
		resetCount:      1000,
	}
}

func (c *channelQueue) Push(ch *Channel) {
	if len(c.channels) >= c.resetCount {
		c.restIndex()
	}
	c.channels = append(c.channels, ch)
	c.channelIndexMap[ch.GetChannelKey()] = len(c.channels) - 1

}

func (c *channelQueue) PeekAndBack(size int) []*Channel {
	chs := c.channels[:size]
	if len(chs) == 0 {
		return nil
	}
	for i := len(chs) - 1; i >= 0; i-- {
		ch := chs[i]
		c.channels = append(c.channels, ch)
		c.channelIndexMap[ch.GetChannelKey()] = len(c.channels) - 1
	}
	return chs
}

func (c *channelQueue) Remove(channelKey string) {
	idx, ok := c.channelIndexMap[channelKey]
	if !ok {
		return
	}
	c.channels[idx] = nil

	delete(c.channelIndexMap, channelKey)
}

func (c *channelQueue) Get(channelID string, channelType uint8) *Channel {
	channelKey := GetChannelKey(channelID, channelType)
	idx, ok := c.channelIndexMap[channelKey]
	if !ok {
		return nil
	}
	return c.channels[idx]
}

func (c *channelQueue) restIndex() {
	newChannels := make([]*Channel, 0)
	for _, channel := range c.channels {
		if channel != nil {
			newChannels = append(newChannels, channel)
		}
	}

	for idx, ch := range newChannels {
		c.channelIndexMap[ch.GetChannelKey()] = idx
	}
}
