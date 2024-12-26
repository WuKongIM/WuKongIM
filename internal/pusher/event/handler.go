package event

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type pushHandler struct {
	id int
	wklog.Log
	pending struct {
		sync.RWMutex
		eventQueue *eventbus.EventQueue
	}
	poller  *poller
	handler eventbus.PushEventHandler
	// 处理中的下标位置
	processingIndex uint64
}

func newPushHandler(id int, poller *poller) *pushHandler {

	uh := &pushHandler{
		id:      id,
		poller:  poller,
		handler: poller.eventPool.handler,
		Log:     wklog.NewWKLog(fmt.Sprintf("pushHandler[%d]", id)),
	}
	uh.pending.eventQueue = eventbus.NewEventQueue(fmt.Sprintf("push:%d", id))
	return uh
}

func (p *pushHandler) addEvent(event *eventbus.Event) {
	p.pending.Lock()
	defer p.pending.Unlock()
	event.Index = p.pending.eventQueue.LastIndex() + 1
	p.pending.eventQueue.Append(event)

}

func (p *pushHandler) hasEvent() bool {
	p.pending.RLock()
	defer p.pending.RUnlock()
	return p.processingIndex < p.pending.eventQueue.LastIndex()
}

// 推进事件
func (p *pushHandler) advanceEvents() {

	p.pending.Lock()
	// 获取事件
	events := p.pending.eventQueue.SliceWithSize(p.processingIndex+1, p.pending.eventQueue.LastIndex()+1, options.G.Poller.ChannelEventMaxSizePerBatch)
	if len(events) == 0 && p.processingIndex < p.pending.eventQueue.LastIndex() {
		p.pending.Unlock()
		p.Foucs("push:advanceEvents: events is empty,but u.processingIndex < u.pending.eventQueue.lastIndex ", zap.Uint64("processingIndex", p.processingIndex), zap.Uint64("lastIndex", p.pending.eventQueue.LastIndex()))
		p.processingIndex = p.pending.eventQueue.LastIndex()
		return
	}
	if len(events) == 0 {
		p.pending.Unlock()
		return
	}

	eventLastIndex := events[len(events)-1].Index
	// 截取掉之前的事件
	p.pending.eventQueue.TruncateTo(eventLastIndex + 1)
	p.processingIndex = eventLastIndex
	p.pending.Unlock()

	// 按类型分组
	group := p.groupByType(events)
	// 处理事件
	for eventType, events := range group {
		// 从对象池中获取上下文
		ctx := p.poller.getContext()
		ctx.Id = p.id
		ctx.EventType = eventType
		ctx.Events = events
		// 处理事件
		p.handler.OnEvent(ctx)

		// 释放上下文
		p.poller.putContext(ctx)
	}

	// 推进事件
	if p.pending.eventQueue.Len() > 0 {
		p.poller.advance()
	}
}

// groupByType 将待处理事件按照事件类型分组
func (p *pushHandler) groupByType(events []*eventbus.Event) map[eventbus.EventType][]*eventbus.Event {
	group := make(map[eventbus.EventType][]*eventbus.Event)
	for _, event := range events {
		group[event.Type] = append(group[event.Type], event)
	}
	return group
}
