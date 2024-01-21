package replica

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type logNode struct {
	logIndex uint64
	waitC    chan struct{}
	next     *logNode
}

type commitWait struct {
	sync.Mutex

	head *logNode
	tail *logNode
	wklog.Log
}

func newCommitWait() *commitWait {
	return &commitWait{
		Log: wklog.NewWKLog("commitWait"),
	}
}

func (c *commitWait) addWaitIndex(logIndex uint64) <-chan struct{} {
	c.Lock()
	defer c.Unlock()

	n := &logNode{
		logIndex: logIndex,
		waitC:    make(chan struct{}, 1),
		next:     nil,
	}
	if c.head == nil {
		c.head = n
	} else {
		c.tail.next = n
	}
	c.tail = n

	return n.waitC
}

func (c *commitWait) commitIndex(logIndex uint64) {
	c.Lock()
	defer c.Unlock()

	if c.head == nil {
		return
	}
	for c.head != nil && c.head.logIndex <= logIndex {
		c.head.waitC <- struct{}{}
		c.head = c.head.next
	}

}
