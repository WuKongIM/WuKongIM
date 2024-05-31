package wkdb

import (
	"strconv"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
)

type dblock struct {
	channelClusterConfig *channelClusterConfigLock
	subscriberCountLock  *subscriberCountLock
	allowlistCountLock   *allowlistCountLock
	denylistCountLock    *denylistCountLock

	updateSessionUpdatedAtLock sync.Mutex
}

func newDBLock() *dblock {
	return &dblock{
		channelClusterConfig: newChannelClusterConfigLock(),
		subscriberCountLock:  newSubscriberCountLock(),
		allowlistCountLock:   newAllowlistCountLock(),
		denylistCountLock:    newDenylistCountLock(),
	}

}

func (d *dblock) start() {
	d.channelClusterConfig.StartCleanLoop()
	d.subscriberCountLock.StartCleanLoop()
	d.allowlistCountLock.StartCleanLoop()
	d.denylistCountLock.StartCleanLoop()
}

func (d *dblock) stop() {
	d.channelClusterConfig.StopCleanLoop()
	d.subscriberCountLock.StopCleanLoop()
	d.allowlistCountLock.StopCleanLoop()
	d.denylistCountLock.StopCleanLoop()
}

type channelClusterConfigLock struct {
	*keylock.KeyLock
}

func newChannelClusterConfigLock() *channelClusterConfigLock {
	return &channelClusterConfigLock{
		keylock.NewKeyLock(),
	}
}

func (c *channelClusterConfigLock) lockByChannel(channelId string, channelType uint8) {
	key := channelId + strconv.FormatInt(int64(channelType), 10)
	c.Lock(key)
}

func (c *channelClusterConfigLock) unlockByChannel(channelId string, channelType uint8) {
	key := channelId + strconv.FormatInt(int64(channelType), 10)
	c.Unlock(key)
}

type subscriberCountLock struct {
	*keylock.KeyLock
}

func newSubscriberCountLock() *subscriberCountLock {
	return &subscriberCountLock{
		keylock.NewKeyLock(),
	}
}

func (c *subscriberCountLock) lock(id uint64) {
	c.Lock(strconv.FormatUint(id, 10))
}

func (c *subscriberCountLock) unlock(id uint64) {
	c.Unlock(strconv.FormatUint(id, 10))
}

type allowlistCountLock struct {
	*keylock.KeyLock
}

func newAllowlistCountLock() *allowlistCountLock {
	return &allowlistCountLock{
		keylock.NewKeyLock(),
	}
}

func (c *allowlistCountLock) lock(id uint64) {
	c.Lock(strconv.FormatUint(id, 10))
}

func (c *allowlistCountLock) unlock(id uint64) {
	c.Unlock(strconv.FormatUint(id, 10))
}

type denylistCountLock struct {
	*keylock.KeyLock
}

func newDenylistCountLock() *denylistCountLock {
	return &denylistCountLock{
		keylock.NewKeyLock(),
	}
}

func (c *denylistCountLock) lock(id uint64) {
	c.Lock(strconv.FormatUint(id, 10))
}

func (c *denylistCountLock) unlock(id uint64) {
	c.Unlock(strconv.FormatUint(id, 10))
}
