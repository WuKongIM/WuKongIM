package reactor

import (
	"hash/fnv"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

type Reactor struct {
	subs []*reactorSub
	mu   sync.Mutex
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

func (r *Reactor) WakeIfNeed(channelId string, channelType uint8) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub := r.getSub(channelId, channelType)
	if sub.exist(wkutil.ChannelToKey(channelId, channelType)) {
		return
	}
	ch := NewChannel(channelId, channelType, func() {
		sub.advance()
	})
	sub.addChannel(ch)
}

func (r *Reactor) AddAction(action reactor.ChannelAction) bool {
	sub := r.getSub(action.FakeChannelId, action.ChannelType)
	return sub.addAction(action)
}

func (r *Reactor) MustAddAction(action reactor.ChannelAction) {
	sub := r.getSub(action.FakeChannelId, action.ChannelType)
	sub.mustAddAction(action)
}

func (r *Reactor) Advance(channelId string, channelType uint8) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub := r.getSub(channelId, channelType)
	sub.advance()
}

func (r *Reactor) getSub(channelId string, channelType uint8) *reactorSub {
	h := fnv.New32a()
	h.Write([]byte(wkutil.ChannelToKey(channelId, channelType)))
	i := h.Sum32() % uint32(len(r.subs))
	return r.subs[i]
}

func (r *Reactor) send(actions []reactor.ChannelAction) {
	options.Send(actions)
}
