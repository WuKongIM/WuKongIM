package reactor

import "github.com/WuKongIM/WuKongIM/internal/reactor"

type Reactor struct {
	subs []*reactorSub
}

func New(opt ...Option) *Reactor {
	options = NewOptions()
	for _, o := range opt {
		o(options)
	}
	r := &Reactor{
		subs: make([]*reactorSub, options.SubCount),
	}

	for i := 0; i < options.SubCount; i++ {
		r.subs[i] = newReactorSub(i, r)
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

func (r *Reactor) send(actions []reactor.ChannelAction) {
}
