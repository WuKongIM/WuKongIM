package reactor

import (
	"math/rand"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
)

type Reactor struct {
	opts     *Options
	subs     []*reactorSub
	subCount int
	workers  []*worker
}

func New(opt ...Option) *Reactor {
	opts := NewOptions()
	for _, o := range opt {
		o(opts)
	}

	subCount := opts.WorkerCount / opts.WorkerPerReactorSub
	if opts.WorkerCount%opts.WorkerPerReactorSub != 0 {
		subCount++
	}
	r := &Reactor{
		opts:     opts,
		subCount: subCount,
	}
	r.subs = make([]*reactorSub, subCount)
	for i := 0; i < subCount; i++ {
		r.subs[i] = newReactorSub(i, r)
	}

	r.workers = make([]*worker, opts.WorkerCount)
	for i := 0; i < opts.WorkerCount; i++ {
		sub := r.subs[i/opts.WorkerPerReactorSub]
		r.workers[i] = newWorker(i, sub)
		sub.workers = append(sub.workers, r.workers[i])
	}

	return r
}

func (r *Reactor) Start() error {

	for _, sub := range r.subs {
		err := sub.start()
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Reactor) Stop() {
	for _, sub := range r.subs {
		sub.stop()
	}
}

func (r *Reactor) AddAction(a reactor.PushAction) {
	wokerId := a.WorkerId
	// 如果workerId为0，随机分配一个worker
	if wokerId == 0 {
		wokerId = r.randWorkerId()
		a.WorkerId = wokerId
	}
	r.getSub(wokerId).addAction(a)
}

func (r *Reactor) send(actions []reactor.PushAction) {
}

func (r *Reactor) getSub(workerId int) *reactorSub {
	return r.workers[workerId].sub
}
func (r *Reactor) randWorkerId() int {
	randWId := rand.Intn(r.opts.WorkerCount)
	return randWId
}
