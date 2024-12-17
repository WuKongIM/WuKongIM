package reactor

import (
	"time"

	goption "github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"github.com/valyala/fastrand"
	"go.uber.org/zap"
)

type reactorSub struct {
	index        int
	r            *Reactor
	tickInterval time.Duration // tick间隔时间
	workers      []*worker
	actionQueue  *actionQueue
	stopper      *syncutil.Stopper
	// 连续readEvent次数
	continReadEventCount int
	advanceC             chan struct{} // 推进事件
	wklog.Log
}

func newReactorSub(index int, r *Reactor) *reactorSub {
	rs := &reactorSub{
		index:        index,
		r:            r,
		tickInterval: r.opts.TickInterval,
		stopper:      syncutil.NewStopper(),
		actionQueue:  newActionQueue(r.opts.ReceiveQueueLength, false, 0, r.opts.MaxReceiveQueueSize),
		Log:          wklog.NewWKLog("reactorSub"),
		advanceC:     make(chan struct{}, 1),
	}
	return rs
}

func (r *reactorSub) start() error {
	r.stopper.RunWorker(r.loop)
	return nil
}

func (r *reactorSub) stop() {
	r.stopper.Stop()
}

func (r *reactorSub) loop() {
	p := float64(fastrand.Uint32()) / (1 << 32)
	// 以避免系统中因定时器、周期性任务或请求间隔完全一致而导致的同步问题（例如拥堵或资源竞争）。
	jitter := time.Duration(p * float64(r.tickInterval/2))
	tick := time.NewTicker(r.tickInterval + jitter)
	defer tick.Stop()

	for {

		if !goption.G.Stress {
			if r.continReadEventCount < 1000 {
				// 读取事件
				r.readEvents()
			} else {
				r.Warn("channel: too many consecutive ready", zap.Int("continReadEventCount", r.continReadEventCount))
				r.continReadEventCount = 0
			}
		} else {
			r.readEvents()
		}

		select {
		case <-r.advanceC:
		case <-tick.C:
			r.continReadEventCount = 0
			r.tick()
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *reactorSub) readEvents() {

	hasEvent := false

	event := r.handleEvents()
	if event {
		hasEvent = true
	}
	event = r.handleReceivedActions()
	if event {
		hasEvent = true
	}

	if hasEvent { // 如果有事件 接着推进
		r.continReadEventCount++
		r.advance()
	}
}

// 处理本地事件
func (r *reactorSub) handleEvents() bool {
	hasEvent := true
	for hasEvent {
		hasEvent = false
		for _, worker := range r.workers {
			has := r.handleEvent(worker)
			if has {
				hasEvent = true
			}
		}
	}
	return hasEvent
}

func (r *reactorSub) handleEvent(worker *worker) bool {
	if !worker.hasReady() {
		return false
	}
	actions := worker.ready()
	if len(actions) == 0 {
		return false
	}
	r.r.send(actions)

	return true
}

// 处理收到的action
func (r *reactorSub) handleReceivedActions() bool {
	actions := r.actionQueue.get()
	if len(actions) == 0 {
		return false
	}
	for _, a := range actions {
		worker := r.workers[a.WorkerId]
		worker.step(a)
	}

	return true
}
func (r *reactorSub) advance() {
	select {
	case r.advanceC <- struct{}{}:
	default:
	}
}

func (r *reactorSub) tick() {
	for _, woker := range r.workers {
		woker.tick()
	}
}

func (r *reactorSub) addAction(a reactor.DiffuseAction) bool {
	if goption.G.Stress {
		r.mustAddAction(a)
		return true
	}
	added := r.actionQueue.add(a)
	if !added {
		r.Warn("drop diffuse action,queue is full",
			zap.Int("workerId", a.WorkerId),
			zap.String("type", a.Type.String()),
		)

	}
	return added
}

func (r *reactorSub) mustAddAction(a reactor.DiffuseAction) {
	r.actionQueue.mustAdd(a)
}
