package wkdb

import (
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
)

type dblock struct {
	channelClusterConfig *channelClusterConfigLock
	subscriberCountLock  *subscriberCountLock
	allowlistCountLock   *allowlistCountLock
	denylistCountLock    *denylistCountLock
	totalLock            *totalLock

	userLock               *userLock
	addOrUpdateChannelLock *addOrUpdateChannelLock
	conversationLock       *conversationLock
}

func newDBLock() *dblock {
	return &dblock{
		channelClusterConfig:   newChannelClusterConfigLock(),
		subscriberCountLock:    newSubscriberCountLock(),
		allowlistCountLock:     newAllowlistCountLock(),
		denylistCountLock:      newDenylistCountLock(),
		userLock:               newUserLock(),
		totalLock:              newTotalLock(),
		addOrUpdateChannelLock: newAddOrUpdateChannelLock(),
		conversationLock:       newConversationLock(),
	}

}

func (d *dblock) start() {
	d.channelClusterConfig.StartCleanLoop()
	d.subscriberCountLock.StartCleanLoop()
	d.allowlistCountLock.StartCleanLoop()
	d.denylistCountLock.StartCleanLoop()
	d.userLock.StartCleanLoop()
	d.addOrUpdateChannelLock.StartCleanLoop()
	d.conversationLock.StartCleanLoop()
}

func (d *dblock) stop() {
	d.channelClusterConfig.StopCleanLoop()
	d.subscriberCountLock.StopCleanLoop()
	d.allowlistCountLock.StopCleanLoop()
	d.denylistCountLock.StopCleanLoop()
	d.userLock.StopCleanLoop()
	d.addOrUpdateChannelLock.StopCleanLoop()
	d.conversationLock.StopCleanLoop()
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

type userLock struct {
	*keylock.KeyLock
}

func newUserLock() *userLock {

	return &userLock{
		keylock.NewKeyLock(),
	}
}

// func (u *userLock) lock(uid string) {
// 	u.Lock(uid)
// }

// func (u *userLock) unlock(uid string) {
// 	u.Unlock(uid)
// }

type totalLock struct {
	*keylock.KeyLock
}

func newTotalLock() *totalLock {
	return &totalLock{
		keylock.NewKeyLock(),
	}
}

func (t *totalLock) lockMessageCount() {
	t.Lock("__message_count")
}

func (t *totalLock) unlockMessageCount() {
	t.Unlock("__message_count")
}

func (t *totalLock) lockSessionCount() {
	t.Lock("__session_count")
}

func (t *totalLock) unlockSessionCount() {
	t.Unlock("__session_count")
}

func (t *totalLock) lockUserCount() {
	t.Lock("__user_count")
}

func (t *totalLock) unlockUserCount() {
	t.Unlock("__user_count")
}

func (t *totalLock) lockDeviceCount() {
	t.Lock("__device_count")
}

func (t *totalLock) unlockDeviceCount() {
	t.Unlock("__device_count")
}

func (t *totalLock) lockConversationCount() {
	t.Lock("__conversation_count")
}

func (t *totalLock) unlockConversationCount() {
	t.Unlock("__conversation_count")
}

func (t *totalLock) lockChannelCount() {
	t.Lock("__channel_count")
}

func (t *totalLock) unlockChannelCount() {
	t.Unlock("__channel_count")
}

func (t *totalLock) lockChannelClusterConfigCount() {
	t.Lock("__channel_cluster_config_count")
}

func (t *totalLock) unlockChannelClusterConfigCount() {
	t.Unlock("__channel_cluster_config_count")
}

type addOrUpdateChannelLock struct {
	*keylock.KeyLock
}

func newAddOrUpdateChannelLock() *addOrUpdateChannelLock {
	return &addOrUpdateChannelLock{
		keylock.NewKeyLock(),
	}
}

// func (c *addOrUpdateChannelLock) lock(channelId string, channelType uint8) {
// 	key := channelId + strconv.FormatInt(int64(channelType), 10)
// 	c.Lock(key)
// }

// func (c *addOrUpdateChannelLock) unlock(channelId string, channelType uint8) {
// 	key := channelId + strconv.FormatInt(int64(channelType), 10)
// 	c.Unlock(key)
// }

type conversationLock struct {
	*keylock.KeyLock
}

func newConversationLock() *conversationLock {
	return &conversationLock{
		keylock.NewKeyLock(),
	}
}

func (c *conversationLock) lock(uid string) {
	c.Lock(uid)
}

func (c *conversationLock) unlock(uid string) {
	c.Unlock(uid)
}
