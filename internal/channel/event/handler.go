package event

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/fasttime"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type channelHandler struct {
	wklog.Log
	channelId   string
	channelType uint8
	channelKey  string
	leaderId    uint64 // 频道领导节点Id
	nodeVersion uint64 // 节点版本,当前节点分布式配置的版本
	lastActive  uint64 // 最后活跃时间
	pending     struct {
		sync.RWMutex
		eventQueue *eventbus.EventQueue
	}
	poller  *poller
	handler eventbus.ChannelEventHandler
	// 处理中的下标位置
	processingIndex uint64
	// 是否正在处理
	processing atomic.Bool
}

func newChannelHandler(channelId string, channelType uint8, poller *poller) *channelHandler {

	channelKey := wkutil.ChannelToKey(channelId, channelType)
	uh := &channelHandler{
		channelKey:  channelKey,
		channelId:   channelId,
		channelType: channelType,
		poller:      poller,
		handler:     poller.eventPool.handler,
		lastActive:  fasttime.UnixTimestamp(),
		Log:         wklog.NewWKLog(fmt.Sprintf("channelHandler[%s]", channelKey)),
	}
	uh.pending.eventQueue = eventbus.NewEventQueue(fmt.Sprintf("channel:%s", channelKey))
	return uh
}

func (c *channelHandler) addEvent(event *eventbus.Event) {
	c.pending.Lock()
	defer c.pending.Unlock()
	event.Index = c.pending.eventQueue.LastIndex() + 1
	c.pending.eventQueue.Append(event)

	c.lastActive = fasttime.UnixTimestamp()
}

func (c *channelHandler) hasEvent() bool {
	c.pending.RLock()
	defer c.pending.RUnlock()
	if c.processing.Load() {
		return false
	}
	return c.processingIndex < c.pending.eventQueue.LastIndex()
}

func (u *channelHandler) events() []*eventbus.Event {
	u.pending.Lock()
	defer u.pending.Unlock()
	events := u.pending.eventQueue.SliceWithSize(u.processingIndex+1, u.pending.eventQueue.LastIndex()+1, options.G.Poller.ChannelEventMaxSizePerBatch)
	if len(events) == 0 {
		return nil
	}
	eventLastIndex := events[len(events)-1].Index

	// 截取掉之前的事件
	u.pending.eventQueue.TruncateTo(eventLastIndex + 1)
	u.processingIndex = eventLastIndex
	return events
}

// 推进事件
func (c *channelHandler) advanceEvents(events []*eventbus.Event) {

	c.processing.Store(true)
	defer func() {
		c.processing.Store(false)
	}()

	// 检查和更新leaderId
	c.checkAndUpdateLeaderIdChange()

	// 按类型分组
	group := c.groupByType(events)
	// 处理事件
	for eventType, events := range group {
		// 从对象池中获取上下文
		ctx := c.poller.getContext()
		ctx.ChannelId = c.channelId
		ctx.ChannelType = c.channelType
		ctx.EventType = eventType
		ctx.Events = events
		ctx.LeaderId = c.leaderId
		// 处理事件
		c.handler.OnEvent(ctx)

		// 释放上下文
		c.poller.putContext(ctx)
	}

	// 推进事件
	if c.pending.eventQueue.Len() > 0 {
		c.poller.advance()
	}
}

// checkAndUpdateLeaderIdChange 检查并更新leaderId变化
func (c *channelHandler) checkAndUpdateLeaderIdChange() {
	c.pending.Lock()
	defer c.pending.Unlock()
	nodeVersion := service.Cluster.NodeVersion()
	if c.nodeVersion >= nodeVersion {
		return
	}
	leaderId, err := service.Cluster.LeaderIdOfChannel(c.channelId, c.channelType)
	if err != nil {
		c.Error("checkLeaderIdChange: get leader id failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return
	}
	if leaderId == 0 {
		c.Warn("checkLeaderIdChange: leader id is 0", zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return
	}
	c.nodeVersion = nodeVersion
	c.leaderId = leaderId
}

// isTimeout 判断用户是否超时
func (c *channelHandler) isTimeout() bool {
	return fasttime.UnixTimestamp()-c.lastActive > uint64(options.G.Poller.ChannelTimeout.Seconds())
}

// groupByType 将待处理事件按照事件类型分组
func (c *channelHandler) groupByType(events []*eventbus.Event) map[eventbus.EventType][]*eventbus.Event {
	group := make(map[eventbus.EventType][]*eventbus.Event)
	for _, event := range events {
		group[event.Type] = append(group[event.Type], event)
	}
	return group
}
