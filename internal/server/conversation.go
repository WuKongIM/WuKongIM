package server

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

// ConversationManager ConversationManager
type ConversationManager struct {
	channelLock *keylock.KeyLock
	s           *Server
	wklog.Log
	queue                          *Queue
	userConversationMapBuckets     []map[string]*lru.Cache[string, *wkstore.Conversation]
	userConversationMapBucketLocks []sync.RWMutex
	bucketNum                      int
	needSaveConversationMap        map[string]bool
	needSaveConversationMapLock    sync.RWMutex
	stopChan                       chan struct{} //停止信号
	calcChan                       chan interface{}
	needSaveChan                   chan string
	crontab                        *cron.Cron
}

// NewConversationManager NewConversationManager
func NewConversationManager(s *Server) *ConversationManager {
	cm := &ConversationManager{
		s:                       s,
		bucketNum:               10,
		Log:                     wklog.NewWKLog("ConversationManager"),
		channelLock:             keylock.NewKeyLock(),
		needSaveConversationMap: map[string]bool{},
		stopChan:                make(chan struct{}),
		calcChan:                make(chan interface{}),
		needSaveChan:            make(chan string),
		queue:                   NewQueue(),
	}
	cm.userConversationMapBuckets = make([]map[string]*lru.Cache[string, *wkstore.Conversation], cm.bucketNum)
	cm.userConversationMapBucketLocks = make([]sync.RWMutex, cm.bucketNum)

	s.Schedule(time.Minute, func() {
		totalConversation := 0
		for i := 0; i < cm.bucketNum; i++ {
			cm.userConversationMapBucketLocks[i].Lock()
			userConversationMap := cm.userConversationMapBuckets[i]
			for _, cache := range userConversationMap {
				totalConversation += cache.Len()
			}
			cm.userConversationMapBucketLocks[i].Unlock()
		}
		s.monitor.ConversationCacheSet(totalConversation)
	})

	cm.crontab = cron.New(cron.WithSeconds())

	cm.crontab.AddFunc("0 0 2 * * ?", cm.clearExpireConversations) // 每条凌晨2点执行一次

	return cm
}

// Start Start
func (cm *ConversationManager) Start() {
	if cm.s.opts.Conversation.On {
		cm.channelLock.StartCleanLoop()
		go cm.saveloop()
		go cm.calcLoop()
		cm.crontab.Start()
	}

}

// Stop Stop
func (cm *ConversationManager) Stop() {
	if cm.s.opts.Conversation.On {
		close(cm.stopChan)
		cm.channelLock.StopCleanLoop()
		// Wait for the queue to complete
		cm.queue.Wait()

		cm.FlushConversations()

		cm.crontab.Stop()
	}
}

// 清空过期最近会话
func (cm *ConversationManager) clearExpireConversations() {
	for idx := range cm.userConversationMapBucketLocks {
		cm.userConversationMapBucketLocks[idx].Lock()
		userConversationMap := cm.userConversationMapBuckets[idx]
		for uid, cache := range userConversationMap {
			keys := cache.Keys()
			for _, key := range keys {
				conversation, _ := cache.Get(key)
				if conversation != nil {
					if conversation.Timestamp+int64(cm.s.opts.Conversation.CacheExpire.Seconds()) < time.Now().Unix() {
						cache.Remove(key)
					}
				}
			}
			if cache.Len() == 0 {
				delete(userConversationMap, uid)
			}
		}
		cm.userConversationMapBucketLocks[idx].Unlock()
	}
}

// 保存最近会话
func (cm *ConversationManager) calcLoop() {
	for {
		messageMapObj := cm.queue.Pop()
		if messageMapObj == nil {
			continue
		}
		messageMap := messageMapObj.(map[string]interface{})
		message := messageMap["message"].(*Message)
		subscribers := messageMap["subscribers"].([]string)

		for _, subscriber := range subscribers {
			cm.calConversation(message, subscriber)
		}
	}
}

func (cm *ConversationManager) saveloop() {
	ticker := time.NewTicker(cm.s.opts.Conversation.SyncInterval)

	needSync := false
	noSaveCount := 0
	for {
		if noSaveCount >= cm.s.opts.Conversation.SyncOnce {
			needSync = true
		}
		if needSync {
			noSaveCount = 0
			cm.FlushConversations()
			needSync = false
		}
		select {
		case uid := <-cm.needSaveChan:
			cm.needSaveConversationMapLock.Lock()
			if !cm.needSaveConversationMap[uid] {
				cm.needSaveConversationMap[uid] = true
				noSaveCount++
			}
			cm.needSaveConversationMapLock.Unlock()

		case <-ticker.C:
			if noSaveCount > 0 {
				needSync = true
			}
		case <-cm.stopChan:
			return
		}
	}
}

// PushMessage PushMessage
func (cm *ConversationManager) PushMessage(message *Message, subscribers []string) {
	if !cm.s.opts.Conversation.On {
		return
	}

	cm.queue.Push(map[string]interface{}{
		"message":     message,
		"subscribers": subscribers,
	})
}

// SetConversationUnread set unread data from conversation
func (cm *ConversationManager) SetConversationUnread(uid string, channelID string, channelType uint8, unread int, messageSeq uint32) error {
	conversationCache := cm.getUserConversationCache(uid)
	for _, key := range conversationCache.Keys() {
		conversation, _ := conversationCache.Get(key)
		if channelID == conversation.ChannelID && channelType == conversation.ChannelType {
			conversation.UnreadCount = unread
			if messageSeq > 0 {
				conversation.LastMsgSeq = messageSeq
			}
			cm.setNeedSave(uid)
			return nil
		}
	}
	conversation, err := cm.s.store.GetConversation(uid, channelID, channelType)
	if err != nil {
		return err
	}
	if conversation != nil {
		conversation.UnreadCount = unread
		if messageSeq > 0 {
			conversation.LastMsgSeq = messageSeq
		}
		conversationCache.Add(cm.getChannelKey(conversation.ChannelID, conversation.ChannelType), conversation)
		cm.setNeedSave(uid)
	}
	return nil
}

func (cm *ConversationManager) GetConversation(uid string, channelID string, channelType uint8) *wkstore.Conversation {

	conversations := cm.getUserCacheConversations(uid)
	if len(conversations) > 0 {
		for _, conversation := range conversations {
			if conversation.ChannelID == channelID && conversation.ChannelType == channelType {
				return conversation
			}
		}
	}

	conversation, err := cm.s.store.GetConversation(uid, channelID, channelType)
	if err != nil {
		cm.Error("查询最近会话失败！", zap.Error(err), zap.String("uid", uid), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
	}

	return conversation

}

// DeleteConversation 删除最近会话
func (cm *ConversationManager) DeleteConversation(uids []string, channelID string, channelType uint8) error {
	if len(uids) == 0 {
		return nil
	}
	for _, uid := range uids {
		conversationCache := cm.getUserConversationCache(uid)
		keys := conversationCache.Keys()
		for _, key := range keys {
			channelKey := cm.getChannelKey(channelID, channelType)
			if channelKey == key {
				conversationCache.Remove(key)
				break
			}
		}
		err := cm.s.store.DeleteConversation(uid, channelID, channelType)
		if err != nil {
			cm.Error("从数据库删除最近会话失败！", zap.Error(err), zap.String("uid", uid), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		}
	}
	return nil
}

func (cm *ConversationManager) getUserAllConversationMapFromStore(uid string) ([]*wkstore.Conversation, error) {
	conversations, err := cm.s.store.GetConversations(uid)
	if err != nil {
		cm.Error("Failed to get the list of recent conversations", zap.String("uid", uid), zap.Error(err))
		return nil, err
	}
	return conversations, nil
}

func (cm *ConversationManager) newLRUCache() *lru.Cache[string, *wkstore.Conversation] {
	c, _ := lru.New[string, *wkstore.Conversation](cm.s.opts.Conversation.UserMaxCount)
	return c
}

// FlushConversations 同步最近会话
func (cm *ConversationManager) FlushConversations() {

	cm.needSaveConversationMapLock.RLock()
	needSaveUIDs := make([]string, 0, len(cm.needSaveConversationMap))
	for uid := range cm.needSaveConversationMap {
		needSaveUIDs = append(needSaveUIDs, uid)
	}
	cm.needSaveConversationMapLock.RUnlock()

	if len(needSaveUIDs) > 0 {
		cm.Debug("Save conversation", zap.Int("count", len(needSaveUIDs)))
		for _, uid := range needSaveUIDs {
			cm.flushUserConversations(uid)
		}
	}

}

func (cm *ConversationManager) flushUserConversations(uid string) {

	conversationCache := cm.getUserConversationCache(uid)
	conversations := make([]*wkstore.Conversation, 0, len(conversationCache.Keys()))
	for _, key := range conversationCache.Keys() {
		conversationObj, ok := conversationCache.Get(key)
		if ok {
			conversations = append(conversations, conversationObj)
		}

	}
	err := cm.s.store.AddOrUpdateConversations(uid, conversations)
	if err != nil {
		cm.Warn("Failed to store conversation data", zap.Error(err))
	} else {
		cm.needSaveConversationMapLock.Lock()
		delete(cm.needSaveConversationMap, uid)
		cm.needSaveConversationMapLock.Unlock()

		// 移除过期的最近会话缓存
		for _, conversation := range conversations {
			if conversation.Timestamp+int64(cm.s.opts.Conversation.CacheExpire.Seconds()) < time.Now().Unix() {
				key := cm.getChannelKey(conversation.ChannelID, conversation.ChannelType)
				conversationCache.Remove(key)
			}
		}
	}
}

func (cm *ConversationManager) getUserConversationCache(uid string) *lru.Cache[string, *wkstore.Conversation] {
	pos := int(wkutil.HashCrc32(uid) % uint32(cm.bucketNum))
	cm.userConversationMapBucketLocks[pos].RLock()
	userConversationMap := cm.userConversationMapBuckets[pos]
	if userConversationMap == nil {
		userConversationMap = make(map[string]*lru.Cache[string, *wkstore.Conversation])
		cm.userConversationMapBuckets[pos] = userConversationMap
	}

	cm.userConversationMapBucketLocks[pos].RUnlock()
	cm.channelLock.Lock(uid)
	cache := userConversationMap[uid]
	if cache == nil {
		cache = cm.newLRUCache()
		userConversationMap[uid] = cache
	}
	cm.channelLock.Unlock(uid)

	return cache
}

func (cm *ConversationManager) getUserCacheConversations(uid string) []*wkstore.Conversation {
	cache := cm.getUserConversationCache(uid)

	conversations := make([]*wkstore.Conversation, 0, len(cache.Keys()))
	for _, key := range cache.Keys() {
		conversationObj, ok := cache.Get(key)
		if ok {
			conversations = append(conversations, conversationObj)
		}
	}
	return conversations

}

func (cm *ConversationManager) calConversation(message *Message, subscriber string) {
	conversationCache := cm.getUserConversationCache(subscriber)

	// if conversationCache.Len() == 0 {
	// 	var err error
	// 	conversations, err := cm.getUserAllConversationMapFromStore(subscriber)
	// 	if err != nil {
	// 		cm.Warn("Failed to get the conversation from the database", zap.Error(err))
	// 		return
	// 	}
	// 	for _, conversation := range conversations {
	// 		channelKey := cm.getChannelKey(conversation.ChannelID, conversation.ChannelType)
	// 		conversationCache.Add(channelKey, conversation)

	// 	}
	// }

	channelID := message.ChannelID
	if message.ChannelType == wkproto.ChannelTypePerson && message.ChannelID == subscriber { // If it is a personal channel and the channel ID is equal to the subscriber, you need to swap fromUID and channelID
		channelID = message.FromUID
	}
	channelKey := cm.getChannelKey(channelID, message.ChannelType)

	cm.channelLock.Lock(channelKey)
	conversation, _ := conversationCache.Get(channelKey)
	cm.channelLock.Unlock(channelKey)

	if conversation == nil {
		var err error
		conversation, err = cm.s.store.GetConversation(subscriber, channelID, message.ChannelType)
		if err != nil {
			cm.Error("获取某个最接近会话失败！", zap.String("subscriber", subscriber), zap.String("channelID", channelID), zap.Uint8("channelType", message.ChannelType), zap.Error(err))
		}
	}

	var modify = false
	if conversation == nil {
		unreadCount := 0
		if message.RedDot && message.FromUID != subscriber { //  message.FromUID != subscriber 自己发的消息不显示红点
			unreadCount = 1
		}
		conversation = &wkstore.Conversation{
			UID:             subscriber,
			ChannelID:       channelID,
			ChannelType:     message.ChannelType,
			UnreadCount:     unreadCount,
			Timestamp:       int64(message.Timestamp),
			LastMsgSeq:      message.MessageSeq,
			LastClientMsgNo: message.ClientMsgNo,
			LastMsgID:       message.MessageID,
			Version:         time.Now().UnixNano() / 1e6,
		}
		modify = true
	} else {

		if message.RedDot && message.FromUID != subscriber { //  message.FromUID != subscriber 自己发的消息不显示红点
			conversation.UnreadCount++
			modify = true
		}
		if conversation.LastMsgSeq < message.MessageSeq { // 只有当前会话的messageSeq小于当前消息的messageSeq才更新
			conversation.Timestamp = int64(message.Timestamp)
			conversation.LastClientMsgNo = message.ClientMsgNo
			conversation.LastMsgSeq = message.MessageSeq
			conversation.LastMsgID = message.MessageID
			modify = true
		}
		if modify {
			conversation.Version = time.Now().UnixNano() / 1e6
		}
	}
	if modify {
		cm.AddOrUpdateConversation(subscriber, conversation)
	}

}

func (cm *ConversationManager) AddOrUpdateConversation(uid string, conversation *wkstore.Conversation) {
	channelKey := cm.getChannelKey(conversation.ChannelID, conversation.ChannelType)
	conversationCache := cm.getUserConversationCache(uid)
	cm.channelLock.Lock(channelKey)
	conversationCache.Add(channelKey, conversation)
	cm.channelLock.Unlock(channelKey)
	cm.setNeedSave(uid)
}

// GetConversations GetConversations
func (cm *ConversationManager) GetConversations(uid string, version int64, larges []*wkproto.Channel) []*wkstore.Conversation {

	newConversations := make([]*wkstore.Conversation, 0)

	oldConversations, err := cm.getUserAllConversationMapFromStore(uid)
	if err != nil {
		cm.Warn("Failed to get the conversation from the database", zap.Error(err))
		return nil
	}
	if len(oldConversations) > 0 {
		newConversations = append(newConversations, oldConversations...)
	}

	updateConversations := cm.getUserCacheConversations(uid)

	for _, updateConversation := range updateConversations {
		existIndex := 0
		var existConversation *wkstore.Conversation
		for idx, conversation := range oldConversations {
			if conversation.ChannelID == updateConversation.ChannelID && conversation.ChannelType == updateConversation.ChannelType {
				existConversation = updateConversation
				existIndex = idx
				break
			}
		}
		if existConversation == nil {
			newConversations = append(newConversations, updateConversation)
		} else {
			newConversations[existIndex] = existConversation
		}
	}
	conversationSlice := conversationSlice{}
	for _, conversation := range newConversations {
		if conversation != nil {
			if version <= 0 || conversation.Version > version || cm.channelInLarges(conversation.ChannelID, conversation.ChannelType, larges) {
				conversationSlice = append(conversationSlice, conversation)
			}
		}
	}
	sort.Sort(conversationSlice)
	return conversationSlice
}

func (cm *ConversationManager) channelInLarges(channelID string, channelType uint8, larges []*wkproto.Channel) bool {
	if len(larges) == 0 {
		return false
	}
	for _, large := range larges {
		if large.ChannelID == channelID && large.ChannelType == channelType {
			return true
		}
	}
	return false
}

func (cm *ConversationManager) setNeedSave(uid string) {
	cm.needSaveChan <- uid
}

func (cm *ConversationManager) getChannelKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%s-%d", channelID, channelType)
}

type conversationSlice []*wkstore.Conversation

func (s conversationSlice) Len() int { return len(s) }

func (s conversationSlice) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s conversationSlice) Less(i, j int) bool {
	return s[i].Timestamp > s[j].Timestamp
}
