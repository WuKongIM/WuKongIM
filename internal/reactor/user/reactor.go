package reactor

import (
	"hash/fnv"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type Reactor struct {
	subs []*reactorSub
	mu   sync.Mutex
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
	if sub.exist(uid) {
		return
	}
	user := NewUser(wkutil.GenUUID(), uid)
	sub.addUser(user)
}

// Advance 推进，让用户立即执行下一个动作
func (r *Reactor) Advance(uid string) {
	sub := r.getSub(uid)
	sub.advance()
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
	sub := r.getSub(a.Uid)
	if a.Type == reactor.UserActionJoin {
		if !sub.exist(a.Uid) {
			r.WakeIfNeed(a.Uid)
		}
	}
	return sub.addAction(a)
}

func (r *Reactor) Exist(uid string) bool {
	return r.getSub(uid).exist(uid)
}

func (r *Reactor) ConnsByUid(uid string) []*reactor.Conn {
	return r.getSub(uid).connsByUid(uid)
}
func (r *Reactor) ConnCountByUid(uid string) int {
	return r.getSub(uid).connCountByUid(uid)
}
func (r *Reactor) ConnsByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []*reactor.Conn {
	return r.getSub(uid).connsByDeviceFlag(uid, deviceFlag)
}
func (r *Reactor) ConnCountByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) int {
	return r.getSub(uid).connCountByDeviceFlag(uid, deviceFlag)
}
func (r *Reactor) ConnById(uid string, fromNode uint64, id int64) *reactor.Conn {
	return r.getSub(uid).connById(uid, fromNode, id)
}
func (r *Reactor) LocalConnById(uid string, id int64) *reactor.Conn {
	return r.getSub(uid).localConnById(uid, id)
}

func (r *Reactor) UpdateConn(c *reactor.Conn) {
	r.getSub(c.Uid).updateConn(c.Uid, c.ConnId, c.FromNode, c)
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
