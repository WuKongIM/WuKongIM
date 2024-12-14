package reactor

import (
	"hash/fnv"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type Reactor struct {
	subs  []*reactorSub
	proto wkproto.Protocol
	mu    sync.Mutex
}

func NewReactor(opt ...Option) *Reactor {
	options = NewOptions()
	for _, op := range opt {
		op(options)
	}
	r := &Reactor{}
	for i := 0; i < options.SubCount; i++ {
		r.subs = append(r.subs, newReactorSub(i, r))
	}
	return r
}

func (r *Reactor) Start() error {

	var err error
	for _, sub := range r.subs {
		err = sub.start()
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

func (r *Reactor) WakeIfNeed(uid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub := r.getSub(uid)
	user := sub.user(uid)
	if user != nil {
		return
	}
	user = NewUser(wkutil.GenUUID(), uid)
	sub.addUser(user)
}

func (r *Reactor) CloseConn(c *reactor.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.getSub(c.Uid)
	user := sub.user(c.Uid)
	if user == nil {
		return
	}
	user.conns.remove(c)
}

func (r *Reactor) AddAction(a reactor.UserAction) bool {
	return r.getSub(a.Uid).addAction(a)
}

func (r *Reactor) SetProto(proto wkproto.Protocol) {
	r.proto = proto
}

func (r *Reactor) GetProto() wkproto.Protocol {
	return r.proto
}

func (r *Reactor) getSub(uid string) *reactorSub {
	h := fnv.New32a()
	h.Write([]byte(uid))
	i := h.Sum32() % uint32(len(r.subs))
	return r.subs[i]
}

func (r *Reactor) send(actions []reactor.UserAction) {
	if options.Send != nil {
		options.Send(actions)
	}
}
