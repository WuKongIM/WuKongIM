package event

import (
	"hash/fnv"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type EventPool struct {
	pollers []*poller
	wklog.Log
	handler eventbus.ChannelEventHandler
}

func NewEventPool(handler eventbus.ChannelEventHandler) *EventPool {
	u := &EventPool{
		handler: handler,
		Log:     wklog.NewWKLog("EventPool"),
	}
	for i := 0; i < options.G.Poller.ChannelCount; i++ {
		p := newPoller(i, u)
		u.pollers = append(u.pollers, p)
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

// AddEvent 添加事件
func (e *EventPool) AddEvent(channelId string, channelType uint8, event *eventbus.Event) {
	p := e.pollerByChannel(channelId, channelType)
	p.addEvent(channelId, channelType, event)
}

// Advance 推进事件
func (e *EventPool) Advance(channelId string, channelType uint8) {
	p := e.pollerByChannel(channelId, channelType)
	p.advance()
}

func (e *EventPool) pollerByChannel(channelId string, _ uint8) *poller {
	h := fnv.New32a()
	h.Write([]byte(channelId))
	i := h.Sum32() % uint32(len(e.pollers))
	return e.pollers[i]
}
