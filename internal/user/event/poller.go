package event

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type poller struct {
	waitlist *linkedList
	wklog.Log
	sync.RWMutex

	tmpHandlers []*userHandler
	stopper     *syncutil.Stopper
	handlePool  *ants.Pool
	index       int

	// tick次数
	tickCount int
	eventPool *EventPool

	contextPool *sync.Pool

	advanceC chan struct{}
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
				return &eventbus.UserContext{}
			},
		},
		advanceC: make(chan struct{}, 1),
	}

	var err error
	p.handlePool, err = ants.NewPool(options.G.Poller.UserGoroutine, ants.WithNonblocking(true), ants.WithPanicHandler(func(i interface{}) {
		p.Error("user handle panic", zap.Any("panic", i), zap.Stack("stack"))
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

func (p *poller) addEvent(uid string, event *eventbus.Event) {
	p.Lock()
	defer p.Unlock()
	h := p.handler(uid)
	if h == nil {
		h = newUserHandler(uid, p)
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

	p.waitlist.readHandlers(&p.tmpHandlers)

	for _, h := range p.tmpHandlers {
		h.tick()
	}
	if p.tickCount%options.G.Poller.ClearIntervalTick == 0 {
		p.tickCount = 0
		for _, h := range p.tmpHandlers {
			if h.isTimeout() {
				p.waitlist.remove(h.Uid)
				if options.G.IsLocalNode(h.leaderId()) {
					trace.GlobalTrace.Metrics.App().OnlineUserCountAdd(-1)
				}
			}
		}
	}

	p.tmpHandlers = p.tmpHandlers[:0]
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
				p.Error("submit user handle task failed", zap.String("error", err.Error()), zap.Int("events", len(events)))
			}
		}
	}
	p.tmpHandlers = p.tmpHandlers[:0]
	if cap(p.tmpHandlers) > 1024 {
		p.tmpHandlers = nil
	}
}

func (p *poller) handler(uid string) *userHandler {
	return p.waitlist.get(uid)
}

func (p *poller) getContext() *eventbus.UserContext {
	return p.contextPool.Get().(*eventbus.UserContext)
}

func (p *poller) putContext(ctx *eventbus.UserContext) {
	ctx.Reset()
	p.contextPool.Put(ctx)
}

func (p *poller) advance() {
	select {
	case p.advanceC <- struct{}{}:
	default:
	}
}

// 查询连接信息
func (p *poller) connsByUid(uid string) []*eventbus.Conn {
	h := p.handler(uid)
	if h == nil {
		return nil
	}
	return h.conns.conns
}
func (p *poller) authedConnsByUid(uid string) []*eventbus.Conn {
	h := p.handler(uid)
	if h == nil {
		return nil
	}
	return h.conns.authedConns()
}
func (p *poller) connCountByUid(uid string) int {
	h := p.handler(uid)
	if h == nil {
		return 0
	}
	return h.conns.count()
}
func (p *poller) connsByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []*eventbus.Conn {
	h := p.handler(uid)
	if h == nil {
		return nil
	}
	return h.conns.connsByDeviceFlag(deviceFlag)
}
func (p *poller) connCountByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) int {
	h := p.handler(uid)
	if h == nil {
		return 0
	}
	return h.conns.countByDeviceFlag(deviceFlag)
}
func (p *poller) connById(uid string, fromNode uint64, id int64) *eventbus.Conn {
	h := p.handler(uid)
	if h == nil {
		return nil
	}
	return h.conns.connById(fromNode, id)
}
func (p *poller) localConnById(uid string, id int64) *eventbus.Conn {
	h := p.handler(uid)
	if h == nil {
		return nil
	}
	return h.conns.connByConnId(options.G.Cluster.NodeId, id)
}
func (p *poller) localConnByUid(uid string) []*eventbus.Conn {
	h := p.handler(uid)
	if h == nil {
		return nil
	}
	return h.conns.connsByNodeId(options.G.Cluster.NodeId)
}

func (p *poller) allConn() []*eventbus.Conn {
	var conns []*eventbus.Conn
	tmpHandlers := make([]*userHandler, 0)
	p.waitlist.readHandlers(&tmpHandlers)
	for _, h := range tmpHandlers {
		conns = append(conns, h.conns.conns...)
	}
	return conns
}

func (p *poller) updateConn(conn *eventbus.Conn) {
	h := p.handler(conn.Uid)
	if h == nil {
		return
	}
	h.conns.addOrUpdateConn(conn)
}

func (p *poller) allUserCount() int {
	return p.waitlist.count()
}

func (p *poller) allConnCount() int {
	count := 0
	p.waitlist.readHandlers(&p.tmpHandlers)
	for _, h := range p.tmpHandlers {
		count += h.conns.count()
	}
	p.tmpHandlers = p.tmpHandlers[:0]
	if cap(p.tmpHandlers) > 1024 {
		p.tmpHandlers = nil
	}
	return count
}

func (p *poller) removeConn(conn *eventbus.Conn) {
	h := p.handler(conn.Uid)
	if h == nil {
		return
	}
	h.conns.remove(conn)
}
