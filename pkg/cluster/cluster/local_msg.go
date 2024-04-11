package cluster

import (
	"container/list"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type localReplicaStoreQueue struct {
	mu   sync.Mutex
	msgs []localReplicaMsg
	wklog.Log
}

func newLcalReplicaStoreQueue() *localReplicaStoreQueue {
	return &localReplicaStoreQueue{
		Log: wklog.NewWKLog("localReplicaStoreQueue"),
	}
}

func (l *localReplicaStoreQueue) add(msg replica.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.msgs = append(l.msgs, localReplicaMsg{Message: msg})
}

func (l *localReplicaStoreQueue) setStored(index uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	for i := 0; i < len(l.msgs); i++ {
		if l.msgs[i].Index == index {
			l.msgs[i].stored = true
			return true
		}
	}
	return false
}

func (l *localReplicaStoreQueue) getMsgs(index uint64) []localReplicaMsg {
	l.mu.Lock()
	defer l.mu.Unlock()

	msgs := make([]localReplicaMsg, 0)
	for i := 0; i < len(l.msgs); i++ {
		if l.msgs[i].Index <= index {
			msgs = append(msgs, l.msgs[i])
		}
	}

	return msgs
}

func (l *localReplicaStoreQueue) first() (localReplicaMsg, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.msgs) == 0 {
		return emptyLocalReplicaMsg, false
	}

	return l.msgs[0], true
}

func (l *localReplicaStoreQueue) firstIsStored() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.msgs) == 0 {
		return false
	}

	return l.msgs[0].stored
}

func (l *localReplicaStoreQueue) removeFirst() (localReplicaMsg, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.msgs) == 0 {
		return emptyLocalReplicaMsg, false

	}
	msg := l.msgs[0]
	l.msgs = l.msgs[1:]
	return msg, true

}

type localReplicaMsg struct {
	replica.Message
	stored bool // 是否已存储
}

var emptyLocalReplicaMsg = localReplicaMsg{}

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
