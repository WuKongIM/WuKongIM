package manager

import (
	"encoding/json"
	"os"
	"path"
	"slices"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/fasthash"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type ConversationManager struct {
	updaters     []*conversationUpdater
	updaterCount int
	client       *ingress.Client
	stopper      *syncutil.Stopper
	wklog.Log
}

func NewConversationManager(updaterCount int) *ConversationManager {

	updaters := make([]*conversationUpdater, updaterCount)
	client := ingress.NewClient()
	for i := 0; i < updaterCount; i++ {
		updaters[i] = newConversationUpdater(client)
	}
	return &ConversationManager{
		updaterCount: updaterCount,
		updaters:     updaters,
		client:       client,
		Log:          wklog.NewWKLog("ConversationManager"),
		stopper:      syncutil.NewStopper(),
	}
}

func (c *ConversationManager) Push(fakeChannelId string, channelType uint8, tagKey string, events []*eventbus.Event) {
	if len(events) == 0 {
		return
	}

	if channelType == wkproto.ChannelTypeLive { // 直播频道不添加会话
		return
	}

	var lastMsgSeq uint64 = 0
	var firstMsgSeq uint64 = 0
	for _, event := range events {
		if event.Frame.GetNoPersist() {
			continue
		}

		if event.MessageSeq > lastMsgSeq {
			lastMsgSeq = event.MessageSeq
		}
		if firstMsgSeq == 0 {
			firstMsgSeq = event.MessageSeq
		}
	}
	if lastMsgSeq == 0 {
		return
	}

	// 如果是cmd频道，则直接更新
	if options.G.IsCmdChannel(fakeChannelId) {
		index := c.getUpdaterIndex(fakeChannelId)
		c.updaters[index].push(fakeChannelId, channelType, tagKey, lastMsgSeq)
		return
	}

	// 如果是个人频道并且开启了白名单，则不需要更新最近会话,因为在添加白名单的时候就已经添加了最近会话
	if channelType == wkproto.ChannelTypePerson && !options.G.WhitelistOffOfPerson {
		return
	}

	// 如果是客服频道或个人频道，则需要更新最近会话
	if channelType == wkproto.ChannelTypeCustomerService || channelType == wkproto.ChannelTypePerson {
		if firstMsgSeq == 1 {
			index := c.getUpdaterIndex(fakeChannelId)
			c.updaters[index].push(fakeChannelId, channelType, tagKey, lastMsgSeq)
		}
	}

}

func (c *ConversationManager) Start() error {
	c.loadFromFile() // 从本地文件加载
	c.stopper.RunWorker(c.loopStoreConversations)
	return nil
}

func (c *ConversationManager) Stop() {
	c.stopper.Stop()

	// 保存所有未存储的频道到本地文件里
	c.saveToFile()
}

func (c *ConversationManager) DeleteFromCache(uid string, channelId string, channelType uint8) error {
	if channelType == wkproto.ChannelTypeLive { // 直播频道不删除会话
		return nil
	}
	index := c.getUpdaterIndex(channelId)
	c.updaters[index].removeUserChannelUpdate(channelId, channelType, uid)
	return nil
}

func (c *ConversationManager) saveToFile() {

	conversationDir := path.Join(options.G.DataDir, "conversationv2")
	err := os.MkdirAll(conversationDir, 0755)
	if err != nil {
		c.Error("mkdir conversation dir err", zap.Error(err))
		return
	}

	allUpdates := make([]*channelUpdate, 0)
	for _, updater := range c.updaters {
		allUpdates = append(allUpdates, updater.getChannelUpdates()...)
	}
	data, err := json.Marshal(allUpdates)
	if err != nil {
		c.Error("save conversations to file failed", zap.Error(err))
		return
	}

	err = os.WriteFile(path.Join(conversationDir, "conversations.json"), data, 0644)
	if err != nil {
		c.Error("save conversations to file failed", zap.Error(err))
		return
	}
}

func (c *ConversationManager) loadFromFile() {
	conversationPath := path.Join(options.G.DataDir, "conversationv2", "conversations.json")
	if !wkutil.FileExists(conversationPath) {
		return
	}
	data, err := wkutil.ReadFile(conversationPath)
	if err != nil {
		c.Error("load conversations from file failed", zap.Error(err))
		return
	}

	if len(data) == 0 {
		return
	}

	var allUpdates []*channelUpdate
	err = json.Unmarshal(data, &allUpdates)
	if err != nil {
		c.Error("load conversations from file failed", zap.Error(err))
		return
	}

	for _, update := range allUpdates {
		c.updaters[c.getUpdaterIndex(update.ChannelId)].setChannelUpdate(update.ChannelId, update.ChannelType, update.TagKey, update.Uids, update.LastMsgSeq)
	}

	// 删除conversations.json
	err = os.Remove(conversationPath)
	if err != nil {
		c.Error("remove conversations.json failed", zap.Error(err))
	}
}

// GetUserChannels 获取用户订阅的频道
func (c *ConversationManager) GetUserChannelsFromCache(uid string, conversationType wkdb.ConversationType) ([]wkproto.Channel, error) {
	var allChannels []wkproto.Channel
	for _, updater := range c.updaters {
		channels, err := updater.getUserChannels(uid, conversationType)
		if err != nil {
			return nil, err
		}
		allChannels = append(allChannels, channels...)
	}
	return allChannels, nil
}

func (c *ConversationManager) loopStoreConversations() {
	tk := time.NewTicker(options.G.Conversation.SyncInterval)
	defer tk.Stop()
	for {
		select {
		case <-tk.C:
			c.storeConversations()
		case <-c.stopper.ShouldStop():
			return
		}
	}
}

func (c *ConversationManager) storeConversations() {
	var conversations []wkdb.Conversation

	// 每次存储数量
	for _, updater := range c.updaters {
		updates := updater.getChannelUpdates()
		for _, update := range updates {
			conversationType := wkdb.ConversationTypeChat
			if options.G.IsCmdChannel(update.ChannelId) {
				conversationType = wkdb.ConversationTypeCMD
			}
			for _, uid := range update.Uids {
				createdAt := time.Now()
				updatedAt := time.Now()
				conversations = append(conversations, wkdb.Conversation{
					ChannelId:   update.ChannelId,
					ChannelType: update.ChannelType,
					Uid:         uid,
					Type:        conversationType,
					CreatedAt:   &createdAt,
					UpdatedAt:   &updatedAt,
				})
				if len(conversations) >= options.G.Conversation.SyncOnce {
					goto store
				}
			}
		}
	}
store:
	if len(conversations) > 0 {
		err := service.Store.AddConversationsIfNotExist(conversations)
		if err != nil {
			c.Error("store conversations failed", zap.Error(err), zap.Int("conversations", len(conversations)))
			return
		}
		// 删除已存储的频道
		for _, conversation := range conversations {
			updater := c.updaters[c.getUpdaterIndex(conversation.ChannelId)]
			updater.removeChannelUpdate(conversation.ChannelId, conversation.ChannelType)
		}
	}
}

func (c *ConversationManager) getUpdaterIndex(fakeChannelId string) int {
	return int(fasthash.Hash(fakeChannelId) % uint32(c.updaterCount))
}

type channelUpdate struct {
	ChannelId   string   `json:"channel_id"`   // 频道ID
	ChannelType uint8    `json:"channel_type"` // 频道类型
	Uids        []string `json:"uids"`         // 更新的用户
	TagKey      string   `json:"tag_key"`      // 标签Key
	LastMsgSeq  uint64   `json:"last_msg_seq"` // 最后一条消息的序号
}

type conversationUpdater struct {
	waitUpdates map[string]*channelUpdate // 等待更新的频道
	sync.RWMutex
	client *ingress.Client
	wklog.Log
}

func newConversationUpdater(client *ingress.Client) *conversationUpdater {
	return &conversationUpdater{
		waitUpdates: make(map[string]*channelUpdate),
		client:      client,
		Log:         wklog.NewWKLog("conversationUpdater"),
	}
}

func (c *conversationUpdater) push(fakeChannelId string, channelType uint8, tagKey string, lastMsgSeq uint64) {

	key := wkutil.ChannelToKey(fakeChannelId, channelType)
	c.RLock()
	update := c.waitUpdates[key]
	c.RUnlock()
	if update != nil && (update.LastMsgSeq >= lastMsgSeq || tagKey == update.TagKey) {
		return
	}

	tag := service.TagManager.Get(tagKey)
	if tag == nil {
		c.Warn("warn: tag not found, conversation not updated", zap.String("tagKey", tagKey), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return
	}

	nodeUsers := tag.GetNodeUsers(options.G.Cluster.NodeId)
	if len(nodeUsers) == 0 {
		return
	}
	c.Lock()
	c.waitUpdates[key] = &channelUpdate{ChannelId: fakeChannelId, ChannelType: channelType, Uids: nodeUsers, TagKey: tagKey, LastMsgSeq: lastMsgSeq}
	c.Unlock()
}

func (c *conversationUpdater) setChannelUpdate(fakeChannelId string, channelType uint8, tagKey string, uids []string, lastMsgSeq uint64) {
	key := wkutil.ChannelToKey(fakeChannelId, channelType)
	c.Lock()
	c.waitUpdates[key] = &channelUpdate{ChannelId: fakeChannelId, ChannelType: channelType, Uids: uids, TagKey: tagKey, LastMsgSeq: lastMsgSeq}
	c.Unlock()
}

// 获取用户订阅的频道
func (c *conversationUpdater) getUserChannels(uid string, conversationType wkdb.ConversationType) ([]wkproto.Channel, error) {
	c.RLock()
	defer c.RUnlock()
	var channels []wkproto.Channel
	for _, channelUpdate := range c.waitUpdates {

		isCMDChannel := options.G.IsCmdChannel(channelUpdate.ChannelId)

		if isCMDChannel && conversationType != wkdb.ConversationTypeCMD {
			continue
		}
		if !isCMDChannel && conversationType != wkdb.ConversationTypeChat {
			continue
		}

		if slices.Contains(channelUpdate.Uids, uid) {
			channels = append(channels, wkproto.Channel{
				ChannelID:   channelUpdate.ChannelId,
				ChannelType: channelUpdate.ChannelType,
			})
		}
	}
	return channels, nil
}

// 获取所有需要更新的频道
func (c *conversationUpdater) getChannelUpdates() []*channelUpdate {
	c.RLock()
	defer c.RUnlock()
	var updates []*channelUpdate
	for _, update := range c.waitUpdates {
		updates = append(updates, update)
	}
	return updates
}

func (c *conversationUpdater) removeChannelUpdate(fakeChannelId string, channelType uint8) {
	c.Lock()
	delete(c.waitUpdates, wkutil.ChannelToKey(fakeChannelId, channelType))
	c.Unlock()
}

func (c *conversationUpdater) removeUserChannelUpdate(fakeChannelId string, channelType uint8, uid string) {
	c.Lock()
	defer c.Unlock()
	for _, update := range c.waitUpdates {
		if update.ChannelId == fakeChannelId && update.ChannelType == channelType && slices.Contains(update.Uids, uid) {
			filteredUids := make([]string, 0, len(update.Uids))
			for _, u := range update.Uids {
				if u != uid {
					filteredUids = append(filteredUids, u)
				}
			}
			update.Uids = filteredUids
			break
		}
	}
}
