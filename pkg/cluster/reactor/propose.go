package reactor

import (
	"container/list"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type proposeReq struct {
	logs []replica.Log
	key  string
}

func newProposeReq(key string, logs []replica.Log) proposeReq {
	return proposeReq{
		logs: logs,
		key:  key,
	}
}

var emptyProposeReq = proposeReq{}

type proposeQueue struct {
	reqs list.List // propose消息队列
	mu   sync.Mutex
	wklog.Log
}

func newProposeQueue() *proposeQueue {
	return &proposeQueue{
		reqs: list.List{},
		Log:  wklog.NewWKLog("proposeQueue"),
	}
}

func (p *proposeQueue) push(req proposeReq) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs.PushBack(req)
}

func (p *proposeQueue) pop() (proposeReq, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reqs.Len() == 0 {
		return emptyProposeReq, false
	}

	e := p.reqs.Front()
	p.reqs.Remove(e)
	req := e.Value.(proposeReq)
	return req, true
}

type ProposeResult struct {
	Id        uint64
	LogIndex  uint64
	committed bool
}
