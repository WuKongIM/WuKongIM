package reactor

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/valyala/fastrand"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type reactorSub struct {
	users        *list // 用户列表
	stopped      atomic.Bool
	actionQueue  *actionQueue  // action队列
	tickInterval time.Duration // tick间隔时间
	stopC        chan struct{}
	advanceC     chan struct{} // 推进事件
	wklog.Log
}

func newReactorSub(index int) *reactorSub {

	return &reactorSub{
		tickInterval: time.Millisecond * 200,
		stopC:        make(chan struct{}),
		advanceC:     make(chan struct{}, 1),
		users:        newList(),
		actionQueue:  newActionQueue(1024, false, 1, 0),
		Log:          wklog.NewWKLog(fmt.Sprintf("user.reactorSub[%d]", index)),
	}
}

func (r *reactorSub) start() error {

	return nil
}

func (r *reactorSub) stop() {

}

func (r *reactorSub) loop() {

	p := float64(fastrand.Uint32()) / (1 << 32)
	// 以避免系统中因定时器、周期性任务或请求间隔完全一致而导致的同步问题（例如拥堵或资源竞争）。
	jitter := time.Duration(p * float64(r.tickInterval/2))
	tick := time.NewTicker(r.tickInterval + jitter)
	defer tick.Stop()

	for !r.stopped.Load() {
		r.readEvents()
		select {
		case <-r.advanceC:
		case <-tick.C:
			r.tick()
		case <-r.stopC:
			return
		}
	}
}

func (r *reactorSub) tick() {

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
		r.advance()
	}
}

// 处理本地事件
func (r *reactorSub) handleEvents() bool {

	return true
}

// 处理收到的action
func (r *reactorSub) handleReceivedActions() bool {
	actions := r.actionQueue.get()
	if len(actions) == 0 {
		return false
	}

	var err error
	for _, a := range actions {
		user := r.users.get(a.Uid)
		if user == nil {
			continue
		}
		if a.No != "" && a.No != user.no {
			continue
		}
		err = user.step(a)
		if err != nil {
			r.Error("step failed", zap.Error(err), zap.String("uid", a.Uid))
		}
	}

	return true
}

func (r *reactorSub) advance() {
	select {
	case r.advanceC <- struct{}{}:
	default:
	}
}
