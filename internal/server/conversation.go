package server

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type ConversationManager struct {
	cache   *lru.Cache[string, *channelSubscribers]
	stopper *syncutil.Stopper
	stopped atomic.Bool
	s       *Server
	wklog.Log
	savePool *ants.Pool // 保存session的协程池
}

func NewConversationManager(s *Server) *ConversationManager {
	cm := &ConversationManager{
		Log:     wklog.NewWKLog("ConversationManager"),
		stopper: syncutil.NewStopper(),
		s:       s,
	}
	var err error
	cm.cache, err = lru.New[string, *channelSubscribers](100000)
	if err != nil {
		cm.Panic("Failed to create cache", zap.Error(err))

	}
	cm.savePool, err = ants.NewPool(s.opts.Conversation.SavePoolSize)
	if err != nil {
		cm.Panic("Failed to create save pool", zap.Error(err))
	}
	return cm
}

func (c *ConversationManager) Push(fakeChannelId string, channelType uint8, uids []string, fromUid string, messageSeq uint64) {
	if strings.TrimSpace(fakeChannelId) == "" || len(uids) == 0 {
		return
	}
	channelKey := c.getChannelKey(fakeChannelId, channelType)
	channelSubscribers, ok := c.cache.Get(channelKey)
	if !ok {
		channelSubscribers = newChannelSubscribers(len(uids))
		c.cache.Add(channelKey, channelSubscribers)
	}
	for _, subscriber := range uids {
		if fromUid != "" && fromUid == subscriber {
			channelSubscribers.add(subscriber, messageSeq)
		} else {
			channelSubscribers.add(subscriber, 0)
		}

	}
}

func (c *ConversationManager) push(fakeChannelId string, channelType uint8, uids map[string]uint64) {
	if strings.TrimSpace(fakeChannelId) == "" || len(uids) == 0 {
		return
	}
	channelKey := c.getChannelKey(fakeChannelId, channelType)
	channelSubscribers, ok := c.cache.Get(channelKey)
	if !ok {
		channelSubscribers = newChannelSubscribers(len(uids))
		c.cache.Add(channelKey, channelSubscribers)
	}
	for subscriber, seq := range uids {
		channelSubscribers.add(subscriber, seq)
	}
}

func (c *ConversationManager) Start() {
	c.restoreLocal()
	c.stopper.RunWorker(c.loop)
}

func (c *ConversationManager) Stop() {
	c.stopped.Store(true)
	c.stopper.Stop()
	c.saveToLocal()
}

func (c *ConversationManager) loop() {
	tk := time.NewTicker(c.s.opts.Conversation.SyncInterval)
	for !c.stopped.Load() {

		c.proposeSave()
		select {
		case <-tk.C:
		case <-c.stopper.ShouldStop():
			return
		}
	}
}

// 保存到本地
func (c *ConversationManager) saveToLocal() {
	keys := c.cache.Keys()
	saveMap := make(map[string]map[string]uint64)
	for _, key := range keys {
		channelSubscribers, ok := c.cache.Get(key)
		if !ok {
			continue
		}
		saveMap[key] = channelSubscribers.subscriberMap
	}
	if len(saveMap) > 0 {

		// 保存到本地文件
		strs := wkutil.ToJSON(saveMap)

		savePath := path.Join(c.s.opts.DataDir, "localsession.json")
		err := wkutil.WriteFile(savePath, []byte(strs))
		if err != nil {
			c.Error("Failed to save local session", zap.Error(err))
		}
	}
}

// 恢复本地数据
func (c *ConversationManager) restoreLocal() {
	savePath := path.Join(c.s.opts.DataDir, "localsession.json")
	data, err := wkutil.ReadFile(savePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		c.Error("Failed to read local session", zap.Error(err))
		return
	}
	if len(data) == 0 {
		return
	}
	var saveMap map[string]map[string]uint64
	err = wkutil.ReadJSONByByte(data, &saveMap)
	if err != nil {
		c.Error("Failed to parse local session", zap.Error(err))
		return
	}
	for key, subscribers := range saveMap {
		channelSubscribers := newChannelSubscribers(len(subscribers))
		for subscriber, messageSeq := range subscribers {
			channelSubscribers.add(subscriber, messageSeq)
		}
		c.cache.Add(key, channelSubscribers)
	}

	err = os.Remove(savePath)
	if err != nil {
		c.Error("remove file failed", zap.String("savePath", savePath))
	}

}

// 提案保存
func (c *ConversationManager) proposeSave() {
	keys := c.cache.Keys()
	// var err error

	slotSessionMap := make(map[uint32][]*wkdb.BatchUpdateConversationModel) // 按照slotId来分组
	var byteSize uint64
	for _, key := range keys {
		if c.s.opts.Conversation.BytesPerSave > 0 && byteSize > c.s.opts.Conversation.BytesPerSave { // 超过保存的字节数，就退出
			break
		}
		if c.stopped.Load() {
			return
		}

		channelSubscribers, ok := c.cache.Get(key)
		if !ok {
			continue
		}
		subscribers := channelSubscribers.subscribers()
		// save subscribers
		channelId, channelType := c.channelFromKey(key)

		if len(subscribers) == 0 {
			continue
		}

		slotSubscribersMap := c.subscribersSplitBySlotId(subscribers) // 按照slotId来分组subscribers
		for slotId, slotSubscribers := range slotSubscribersMap {
			model := &wkdb.BatchUpdateConversationModel{
				Uids:        slotSubscribers,
				ChannelId:   channelId,
				ChannelType: channelType,
			}
			slotSessionMap[slotId] = append(slotSessionMap[slotId], model)
			byteSize += uint64(model.Size())
		}

		c.cache.Remove(key)
	}
	if len(slotSessionMap) == 0 {
		return
	}

	// var err error
	for slotId, models := range slotSessionMap {
		// running := c.savePool.Running()
		// if running > c.s.opts.Conversation.SavePoolSize+10 {
		// 	c.Warn("The save pool is busy", zap.Int("running", running), zap.Int("poolSize", c.s.opts.Conversation.SavePoolSize))
		// }

		err := c.s.store.BatchUpdateConversation(slotId, models)
		if err != nil {
			c.Error("Failed to update session", zap.Error(err), zap.Uint32("slotId", slotId), zap.Int("models", len(models)))
			// 如果失败 则重新加入队列里
			for _, model := range models {
				c.push(model.ChannelId, model.ChannelType, model.Uids)
			}
		}
		// err = c.savePool.Submit(func(stid uint32, ms []*wkdb.BatchUpdateConversationModel) func() {
		// 	return func() {
		// 		err := c.s.store.BatchUpdateConversation(stid, ms)
		// 		if err != nil {
		// 			c.Error("Failed to update session", zap.Error(err), zap.Uint32("slotId", stid), zap.Int("models", len(ms)))
		// 			// 如果失败 则重新加入队列里
		// 			for _, model := range ms {
		// 				c.push(model.ChannelId, model.ChannelType, model.Uids)
		// 			}
		// 		}
		// 	}
		// }(slotId, models))
		// if err != nil {
		// 	c.Error("Failed to submit save pool", zap.Error(err))
		// }
	}

}

// 从换成中获取用户的会话channel
func (c *ConversationManager) GetUserConversationFromCache(uid string, conversationType wkdb.ConversationType) []wkdb.Conversation {
	keys := c.cache.Keys()

	var conversations []wkdb.Conversation
	for _, key := range keys {
		channelId, channelType := c.channelFromKey(key)
		if c.s.opts.IsCmdChannel(channelId) && conversationType == wkdb.ConversationTypeChat {
			continue
		}
		subscribers, _ := c.cache.Get(key)
		if subscribers != nil {
			seq, exist := subscribers.exist(uid)
			if exist {
				conversation := wkdb.Conversation{
					Uid:            uid,
					ChannelId:      channelId,
					ChannelType:    channelType,
					ReadedToMsgSeq: seq,
					Type:           conversationType,
				}
				conversations = append(conversations, conversation)
			}
		}
	}
	return conversations
}

func (c *ConversationManager) DeleteUserConversationFromCache(uid string, channelId string, channelType uint8) {
	channelKey := c.getChannelKey(channelId, channelType)
	subs, ok := c.cache.Get(channelKey)
	if !ok {
		return
	}
	subs.remove(uid)
}

// 按slotId来分组subscribers

func (c *ConversationManager) subscribersSplitBySlotId(subscribers map[string]uint64) map[uint32]map[string]uint64 {
	subscribersMap := make(map[uint32]map[string]uint64)
	for subscriber, seq := range subscribers {
		slotId := c.s.getSlotId(subscriber)
		subscribersMap[slotId] = map[string]uint64{
			subscriber: seq,
		}
	}
	return subscribersMap
}

type channelSubscribers struct {
	subscriberMap map[string]uint64 // 订阅者和已读消息messageSeq
	mu            sync.RWMutex
}

func newChannelSubscribers(cap int) *channelSubscribers {
	return &channelSubscribers{
		subscriberMap: make(map[string]uint64, cap),
	}
}

func (c *channelSubscribers) add(subscriber string, messageSeq uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	oldSeq, ok := c.subscriberMap[subscriber]
	if ok && oldSeq > messageSeq {
		return
	}
	c.subscriberMap[subscriber] = messageSeq

}

func (c *channelSubscribers) remove(subscriber string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subscriberMap, subscriber)
}

func (c *channelSubscribers) exist(subscriber string) (uint64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seq, ok := c.subscriberMap[subscriber]
	return seq, ok
}

func (c *channelSubscribers) subscribers() map[string]uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.subscriberMap
}

func (cm *ConversationManager) getChannelKey(channelId string, channelType uint8) string {
	return fmt.Sprintf("%s-%d", channelId, channelType)
}

func (c *ConversationManager) channelFromKey(key string) (string, uint8) {
	strs := strings.Split(key, "-")
	if len(strs) != 2 {
		return "", 0
	}
	channelTypeI, _ := strconv.Atoi(strs[1])
	return strs[0], uint8(channelTypeI)
}

// // ConversationManager ConversationManager
// type ConversationManager struct {
// 	channelLock *keylock.KeyLock
// 	s           *Server
// 	wklog.Log
// 	queue                          *Queue
// 	userConversationMapBuckets     []map[string]*lru.Cache[string, wkdb.Conversation]
// 	userConversationMapBucketLocks []sync.RWMutex
// 	bucketNum                      int
// 	needSaveConversationMap        map[string]bool
// 	needSaveConversationMapLock    sync.RWMutex
// 	stopChan                       chan struct{} //停止信号
// 	calcChan                       chan interface{}
// 	needSaveChan                   chan string
// 	crontab                        *cron.Cron
// }

// // NewConversationManager NewConversationManager
// func NewConversationManager(s *Server) *ConversationManager {
// 	cm := &ConversationManager{
// 		s:                       s,
// 		bucketNum:               10,
// 		Log:                     wklog.NewWKLog("ConversationManager"),
// 		channelLock:             keylock.NewKeyLock(),
// 		needSaveConversationMap: map[string]bool{},
// 		stopChan:                make(chan struct{}),
// 		calcChan:                make(chan interface{}),
// 		needSaveChan:            make(chan string),
// 		queue:                   NewQueue(),
// 	}
// 	cm.userConversationMapBuckets = make([]map[string]*lru.Cache[string, wkdb.Conversation], cm.bucketNum)
// 	cm.userConversationMapBucketLocks = make([]sync.RWMutex, cm.bucketNum)

// 	s.Schedule(time.Minute, func() {
// 		totalConversation := 0
// 		for i := 0; i < cm.bucketNum; i++ {
// 			cm.userConversationMapBucketLocks[i].Lock()
// 			userConversationMap := cm.userConversationMapBuckets[i]
// 			for _, cache := range userConversationMap {
// 				totalConversation += cache.Len()
// 			}
// 			cm.userConversationMapBucketLocks[i].Unlock()
// 		}
// 		s.monitor.ConversationCacheSet(totalConversation)
// 	})

// 	cm.crontab = cron.New(cron.WithSeconds())

// 	_, err := cm.crontab.AddFunc("0 0 2 * * ?", cm.clearExpireConversations) // 每条凌晨2点执行一次
// 	if err != nil {
// 		cm.Panic("Failed to add cron job", zap.Error(err))
// 	}

// 	return cm
// }

// // Start Start
// func (cm *ConversationManager) Start() {
// 	if cm.s.opts.Conversation.On {
// 		cm.channelLock.StartCleanLoop()
// 		go cm.saveloop()
// 		go cm.calcLoop()
// 		cm.crontab.Start()
// 	}

// }

// // Stop Stop
// func (cm *ConversationManager) Stop() {
// 	cm.Debug("stop...")
// 	if cm.s.opts.Conversation.On {
// 		close(cm.stopChan)
// 		cm.channelLock.StopCleanLoop()
// 		// Wait for the queue to complete
// 		cm.queue.Wait()

// 		cm.FlushConversations()

// 		cm.crontab.Stop()
// 	}
// }

// // 清空过期最近会话
// func (cm *ConversationManager) clearExpireConversations() {
// 	for idx := range cm.userConversationMapBucketLocks {
// 		cm.userConversationMapBucketLocks[idx].Lock()
// 		userConversationMap := cm.userConversationMapBuckets[idx]
// 		for uid, cache := range userConversationMap {
// 			keys := cache.Keys()
// 			for _, key := range keys {
// 				conversation, _ := cache.Get(key)
// 				if !wkdb.IsEmptyConversation(conversation) {
// 					if conversation.Timestamp+int64(cm.s.opts.Conversation.CacheExpire.Seconds()) < time.Now().Unix() {
// 						cache.Remove(key)
// 					}
// 				}
// 			}
// 			if cache.Len() == 0 {
// 				delete(userConversationMap, uid)
// 			}
// 		}
// 		cm.userConversationMapBucketLocks[idx].Unlock()
// 	}
// }

// // 保存最近会话
// func (cm *ConversationManager) calcLoop() {
// 	for {
// 		messageMapObj := cm.queue.Pop()
// 		if messageMapObj == nil {
// 			continue
// 		}
// 		messageMap := messageMapObj.(map[string]interface{})
// 		message := messageMap["message"].(*Message)
// 		subscribers := messageMap["subscribers"].([]string)

// 		for _, subscriber := range subscribers {
// 			cm.calConversation(message, subscriber)
// 		}
// 	}
// }

// func (cm *ConversationManager) saveloop() {
// 	ticker := time.NewTicker(cm.s.opts.Conversation.SyncInterval)

// 	needSync := false
// 	noSaveCount := 0
// 	for {
// 		if noSaveCount >= cm.s.opts.Conversation.SyncOnce {
// 			needSync = true
// 		}
// 		if needSync {
// 			noSaveCount = 0
// 			cm.FlushConversations()
// 			needSync = false
// 		}
// 		select {
// 		case uid := <-cm.needSaveChan:
// 			cm.needSaveConversationMapLock.Lock()
// 			if !cm.needSaveConversationMap[uid] {
// 				cm.needSaveConversationMap[uid] = true
// 				noSaveCount++
// 			}
// 			cm.needSaveConversationMapLock.Unlock()

// 		case <-ticker.C:
// 			if noSaveCount > 0 {
// 				needSync = true
// 			}
// 		case <-cm.stopChan:
// 			return
// 		}
// 	}
// }

// // PushMessage PushMessage
// func (cm *ConversationManager) PushMessage(message *Message, subscribers []string) {
// 	if !cm.s.opts.Conversation.On {
// 		return
// 	}

// 	cm.queue.Push(map[string]interface{}{
// 		"message":     message,
// 		"subscribers": subscribers,
// 	})
// }

// // SetConversationUnread set unread data from conversation
// func (cm *ConversationManager) SetConversationUnread(uid string, channelID string, channelType uint8, unread int, messageSeq uint64) error {
// 	conversationCache := cm.getUserConversationCache(uid)
// 	for _, key := range conversationCache.Keys() {
// 		conversation, _ := conversationCache.Get(key)
// 		if channelID == conversation.ChannelId && channelType == conversation.ChannelType {
// 			conversation.UnreadCount = uint32(unread)
// 			if messageSeq > 0 {
// 				conversation.LastMsgSeq = messageSeq
// 			}
// 			conversationCache.Add(cm.getChannelKey(conversation.ChannelId, conversation.ChannelType), conversation)
// 			cm.setNeedSave(uid)
// 			return nil
// 		}
// 	}
// 	conversation, err := cm.s.store.GetConversation(uid, channelID, channelType)
// 	if err != nil {
// 		return err
// 	}
// 	if !wkdb.IsEmptyConversation(conversation) {
// 		conversation.UnreadCount = uint32(unread)
// 		if messageSeq > 0 {
// 			conversation.LastMsgSeq = messageSeq
// 		}
// 		conversationCache.Add(cm.getChannelKey(conversation.ChannelId, conversation.ChannelType), conversation)
// 		cm.setNeedSave(uid)
// 	}
// 	return nil
// }

// func (cm *ConversationManager) GetConversation(uid string, channelID string, channelType uint8) wkdb.Conversation {

// 	conversations := cm.getUserCacheConversations(uid)
// 	if len(conversations) > 0 {
// 		for _, conversation := range conversations {
// 			if conversation.ChannelId == channelID && conversation.ChannelType == channelType {
// 				return conversation
// 			}
// 		}
// 	}

// 	conversation, err := cm.s.store.GetConversation(uid, channelID, channelType)
// 	if err != nil {
// 		cm.Error("查询最近会话失败！", zap.Error(err), zap.String("uid", uid), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
// 	}

// 	return conversation

// }

// // DeleteConversation 删除最近会话
// func (cm *ConversationManager) DeleteConversation(uids []string, channelID string, channelType uint8) error {
// 	if len(uids) == 0 {
// 		return nil
// 	}
// 	for _, uid := range uids {
// 		conversationCache := cm.getUserConversationCache(uid)
// 		keys := conversationCache.Keys()
// 		for _, key := range keys {
// 			channelKey := cm.getChannelKey(channelID, channelType)
// 			if channelKey == key {
// 				conversationCache.Remove(key)
// 				break
// 			}
// 		}
// 		err := cm.s.store.DeleteConversation(uid, channelID, channelType)
// 		if err != nil {
// 			cm.Error("从数据库删除最近会话失败！", zap.Error(err), zap.String("uid", uid), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
// 		}
// 	}
// 	return nil
// }

// func (cm *ConversationManager) getUserAllConversationMapFromStore(uid string) ([]wkdb.Conversation, error) {
// 	conversations, err := cm.s.store.GetConversations(uid)
// 	if err != nil {
// 		cm.Error("Failed to get the list of recent conversations", zap.String("uid", uid), zap.Error(err))
// 		return nil, err
// 	}
// 	return conversations, nil
// }

// func (cm *ConversationManager) newLRUCache() *lru.Cache[string, wkdb.Conversation] {
// 	c, _ := lru.New[string, wkdb.Conversation](cm.s.opts.Conversation.UserMaxCount)
// 	return c
// }

// // FlushConversations 同步最近会话
// func (cm *ConversationManager) FlushConversations() {

// 	cm.needSaveConversationMapLock.RLock()
// 	needSaveUIDs := make([]string, 0, len(cm.needSaveConversationMap))
// 	for uid := range cm.needSaveConversationMap {
// 		needSaveUIDs = append(needSaveUIDs, uid)
// 	}
// 	cm.needSaveConversationMapLock.RUnlock()

// 	if len(needSaveUIDs) > 0 {
// 		cm.Debug("Save conversation", zap.Int("count", len(needSaveUIDs)))
// 		for _, uid := range needSaveUIDs {
// 			cm.flushUserConversations(uid)
// 		}
// 	}

// }

// func (cm *ConversationManager) flushUserConversations(uid string) {

// 	conversationCache := cm.getUserConversationCache(uid)
// 	conversations := make([]wkdb.Conversation, 0, len(conversationCache.Keys()))
// 	for _, key := range conversationCache.Keys() {
// 		conversationObj, ok := conversationCache.Get(key)
// 		if ok {
// 			conversations = append(conversations, conversationObj)
// 		}

// 	}
// 	err := cm.s.store.AddOrUpdateConversations(uid, conversations)
// 	if err != nil {
// 		cm.Warn("Failed to store conversation data", zap.Error(err))
// 	} else {
// 		cm.needSaveConversationMapLock.Lock()
// 		delete(cm.needSaveConversationMap, uid)
// 		cm.needSaveConversationMapLock.Unlock()

// 		// 移除过期的最近会话缓存
// 		for _, conversation := range conversations {
// 			if conversation.Timestamp+int64(cm.s.opts.Conversation.CacheExpire.Seconds()) < time.Now().Unix() {
// 				key := cm.getChannelKey(conversation.ChannelId, conversation.ChannelType)
// 				conversationCache.Remove(key)
// 			}
// 		}
// 	}
// }

// func (cm *ConversationManager) getUserConversationCache(uid string) *lru.Cache[string, wkdb.Conversation] {
// 	pos := int(wkutil.HashCrc32(uid) % uint32(cm.bucketNum))
// 	cm.userConversationMapBucketLocks[pos].RLock()
// 	userConversationMap := cm.userConversationMapBuckets[pos]
// 	if userConversationMap == nil {
// 		userConversationMap = make(map[string]*lru.Cache[string, wkdb.Conversation])
// 		cm.userConversationMapBuckets[pos] = userConversationMap
// 	}

// 	cm.userConversationMapBucketLocks[pos].RUnlock()
// 	cm.channelLock.Lock(uid)
// 	cache := userConversationMap[uid]
// 	if cache == nil {
// 		cache = cm.newLRUCache()
// 		userConversationMap[uid] = cache
// 	}
// 	cm.channelLock.Unlock(uid)

// 	return cache
// }

// func (cm *ConversationManager) getUserCacheConversations(uid string) []wkdb.Conversation {
// 	cache := cm.getUserConversationCache(uid)

// 	conversations := make([]wkdb.Conversation, 0, len(cache.Keys()))
// 	for _, key := range cache.Keys() {
// 		conversationObj, ok := cache.Get(key)
// 		if ok {
// 			conversations = append(conversations, conversationObj)
// 		}
// 	}
// 	return conversations

// }

// func (cm *ConversationManager) calConversation(message *Message, subscriber string) {
// 	conversationCache := cm.getUserConversationCache(subscriber)

// 	// if conversationCache.Len() == 0 {
// 	// 	var err error
// 	// 	conversations, err := cm.getUserAllConversationMapFromStore(subscriber)
// 	// 	if err != nil {
// 	// 		cm.Warn("Failed to get the conversation from the database", zap.Error(err))
// 	// 		return
// 	// 	}
// 	// 	for _, conversation := range conversations {
// 	// 		channelKey := cm.getChannelKey(conversation.ChannelID, conversation.ChannelType)
// 	// 		conversationCache.Add(channelKey, conversation)

// 	// 	}
// 	// }

// 	channelID := message.ChannelID
// 	if message.ChannelType == wkproto.ChannelTypePerson && message.ChannelID == subscriber { // If it is a personal channel and the channel ID is equal to the subscriber, you need to swap fromUID and channelID
// 		channelID = message.FromUID
// 	}
// 	channelKey := cm.getChannelKey(channelID, message.ChannelType)

// 	cm.channelLock.Lock(channelKey)
// 	conversation, _ := conversationCache.Get(channelKey)
// 	cm.channelLock.Unlock(channelKey)

// 	if wkdb.IsEmptyConversation(conversation) {
// 		var err error
// 		conversation, err = cm.s.store.GetConversation(subscriber, channelID, message.ChannelType)
// 		if err != nil {
// 			cm.Error("获取某个最接近会话失败！", zap.String("subscriber", subscriber), zap.String("channelID", channelID), zap.Uint8("channelType", message.ChannelType), zap.Error(err))
// 		}
// 	}

// 	var modify = false
// 	if wkdb.IsEmptyConversation(conversation) {
// 		var unreadCount uint32 = 0
// 		if message.RedDot && message.FromUID != subscriber { //  message.FromUID != subscriber 自己发的消息不显示红点
// 			unreadCount = 1
// 		}
// 		conversation = wkdb.Conversation{
// 			UID:             subscriber,
// 			ChannelId:       channelID,
// 			ChannelType:     message.ChannelType,
// 			UnreadCount:     unreadCount,
// 			Timestamp:       int64(message.Timestamp),
// 			LastMsgSeq:      uint64(message.MessageSeq),
// 			LastClientMsgNo: message.ClientMsgNo,
// 			LastMsgID:       message.MessageID,
// 			Version:         time.Now().UnixNano() / 1e6,
// 		}
// 		modify = true
// 	} else {

// 		if message.RedDot && message.FromUID != subscriber { //  message.FromUID != subscriber 自己发的消息不显示红点
// 			conversation.UnreadCount++
// 			modify = true
// 		}
// 		if conversation.LastMsgSeq < uint64(message.MessageSeq) { // 只有当前会话的messageSeq小于当前消息的messageSeq才更新
// 			conversation.Timestamp = int64(message.Timestamp)
// 			conversation.LastClientMsgNo = message.ClientMsgNo
// 			conversation.LastMsgSeq = uint64(message.MessageSeq)
// 			conversation.LastMsgID = message.MessageID
// 			modify = true
// 		}
// 		if modify {
// 			conversation.Version = time.Now().UnixNano() / 1e6
// 		}
// 	}
// 	if modify {
// 		cm.AddOrUpdateConversation(subscriber, conversation)
// 	}

// }

// func (cm *ConversationManager) AddOrUpdateConversation(uid string, conversation wkdb.Conversation) {
// 	channelKey := cm.getChannelKey(conversation.ChannelId, conversation.ChannelType)
// 	conversationCache := cm.getUserConversationCache(uid)
// 	cm.channelLock.Lock(channelKey)
// 	conversationCache.Add(channelKey, conversation)
// 	cm.channelLock.Unlock(channelKey)
// 	cm.setNeedSave(uid)
// }

// // GetConversations GetConversations
// func (cm *ConversationManager) GetConversations(uid string, version int64, larges []*wkproto.Channel) []wkdb.Conversation {

// 	newConversations := make([]wkdb.Conversation, 0)

// 	oldConversations, err := cm.getUserAllConversationMapFromStore(uid)
// 	if err != nil {
// 		cm.Warn("Failed to get the conversation from the database", zap.Error(err))
// 		return nil
// 	}
// 	if len(oldConversations) > 0 {
// 		newConversations = append(newConversations, oldConversations...)
// 	}

// 	updateConversations := cm.getUserCacheConversations(uid)

// 	for _, updateConversation := range updateConversations {
// 		existIndex := 0
// 		var existConversation wkdb.Conversation
// 		for idx, conversation := range oldConversations {
// 			if conversation.ChannelId == updateConversation.ChannelId && conversation.ChannelType == updateConversation.ChannelType {
// 				existConversation = updateConversation
// 				existIndex = idx
// 				break
// 			}
// 		}
// 		if wkdb.IsEmptyConversation(existConversation) {
// 			newConversations = append(newConversations, updateConversation)
// 		} else {
// 			newConversations[existIndex] = existConversation
// 		}
// 	}
// 	conversationSlice := conversationSlice{}
// 	for _, conversation := range newConversations {
// 		if !wkdb.IsEmptyConversation(conversation) {
// 			if version <= 0 || conversation.Version > version || cm.channelInLarges(conversation.ChannelId, conversation.ChannelType, larges) {
// 				conversationSlice = append(conversationSlice, conversation)
// 			}
// 		}
// 	}
// 	sort.Sort(conversationSlice)
// 	return conversationSlice
// }

// func (cm *ConversationManager) channelInLarges(channelID string, channelType uint8, larges []*wkproto.Channel) bool {
// 	if len(larges) == 0 {
// 		return false
// 	}
// 	for _, large := range larges {
// 		if large.ChannelID == channelID && large.ChannelType == channelType {
// 			return true
// 		}
// 	}
// 	return false
// }

// func (cm *ConversationManager) setNeedSave(uid string) {
// 	cm.needSaveChan <- uid
// }

// type conversationSlice []wkdb.Conversation

// func (s conversationSlice) Len() int { return len(s) }

// func (s conversationSlice) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// func (s conversationSlice) Less(i, j int) bool {
// 	return s[i].Timestamp > s[j].Timestamp
// }

type conversationCacheInfo struct {
	channelId    string
	channelType  uint8
	uid          string
	readedMsgSeq uint64 // 已读至的messageSeq
}
