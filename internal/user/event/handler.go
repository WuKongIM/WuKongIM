package event

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/fasttime"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type userHandler struct {
	wklog.Log
	Uid        string
	lastActive uint64 // 最后活跃时间
	pending    struct {
		sync.RWMutex
		eventQueue *eventbus.EventQueue
	}
	poller  *poller
	handler eventbus.UserEventHandler

	// 连接列表
	conns *conns
	// 处理中的下标位置
	processingIndex uint64
	// 是否正在处理
	processing bool
}

func newUserHandler(uid string, poller *poller) *userHandler {
	uh := &userHandler{
		Uid:        uid,
		poller:     poller,
		handler:    poller.eventPool.handler,
		lastActive: fasttime.UnixTimestamp(),
		conns:      newConns(),
		Log:        wklog.NewWKLog(fmt.Sprintf("userHandler[%s]", uid)),
	}
	uh.pending.eventQueue = eventbus.NewEventQueue(fmt.Sprintf("user:%s", uid))
	return uh
}

func (u *userHandler) addEvent(event *eventbus.Event) {
	u.pending.Lock()
	defer u.pending.Unlock()
	event.Index = u.pending.eventQueue.LastIndex() + 1
	u.pending.eventQueue.Append(event)

	u.lastActive = fasttime.UnixTimestamp()
}

func (u *userHandler) hasEvent() bool {
	u.pending.RLock()
	defer u.pending.RUnlock()
	if u.processing {
		return false
	}
	return u.processingIndex < u.pending.eventQueue.LastIndex()
}

// 推进事件
func (u *userHandler) advanceEvents() {

	slotLeaderId := u.leaderId(u.Uid)
	if slotLeaderId == 0 {
		u.Error("advanceEvents: slotLeaderId is 0", zap.String("uid", u.Uid))
		return
	}

	u.pending.Lock()
	u.processing = true
	defer func() {
		u.processing = false
	}()
	// 获取事件
	events := u.pending.eventQueue.SliceWithSize(u.processingIndex+1, u.pending.eventQueue.LastIndex()+1, options.G.Poller.UserEventMaxSizePerBatch)
	if len(events) == 0 && u.processingIndex < u.pending.eventQueue.LastIndex() {
		u.pending.Unlock()
		u.Foucs("advanceEvents: events is empty,but u.processingIndex < u.pending.eventQueue.lastIndex ", zap.Uint64("processingIndex", u.processingIndex), zap.Uint64("lastIndex", u.pending.eventQueue.LastIndex()))
		u.processingIndex = u.pending.eventQueue.LastIndex()
		return
	}
	if len(events) == 0 {
		u.pending.Unlock()
		return
	}
	eventLastIndex := events[len(events)-1].Index

	// 截取掉之前的事件
	u.pending.eventQueue.TruncateTo(eventLastIndex + 1)

	u.processingIndex = eventLastIndex
	u.pending.Unlock()

	// 按类型分组
	group := u.groupByType(events)
	// 处理事件
	for eventType, events := range group {

		// 从对象池中获取上下文
		ctx := u.poller.getContext()
		ctx.Uid = u.Uid
		ctx.EventType = eventType
		ctx.Events = events
		ctx.AddConn = u.addConn
		// 处理事件
		u.handler.OnEvent(ctx)

		// 释放上下文
		u.poller.putContext(ctx)
	}

	if u.pending.eventQueue.Len() > 0 {
		u.poller.advance()
	}

}

func (u *userHandler) leaderId(uid string) uint64 {
	slotId := service.Cluster.GetSlotId(uid)
	leaderId, err := service.Cluster.SlotLeaderId(slotId)
	if err != nil {
		u.Error("get leaderId failed", zap.Error(err))
		return 0
	}
	return leaderId
}

// 连接成功
func (u *userHandler) addConn(conn *eventbus.Conn) {
	u.conns.addOrUpdateConn(conn)
	u.lastActive = fasttime.UnixTimestamp()
}

// isTimeout 判断用户是否超时
func (u *userHandler) isTimeout() bool {
	return fasttime.UnixTimestamp()-u.lastActive > uint64(options.G.Poller.UserTimeout.Seconds())
}

// groupByType 将待处理事件按照事件类型分组
func (u *userHandler) groupByType(events []*eventbus.Event) map[eventbus.EventType][]*eventbus.Event {
	group := make(map[eventbus.EventType][]*eventbus.Event)
	for _, event := range events {
		group[event.Type] = append(group[event.Type], event)
	}
	return group
}
