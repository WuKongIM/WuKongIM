package reactor

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type Reactor struct {
	subReactors []*ReactorSub
	opts        *Options
	mu          sync.RWMutex
	taskPool    *ants.Pool
	wklog.Log
}

func New(opts *Options) *Reactor {
	r := &Reactor{
		opts: opts,
		Log:  wklog.NewWKLog("Reactor"),
	}
	taskPool, err := ants.NewPool(opts.TaskPoolSize)
	if err != nil {
		r.Panic("create task pool error", zap.Error(err))
	}
	r.taskPool = taskPool

	for i := 0; i < int(r.opts.SubReactorNum); i++ {
		sub := r.newReactorSub(i)
		r.subReactors = append(r.subReactors, sub)
	}
	return r
}

func (r *Reactor) Start() error {
	for _, sub := range r.subReactors {
		err := sub.Start()
		if err != nil {
			return err
		}

	}
	return nil
}

func (r *Reactor) Stop() {

	for _, sub := range r.subReactors {
		sub.Stop()
	}

}
func (r *Reactor) ProposeAndWait(ctx context.Context, handleKey string, logs []replica.Log) ([]ProposeResult, error) {
	sub := r.reactorSub(handleKey)
	return sub.proposeAndWait(ctx, handleKey, logs)
}

func (r *Reactor) Propose(handleKey string, logs []replica.Log) error {
	sub := r.reactorSub(handleKey)
	return sub.propose(handleKey, logs)
}

func (r *Reactor) AddHandler(key string, handler IHandler) {
	h := getHandlerFromPool()
	h.init(key, handler, r)
	sub := r.reactorSub(key)
	sub.addHandler(h)
}

func (r *Reactor) RemoveHandler(key string) {

	sub := r.reactorSub(key)
	h := sub.removeHandler(key)
	putHandlerToPool(h)
}

func (r *Reactor) Handler(key string) IHandler {
	sub := r.reactorSub(key)
	h := sub.handler(key)
	if h == nil {
		return nil
	}
	return h.handler
}

func (r *Reactor) ExistHandler(key string) bool {
	sub := r.reactorSub(key)
	return sub.existHandler(key)
}

func (r *Reactor) AddMessage(m Message) {
	sub := r.reactorSub(m.HandlerKey)
	sub.addMessage(m)
}

func (r *Reactor) HandlerLen() int {
	len := 0
	for _, sub := range r.subReactors {
		len += sub.handlerLen()
	}
	return len
}

func (r *Reactor) newReactorSub(i int) *ReactorSub {
	return NewReactorSub(i, r)
}

func (r *Reactor) reactorSub(key string) *ReactorSub {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx := hashWthString(key)
	return r.subReactors[idx%r.opts.SubReactorNum]
}

func (r *Reactor) newMessageQueue() *MessageQueue {
	return NewMessageQueue(r.opts.ReceiveQueueLength, false, r.opts.LazyFreeCycle, r.opts.MaxReceiveQueueSize)
}
