package wkdb

import (
	"strconv"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
)

type dblock struct {
	channelClusterConfig channelClusterConfigLock

	updateSessionUpdatedAtLock sync.Mutex
}

func newDBLock() *dblock {
	return &dblock{
		channelClusterConfig: *newChannelClusterConfigLock(),
	}

}

func (d *dblock) start() {
	d.channelClusterConfig.start()
}

func (d *dblock) stop() {
	d.channelClusterConfig.stop()
}

type channelClusterConfigLock struct {
	channelKeyLock *keylock.KeyLock
}

func newChannelClusterConfigLock() *channelClusterConfigLock {
	return &channelClusterConfigLock{
		channelKeyLock: keylock.NewKeyLock(),
	}
}

func (c *channelClusterConfigLock) start() {
	c.channelKeyLock.StartCleanLoop()
}

func (c *channelClusterConfigLock) stop() {
	c.channelKeyLock.StopCleanLoop()
}

func (c *channelClusterConfigLock) lockByChannel(channelId string, channelType uint8) {
	key := channelId + strconv.FormatInt(int64(channelType), 10)
	c.channelKeyLock.Lock(key)
}

func (c *channelClusterConfigLock) unlockByChannel(channelId string, channelType uint8) {
	key := channelId + strconv.FormatInt(int64(channelType), 10)
	c.channelKeyLock.Unlock(key)
}
