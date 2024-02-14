package cluster

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type commitWait struct {
	waitList []waitItem
	sync.Mutex
	wklog.Log
}

func newCommitWait() *commitWait {
	return &commitWait{
		Log: wklog.NewWKLog("commitWait"),
	}
}

type waitItem struct {
	logIndex uint64
	waitC    chan struct{}
}

func (c *commitWait) addWaitIndex(logIndex uint64) (<-chan struct{}, error) {
	c.Lock()
	defer c.Unlock()

	// for _, item := range c.waitList {
	// 	if item.logIndex == logIndex {
	// 		return nil, errors.New("logIndex already exists")
	// 	}
	// }

	waitC := make(chan struct{}, 1)
	c.waitList = append(c.waitList, waitItem{logIndex: logIndex, waitC: waitC})
	return waitC, nil
}

func (c *commitWait) commitIndex(logIndex uint64) {
	c.Lock()
	defer c.Unlock()

	maxIndex := 0
	exist := false
	for i, item := range c.waitList {
		if item.logIndex <= logIndex {
			select {
			case item.waitC <- struct{}{}:
			default:
				c.Warn("commitIndex notify failed", zap.Uint64("logIndex", logIndex))
			}
			maxIndex = i
			exist = true
		}
	}
	if exist {
		c.waitList = c.waitList[maxIndex+1:]
	}
}
