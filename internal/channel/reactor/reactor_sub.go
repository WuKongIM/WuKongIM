package reactor

import (
	"fmt"
	"time"

	goption "github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
	"github.com/valyala/fastrand"
	"go.uber.org/zap"
)

type reactorSub struct {
	channels     *list // 频道列表
	index        int
	stopper      *syncutil.Stopper
	tickInterval time.Duration // tick间隔时间
	// 连续readEvent次数
	continReadEventCount int
	advanceC             chan struct{} // 推进事件
	wklog.Log
	tmpChannels []*Channel
	actionQueue *actionQueue
	r           *Reactor
}

func newReactorSub(index int, r *Reactor) *reactorSub {
	return &reactorSub{
		index:        index,
		channels:     newList(),
		tickInterval: options.TickInterval,
		stopper:      syncutil.NewStopper(),
		Log:          wklog.NewWKLog(fmt.Sprintf("reactorSub[%d]", index)),
		advanceC:     make(chan struct{}, 1),
		r:            r,
		actionQueue:  newActionQueue(options.ReceiveQueueLength, false, 0, options.MaxReceiveQueueSize),
	}
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

		if !goption.G.Violent {
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

	event := r.handleReceivedActions()
	if event {
		hasEvent = true
	}

	event = r.handleEvents()
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
	r.channels.read(&r.tmpChannels)
	hasEvent := true

	for hasEvent {
		hasEvent = false
		for _, user := range r.tmpChannels {
			has := r.handleEvent(user)
			if has {
				hasEvent = true
			}
		}
	}
	r.tmpChannels = r.tmpChannels[:0]
	return hasEvent
}

func (r *reactorSub) handleEvent(ch *Channel) bool {
	if !ch.hasReady() {
		return false
	}
	actions := ch.ready()
	if len(actions) == 0 {
		return false
	}

	for _, action := range actions {
		switch action.Type {
		case reactor.ChannelActionClose:
			r.channels.remove(ch.key)
			trace.GlobalTrace.Metrics.Cluster().ChannelActiveCountAdd(-1)
		}
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
		user := r.channels.get(wkutil.ChannelToKey(a.FakeChannelId, a.ChannelType))
		if user == nil {
			continue
		}
		if a.No != "" && a.No != user.no {
			continue
		}
		user.step(a)
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
	r.channels.read(&r.tmpChannels)
	for _, channel := range r.tmpChannels {
		channel.tick()
	}

	r.tmpChannels = r.tmpChannels[:0]
}

func (r *reactorSub) addAction(a reactor.ChannelAction) bool {
	// r.Info("addAction==", zap.String("channel", a.FakeChannelId), zap.Uint8("channelType", a.ChannelType), zap.String("type", a.Type.String()))
	if goption.G.Violent {
		r.mustAddAction(a)
		return true
	}
	added := r.actionQueue.add(a)
	if !added {
		r.Warn("drop channel action,queue is full",
			zap.String("channelId", a.FakeChannelId),
			zap.Uint8("channelType", a.ChannelType),
			zap.String("type", a.Type.String()),
		)

	}
	return added
}

func (r *reactorSub) mustAddAction(a reactor.ChannelAction) {
	r.actionQueue.mustAdd(a)
}

func (r *reactorSub) exist(channelKey string) bool {
	return r.channels.exist(channelKey)
}

func (r *reactorSub) addChannel(ch *Channel) {
	r.channels.add(ch)
}
