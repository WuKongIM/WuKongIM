package clusterevent

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
)

type ClusterEventManager struct {
	watchCh chan *ClusterEvent
	stopper *syncutil.Stopper
	wklog.Log
}

func NewClusterEventManager() *ClusterEventManager {
	return &ClusterEventManager{
		watchCh: make(chan *ClusterEvent),
		stopper: syncutil.NewStopper(),
		Log:     wklog.NewWKLog("ClusterEventManager"),
	}
}

func (c *ClusterEventManager) Start() error {
	c.stopper.RunWorker(func() {
		c.loop()
	})
	return nil
}

func (c *ClusterEventManager) Stop() {
	c.stopper.Stop()
}

func (c *ClusterEventManager) Watch() <-chan *ClusterEvent {
	return nil
}

func (c *ClusterEventManager) loop() {
	tick := time.NewTicker(time.Second * 1)
	for {
		select {
		case <-c.stopper.ShouldStop():
			return
		case <-tick.C:
		}
	}
}
