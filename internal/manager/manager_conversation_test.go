package manager

import (
	"fmt"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	options.G = options.New()
	m.Run()
}

func newTestUpdater() *conversationUpdater {
	return newConversationUpdater(nil)
}

func makeTestChannelUpdate(channelId string, uids []string) *channelUpdate {
	userReadSeqs := make(map[string]uint64)
	for _, uid := range uids {
		userReadSeqs[uid] = 0
	}
	return &channelUpdate{
		ChannelId:    channelId,
		ChannelType:  wkproto.ChannelTypePerson,
		UserReadSeqs: userReadSeqs,
		TagKey:       "tag1",
		LastMsgSeq:   100,
	}
}

// 1. 基础功能测试
func TestChannelUpdate_SetAndGet(t *testing.T) {
	updater := newTestUpdater()
	update := makeTestChannelUpdate("channel1", []string{"user1", "user2"})

	updater.setChannelUpdate(update)

	updates := updater.getChannelUpdates()
	assert.Equal(t, 1, len(updates))
	assert.Equal(t, "channel1", updates[0].ChannelId)
	assert.Equal(t, 2, len(updates[0].UserReadSeqs))
}

func TestChannelUpdate_Remove(t *testing.T) {
	updater := newTestUpdater()
	update := makeTestChannelUpdate("channel1", []string{"user1"})
	updater.setChannelUpdate(update)

	updater.removeChannelUpdate("channel1", wkproto.ChannelTypePerson)

	updates := updater.getChannelUpdates()
	assert.Equal(t, 0, len(updates))

	// 验证倒排索引也被清理
	channels := updater.getUserChannels("user1", wkdb.ConversationTypeChat)
	assert.Equal(t, 0, len(channels))
}

func TestUserChannelUpdate_Remove(t *testing.T) {
	updater := newTestUpdater()
	update := makeTestChannelUpdate("channel1", []string{"user1", "user2"})
	updater.setChannelUpdate(update)

	updater.removeUserChannelUpdate("channel1", wkproto.ChannelTypePerson, "user1")

	// 验证 user1 被移除，user2 还在
	channels1 := updater.getUserChannels("user1", wkdb.ConversationTypeChat)
	assert.Equal(t, 0, len(channels1))

	channels2 := updater.getUserChannels("user2", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(channels2))

	updates := updater.getChannelUpdates()
	assert.Equal(t, 1, len(updates[0].UserReadSeqs))
	_, ok := updates[0].UserReadSeqs["user2"]
	assert.True(t, ok)
}

func TestGetUserChannels(t *testing.T) {
	updater := newTestUpdater()
	updater.setChannelUpdate(makeTestChannelUpdate("channel1", []string{"user1", "user2"}))
	updater.setChannelUpdate(makeTestChannelUpdate("channel2", []string{"user1", "user3"}))

	channels := updater.getUserChannels("user1", wkdb.ConversationTypeChat)
	assert.Equal(t, 2, len(channels))

	channels2 := updater.getUserChannels("user2", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(channels2))
	assert.Equal(t, "channel1", channels2[0].ChannelID)
}

// 2. 倒排索引测试
func TestUserIndex_AddUser(t *testing.T) {
	updater := newTestUpdater()
	update := makeTestChannelUpdate("channel1", []string{"user1"})
	updater.setChannelUpdate(update)

	assert.Equal(t, 1, len(updater.userIndex["user1"]))
}

func TestUserIndex_RemoveUser(t *testing.T) {
	updater := newTestUpdater()
	updater.setChannelUpdate(makeTestChannelUpdate("channel1", []string{"user1"}))
	updater.removeUserChannelUpdate("channel1", wkproto.ChannelTypePerson, "user1")

	assert.Equal(t, 0, len(updater.userIndex["user1"]))
}

func TestUserIndex_UpdateChannel(t *testing.T) {
	updater := newTestUpdater()
	// 初始 user1 在 channel1
	updater.setChannelUpdate(makeTestChannelUpdate("channel1", []string{"user1"}))
	assert.Equal(t, 1, len(updater.userIndex["user1"]))

	// 更新 channel1，移除 user1，添加 user2
	update2 := makeTestChannelUpdate("channel1", []string{"user2"})
	updater.setChannelUpdate(update2)

	assert.Equal(t, 0, len(updater.userIndex["user1"]))
	assert.Equal(t, 1, len(updater.userIndex["user2"]))
}

// 3. 边界条件测试
func TestEmptyUidSet(t *testing.T) {
	updater := newTestUpdater()
	update := makeTestChannelUpdate("channel1", []string{})
	updater.setChannelUpdate(update)

	updates := updater.getChannelUpdates()
	assert.Equal(t, 1, len(updates))
	assert.Equal(t, 0, len(updates[0].UserReadSeqs))
}

func TestNilUpdate(t *testing.T) {
	updater := newTestUpdater()
	updater.setChannelUpdate(nil)
	assert.Equal(t, 0, len(updater.getChannelUpdates()))
}

func TestNonExistentUser(t *testing.T) {
	updater := newTestUpdater()
	channels := updater.getUserChannels("none", wkdb.ConversationTypeChat)
	assert.Nil(t, channels)
}

func TestNonExistentChannel(t *testing.T) {
	updater := newTestUpdater()
	// 应该不报错
	updater.removeChannelUpdate("none", wkproto.ChannelTypePerson)
	updater.removeUserChannelUpdate("none", wkproto.ChannelTypePerson, "user1")
}

// 4. 并发测试
func TestConcurrent_SetAndGet(t *testing.T) {
	updater := newTestUpdater()
	wg := sync.WaitGroup{}
	channelCount := 100
	userPerChannel := 10

	// 并发写
	for i := 0; i < channelCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			uids := make([]string, userPerChannel)
			for j := 0; j < userPerChannel; j++ {
				uids[j] = fmt.Sprintf("user_%d_%d", id, j)
			}
			updater.setChannelUpdate(makeTestChannelUpdate(fmt.Sprintf("channel_%d", id), uids))
		}(i)
	}

	// 并发读
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			updater.getChannelUpdates()
			updater.getUserChannels("user_0_0", wkdb.ConversationTypeChat)
		}()
	}

	wg.Wait()
	assert.Equal(t, channelCount, len(updater.getChannelUpdates()))
}

func TestConcurrent_RemoveWhileRead(t *testing.T) {
	updater := newTestUpdater()
	wg := sync.WaitGroup{}
	channelCount := 100

	// 预先填充数据
	for i := 0; i < channelCount; i++ {
		updater.setChannelUpdate(makeTestChannelUpdate(fmt.Sprintf("channel_%d", i), []string{"user1"}))
	}

	// 并发读
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			updater.getUserChannels("user1", wkdb.ConversationTypeChat)
		}
	}()

	// 并发删
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < channelCount; i++ {
			updater.removeChannelUpdate(fmt.Sprintf("channel_%d", i), wkproto.ChannelTypePerson)
		}
	}()

	wg.Wait()
}

func TestConcurrent_SameChannel(t *testing.T) {
	updater := newTestUpdater()
	wg := sync.WaitGroup{}
	iter := 1000

	for i := 0; i < iter; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			updater.setChannelUpdate(makeTestChannelUpdate("same_channel", []string{fmt.Sprintf("user_%d", index)}))
		}(i)
	}

	wg.Wait()
	updates := updater.getChannelUpdates()
	assert.Equal(t, 1, len(updates))
	assert.Equal(t, 1, len(updates[0].UserReadSeqs))
}

// 5. 性能基准测试
func BenchmarkGetUserChannels(b *testing.B) {
	updater := newTestUpdater()
	channelCount := 1000
	for i := 0; i < channelCount; i++ {
		updater.setChannelUpdate(makeTestChannelUpdate(fmt.Sprintf("channel_%d", i), []string{"user1"}))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updater.getUserChannels("user1", wkdb.ConversationTypeChat)
	}
}

func BenchmarkSetChannelUpdate(b *testing.B) {
	updater := newTestUpdater()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updater.setChannelUpdate(makeTestChannelUpdate("channel", []string{"user1"}))
	}
}
