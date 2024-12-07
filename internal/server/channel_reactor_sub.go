package server

import (
	"context"
	"time"

	"github.com/lni/goutils/syncutil"
	"github.com/valyala/fastrand"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type channelReactorSub struct {
	stopper *syncutil.Stopper

	channelQueue *channelList
	stopped      atomic.Bool

	advanceC     chan struct{}
	stepChannelC chan stepChannel
	r            *channelReactor
	index        int
}

func newChannelReactorSub(index int, r *channelReactor) *channelReactorSub {
	return &channelReactorSub{
		stopper:      syncutil.NewStopper(),
		channelQueue: newChannelList(),
		advanceC:     make(chan struct{}, 1),
		stepChannelC: make(chan stepChannel, 1024*10),
		r:            r,
		index:        index,
	}
}

func (r *channelReactorSub) start() error {
	r.stopper.RunWorker(r.loop)

	return nil
}

func (r *channelReactorSub) stop() {
	r.stopped.Store(true)
	r.stopper.Stop()
}

func (r *channelReactorSub) loop() {

	p := float64(fastrand.Uint32()) / (1 << 32)

	// 以避免系统中因定时器、周期性任务或请求间隔完全一致而导致的同步问题（例如拥堵或资源竞争）。
	jitter := time.Duration(p * float64(r.r.s.opts.Reactor.Channel.TickInterval/2))
	tk := time.NewTicker(r.r.s.opts.Reactor.Channel.TickInterval + jitter)
	defer tk.Stop()

	for !r.stopped.Load() {
		r.readys()
		select {
		case <-tk.C:
			r.ticks()
		case <-r.advanceC:
		case req := <-r.stepChannelC:
			var err error
			if req.ch != nil {
				err = req.ch.step(req.action)
			}
			if req.waitC != nil {
				req.waitC <- err
			}
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactorSub) step(ch *channel, action *ChannelAction) {
	select {
	case r.stepChannelC <- stepChannel{ch: ch, action: action}:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactorSub) stepWait(ch *channel, action *ChannelAction) error {

	start := time.Now()

	defer func() {
		end := time.Since(start)
		if end > 200*time.Millisecond {
			r.r.Warn("stepWait cost too long", zap.Duration("cost", end))
		}
	}()

	waitC := make(chan error, 1)
	select {
	case r.stepChannelC <- stepChannel{ch: ch, action: action, waitC: waitC}:
	case <-r.stopper.ShouldStop():
		return ErrReactorStopped
	}

	timeoutCtx, cancel := context.WithTimeout(r.r.s.ctx, 5*time.Second)
	defer cancel()

	select {
	case err := <-waitC:
		return err
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
	case <-r.stopper.ShouldStop():
		return ErrReactorStopped
	}
}

func (r *channelReactorSub) readys() {

	r.channelQueue.iter(func(ch *channel) {
		if r.stopped.Load() {
			return
		}
		if ch.hasReady() {
			r.handleReady(ch)
		}
	})
}

func (r *channelReactorSub) ticks() {
	r.channelQueue.iter(func(ch *channel) {
		if r.stopped.Load() {
			return
		}
		ch.tick()
	})
}

func (r *channelReactorSub) handleReady(ch *channel) {
	rd := ch.ready()

	for _, action := range rd.actions {

		switch action.ActionType {
		case ChannelActionInit: // 初始化
			r.r.addInitReq(&initReq{
				ch:  ch,
				sub: r,
			})
		case ChannelActionPayloadDecrypt, ChannelActionStreamPayloadDecrypt: // 消息解密
			r.r.addPayloadDecryptReq(&payloadDecryptReq{
				ch:       ch,
				messages: action.Messages,
				isStream: action.ActionType == ChannelActionStreamPayloadDecrypt,
				sub:      r,
			})
		case ChannelActionPermissionCheck: // 权限校验
			r.r.addPermissionReq(&permissionReq{
				ch:       ch,
				messages: action.Messages,
				sub:      r,
			})
		case ChannelActionStorage: // 消息存储
			r.r.addStorageReq(&storageReq{
				ch:       ch,
				messages: action.Messages,
				sub:      r,
			})
		case ChannelActionDeliver, ChannelActionStreamDeliver: // 消息投递
			r.r.addDeliverReq(&deliverReq{
				ch:          ch,
				channelId:   ch.channelId,
				channelType: ch.channelType,
				tagKey:      ch.receiverTagKey.Load(),
				messages:    action.Messages,
				isStream:    action.ActionType == ChannelActionStreamDeliver,
				sub:         r,
			})
		case ChannelActionSendack: // 发送回执
			r.r.addSendackReq(&sendackReq{
				ch:       ch,
				messages: action.Messages,
				sub:      r,
			})
		case ChannelActionForward: // 转发消息
			r.r.addForwardReq(&forwardReq{
				ch:       ch,
				messages: action.Messages,
				leaderId: action.LeaderId,
				sub:      r,
			})
		case ChannelActionClose:
			r.r.addCloseReq(&closeReq{
				ch:       ch,
				leaderId: ch.leaderId,
			})
		case ChannelActionCheckTag:
			r.r.addCheckTagReq(&checkTagReq{
				ch: ch,
			})
		}
	}

}

func (r *channelReactorSub) channel(key string) *channel {
	return r.channelQueue.get(key)
}

func (r *channelReactorSub) addChannel(ch *channel) {
	r.channelQueue.add(ch)
}

func (r *channelReactorSub) removeChannel(key string) {
	r.channelQueue.remove(key)
}

// func (r *channelReactorSub) advance() {
// 	select {
// 	case r.advanceC <- struct{}{}:
// 	default:
// 	}
// }

type stepChannel struct {
	ch     *channel
	action *ChannelAction
	waitC  chan error
}
