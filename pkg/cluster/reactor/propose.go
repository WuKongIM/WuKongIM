package reactor

import (
	"container/list"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type proposeReq struct {
	logs    []replica.Log
	handler *handler
	waitKey string
}

func newProposeReq(handler *handler, waitKey string, logs []replica.Log) proposeReq {
	return proposeReq{
		logs:    logs,
		handler: handler,
		waitKey: waitKey,
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
	Index     uint64
	committed bool
}

func (p ProposeResult) LogId() uint64 {
	return p.Id
}

func (p ProposeResult) LogIndex() uint64 {
	return p.Index
}
