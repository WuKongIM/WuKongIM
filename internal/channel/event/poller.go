package event

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type poller struct {
	waitlist *linkedList
	wklog.Log
	sync.RWMutex

	tmpHandlers []*channelHandler
	stopper     *syncutil.Stopper
	handlePool  *ants.Pool
	index       int

	// tick次数
	tickCount int
	eventPool *EventPool

	contextPool *sync.Pool
	advanceC    chan struct{}
}

func newPoller(index int, eventPool *EventPool) *poller {

	p := &poller{
		waitlist:  newLinkedList(),
		stopper:   syncutil.NewStopper(),
		index:     index,
		eventPool: eventPool,
		Log:       wklog.NewWKLog(fmt.Sprintf("poller[%d]", index)),
		contextPool: &sync.Pool{
			New: func() interface{} {
				return &eventbus.ChannelContext{}
			},
		},
		advanceC: make(chan struct{}, 1),
	}

	var err error
	p.handlePool, err = ants.NewPool(options.G.Poller.ChannelGoroutine, ants.WithNonblocking(true), ants.WithPanicHandler(func(i interface{}) {
		p.Panic("channel handle panic", zap.Any("panic", i), zap.Stack("stack"))
	}))
	if err != nil {
		p.Panic("new ants pool failed", zap.String("error", err.Error()))
	}
	return p
}

func (p *poller) start() error {
	p.stopper.RunWorker(p.loopEvent)
	return nil
}

func (p *poller) stop() {
	p.stopper.Stop()
}

func (p *poller) addEvent(channelId string, channelType uint8, event *eventbus.Event) {
	p.Lock()
	defer p.Unlock()
	h := p.handler(channelId, channelType)
	if h == nil {
		h = newChannelHandler(channelId, channelType, p)
		p.waitlist.push(h)
	}
	h.addEvent(event)
}

func (p *poller) loopEvent() {
	tk := time.NewTicker(options.G.Poller.IntervalTick)
	defer tk.Stop()
	for {
		p.handleEvents()
		select {
		case <-tk.C:
			p.tick()
		case <-p.advanceC:
		case <-p.stopper.ShouldStop():
			return
		}
	}
}

func (p *poller) tick() {
	p.tickCount++
	if p.tickCount%options.G.Poller.ClearIntervalTick == 0 {
		p.tickCount = 0
		p.clear()
	}
}

func (p *poller) clear() {
	p.Lock()
	defer p.Unlock()
	p.waitlist.readHandlers(&p.tmpHandlers)
	for _, h := range p.tmpHandlers {
		if h.isTimeout() {
			p.waitlist.remove(h.channelId, h.channelType)
		}
	}

}

func (p *poller) handleEvents() {
	p.waitlist.readHandlers(&p.tmpHandlers)
	var err error
	for _, h := range p.tmpHandlers {
		if h.hasEvent() {
			events := h.events()
			err = p.handlePool.Submit(func() {
				h.advanceEvents(events)
			})
			if err != nil {
				p.Error("submit channel handle task failed", zap.String("error", err.Error()))
			}
		}
	}
	p.tmpHandlers = p.tmpHandlers[:0]
	if cap(p.tmpHandlers) > 1024 {
		p.tmpHandlers = nil
	}
}

func (p *poller) advance() {
	select {
	case p.advanceC <- struct{}{}:
	default:
	}
}

func (p *poller) handler(channelId string, channelType uint8) *channelHandler {
	return p.waitlist.get(channelId, channelType)
}

func (p *poller) getContext() *eventbus.ChannelContext {
	return p.contextPool.Get().(*eventbus.ChannelContext)
}

func (p *poller) putContext(ctx *eventbus.ChannelContext) {
	ctx.Reset()
	p.contextPool.Put(ctx)
}
