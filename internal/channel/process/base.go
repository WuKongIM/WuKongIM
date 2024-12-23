package process

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/common"
	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type Channel struct {
	wklog.Log
	processPool *ants.Pool
	// 正在选举中的频道，防止重复选举
	electioningMap  map[string]bool
	electioningLock sync.RWMutex
	client          *ingress.Client
	commonService   *common.Service
}

func New() *Channel {
	ch := &Channel{
		Log:            wklog.NewWKLog("processChannel"),
		electioningMap: make(map[string]bool),
		client:         ingress.NewClient(),
		commonService:  common.NewService(),
	}
	var err error
	ch.processPool, err = ants.NewPool(options.G.GoPool.ChannelProcess, ants.WithNonblocking(true), ants.WithPanicHandler(func(i interface{}) {
		ch.Panic("channel process pool is panic", zap.Any("err", err), zap.Stack("stack"))
	}))
	if err != nil {
		ch.Panic("new channel process pool failed", zap.Error(err))
	}
	return ch
}

func (c *Channel) WithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), options.G.Channel.ProcessTimeout)

}

func (c *Channel) setElectioning(channelKey string) {
	c.electioningLock.Lock()
	defer c.electioningLock.Unlock()
	c.electioningMap[channelKey] = true
}

func (c *Channel) unsetElectioning(channelKey string) {
	c.electioningLock.Lock()
	defer c.electioningLock.Unlock()
	delete(c.electioningMap, channelKey)
}

func (c *Channel) isElectioning(channelKey string) bool {
	c.electioningLock.RLock()
	defer c.electioningLock.RUnlock()
	return c.electioningMap[channelKey]
}
