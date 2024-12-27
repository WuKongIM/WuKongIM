package event

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/valyala/fastrand"
	"go.uber.org/zap"
)

type EventPool struct {
	pollers []*poller
	wklog.Log
	handler  eventbus.PushEventHandler
	handlers []*pushHandler
}

func NewEventPool(handler eventbus.PushEventHandler) *EventPool {
	u := &EventPool{
		handler: handler,
		Log:     wklog.NewWKLog("EventPool"),
	}

	pollerCount := options.G.Poller.PushHandlerCount / options.G.Poller.PushHandlerPerPoller
	if options.G.Poller.PushHandlerCount%options.G.Poller.PushHandlerPerPoller != 0 {
		pollerCount++
	}

	for i := 0; i < pollerCount; i++ {
		p := newPoller(i, u)
		u.pollers = append(u.pollers, p)
	}

	u.handlers = make([]*pushHandler, options.G.Poller.PushHandlerCount)
	for i := 0; i < options.G.Poller.PushHandlerCount; i++ {
		poller := u.pollers[i/options.G.Poller.PushHandlerPerPoller]
		u.handlers[i] = newPushHandler(i+1, poller)
		poller.handlers = append(poller.handlers, u.handlers[i])
	}
	return u
}

// Start 启动
func (e *EventPool) Start() error {
	for _, p := range e.pollers {
		err := p.start()
		if err != nil {
			e.Error("start poller failed", zap.Error(err))
			return err
		}
	}
	return nil
}

// Stop 停止
func (e *EventPool) Stop() {
	for _, p := range e.pollers {
		p.stop()
	}
}

// 随机获得一个handler id
func (e *EventPool) RandHandlerId() int {
	v := int(fastrand.Uint32() % uint32(options.G.Poller.PushHandlerCount+1))
	if v <= 0 {
		return 1
	}
	return v
}

// AddEvent 添加事件
func (e *EventPool) AddEvent(id int, event *eventbus.Event) {
	if id <= 0 {
		id = e.RandHandlerId()
	}
	h := e.handlerById(id)
	h.addEvent(event)
}

// Advance 推进事件
func (e *EventPool) Advance(id int) {
	p := e.handlerById(id)
	p.poller.advance()
}

func (e *EventPool) handlerById(id int) *pushHandler {

	return e.handlers[id-1]
}
