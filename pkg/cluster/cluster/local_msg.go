package cluster

import (
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
