package clusterstore_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/stretchr/testify/assert"
)

func TestAddSubscribers(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()
	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	// 节点1添加订阅者
	err := s1.AddSubscribers(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 2000)

	// 节点2获取订阅者
	subscribers, err := s2.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, subscribers)

	// 节点3获取订阅者
	subscribers, err = s3.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, subscribers)

}

func TestRemoveSubscribers(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()
	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	// 节点1添加订阅者
	err := s1.AddSubscribers(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	// 节点2获取订阅者
	subscribers, err := s2.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, subscribers)

	// 节点1删除订阅者
	err = s1.RemoveSubscribers(channelID, channelType, []string{"123"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	// 节点2获取订阅者
	subscribers, err = s2.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"34"}, subscribers)

	// 节点3获取订阅者
	subscribers, err = s3.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"34"}, subscribers)
}

func TestRemoveAllSubscriber(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	// 节点1添加订阅者
	err := s1.AddSubscribers(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	// 节点2获取订阅者
	subscribers, err := s2.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, subscribers)

	// 节点1删除订阅者
	err = s1.RemoveAllSubscriber(channelID, channelType)
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	// 节点2获取订阅者
	subscribers, err = s2.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(subscribers))

	// 节点2获取订阅者
	subscribers, err = s3.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(subscribers))
}

func TestAddOrUpdateChannel(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	err := s1.AddOrUpdateChannel(&wkstore.ChannelInfo{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         true,
		Large:       true,
	})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	channelInfo, err := s2.GetChannel(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, true, channelInfo.Ban)
	assert.Equal(t, true, channelInfo.Large)

	channelInfo, err = s1.GetChannel(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, true, channelInfo.Ban)
	assert.Equal(t, true, channelInfo.Large)

	channelInfo, err = s3.GetChannel(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, true, channelInfo.Ban)
	assert.Equal(t, true, channelInfo.Large)
}

func TestDeleteChannel(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	err := s1.AddOrUpdateChannel(&wkstore.ChannelInfo{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         true,
		Large:       true,
	})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	channelInfo, err := s2.GetChannel(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, true, channelInfo.Ban)
	assert.Equal(t, true, channelInfo.Large)

	channelInfo, err = s1.GetChannel(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, true, channelInfo.Ban)
	assert.Equal(t, true, channelInfo.Large)

	err = s1.DeleteChannel(channelID, channelType)
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	channelInfo, err = s2.GetChannel(channelID, channelType)
	assert.NoError(t, err)
	assert.Nil(t, channelInfo)

	channelInfo, err = s1.GetChannel(channelID, channelType)
	assert.NoError(t, err)
	assert.Nil(t, channelInfo)

	channelInfo, err = s3.GetChannel(channelID, channelType)
	assert.NoError(t, err)
	assert.Nil(t, channelInfo)
}

func TestAddAndGetDenylist(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	err := s1.AddDenylist(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	denylist, err := s2.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, denylist)

	denylist, err = s1.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, denylist)

	denylist, err = s3.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, denylist)
}

func TestRemoveAllDenylist(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	err := s1.AddDenylist(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	denylist, err := s2.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, denylist)

	denylist, err = s1.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, denylist)

	err = s1.RemoveAllDenylist(channelID, channelType)
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	denylist, err = s2.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(denylist))

	denylist, err = s1.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(denylist))

	denylist, err = s3.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(denylist))
}

func TestRemoveDenylist(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	err := s1.AddDenylist(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	denylist, err := s2.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, denylist)

	denylist, err = s1.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, denylist)

	err = s1.RemoveDenylist(channelID, channelType, []string{"123"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	denylist, err = s2.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"34"}, denylist)

	denylist, err = s1.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"34"}, denylist)

	denylist, err = s3.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"34"}, denylist)
}

func TestAddAndGetAllowlist(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	err := s1.AddAllowlist(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	allowlist, err := s2.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, allowlist)

	allowlist, err = s1.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, allowlist)

	allowlist, err = s3.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, allowlist)
}

func TestRemoveAllowlist(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2
	err := s1.AddAllowlist(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	allowlist, err := s2.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, allowlist)

	allowlist, err = s1.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, allowlist)

	err = s1.RemoveAllowlist(channelID, channelType, []string{"123"})
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	allowlist, err = s2.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"34"}, allowlist)

	allowlist, err = s1.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"34"}, allowlist)

	allowlist, err = s3.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"34"}, allowlist)
}

func TestRemoveAllAllowlist(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2
	err := s1.AddAllowlist(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	allowlist, err := s2.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, allowlist)

	allowlist, err = s1.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, allowlist)

	err = s1.RemoveAllAllowlist(channelID, channelType)
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	allowlist, err = s2.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(allowlist))

	allowlist, err = s1.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(allowlist))

	allowlist, err = s3.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(allowlist))
}
