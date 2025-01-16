package manager

import (
	"fmt"
	"hash/fnv"
	"os"
	"path"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/zap"
)

type ConversationManager struct {
	stopper *syncutil.Stopper
	wklog.Log

	workers []*conversationWorker

	deadlock.RWMutex
}

func NewConversationManager() *ConversationManager {

	cm := &ConversationManager{
		Log:     wklog.NewWKLog("ConversationManager"),
		stopper: syncutil.NewStopper(),
	}

	return cm
}

func (c *ConversationManager) Push(fakeChannelId string, channelType uint8, tagKey string, events []*eventbus.Event) {

	worker := c.worker(fakeChannelId, channelType)
	// worker.push(req) // 这个有延迟，导致最近会话获取不到
	worker.handleReq(fakeChannelId, channelType, tagKey, events)

}

func (c *ConversationManager) Start() error {

	c.workers = make([]*conversationWorker, options.G.Conversation.WorkerCount)
	for i := 0; i < options.G.Conversation.WorkerCount; i++ {
		cw := newConversationWorker(i)
		c.workers[i] = cw
		err := cw.start()
		if err != nil {
			c.Error("start conversation worker err", zap.Error(err))
			return err
		}
	}

	c.recoverFromFile()

	return nil
}

func (c *ConversationManager) Stop() {

	for _, w := range c.workers {
		w.stop()
	}

	c.saveToFile()
}

// ForcePropose 强制提交最近会话
func (c *ConversationManager) ForcePropose() {
	for _, w := range c.workers {
		w.propose()
	}
}

func (c *ConversationManager) saveToFile() {
	c.Lock()
	defer c.Unlock()

	conversationDir := path.Join(options.G.DataDir, "conversation")
	err := os.MkdirAll(conversationDir, 0755)
	if err != nil {
		c.Error("mkdir conversation dir err", zap.Error(err))
		return
	}

	allUpdates := make([]*conversationUpdate, 0)
	for _, w := range c.workers {
		allUpdates = append(allUpdates, w.updates...)
	}
	if len(allUpdates) == 0 {
		return
	}

	err = os.WriteFile(path.Join(conversationDir, "conversation.json"), []byte(wkutil.ToJSON(allUpdates)), 0644)
	if err != nil {
		c.Error("write conversation file err", zap.Error(err))
	}
}

func (c *ConversationManager) recoverFromFile() {

	conversationPath := path.Join(options.G.DataDir, "conversation", "conversation.json")

	if !wkutil.FileExists(conversationPath) {
		return
	}

	data, err := wkutil.ReadFile(conversationPath)
	if err != nil {
		c.Panic("read conversation file err", zap.Error(err))
		return
	}

	if len(data) == 0 {
		return
	}

	var allUpdates []*conversationUpdate
	err = wkutil.ReadJSONByByte(data, &allUpdates)
	if err != nil {
		c.Panic("read conversation file err", zap.Error(err))
		return
	}

	for _, update := range allUpdates {
		cc := c.worker(update.channelId, update.channelType)
		cc.updates = append(cc.updates, update)
	}

	err = wkutil.RemoveFile(conversationPath)
	if err != nil {
		c.Error("remove conversation file err", zap.Error(err))
	}

}

func (c *ConversationManager) worker(channelId string, channelType uint8) *conversationWorker {
	c.Lock()
	defer c.Unlock()
	h := fnv.New32a()
	h.Write([]byte(wkutil.ChannelToKey(channelId, channelType)))

	i := h.Sum32() % uint32(len(c.workers))
	return c.workers[i]
}

func (c *ConversationManager) getConversationUpdatesWithUid(uid string, conversationType wkdb.ConversationType) []*conversationUpdate {
	c.RLock()
	defer c.RUnlock()

	conversationUpdates := make([]*conversationUpdate, 0)

	for _, worker := range c.workers {
		for _, update := range worker.updates {
			if update.conversationType != conversationType {
				continue
			}
			if update.exist(uid) {
				conversationUpdates = append(conversationUpdates, update)
			}
		}
	}
	return conversationUpdates
}

func (c *ConversationManager) GetFromCache(uid string, conversationType wkdb.ConversationType) []wkdb.Conversation {

	updates := c.getConversationUpdatesWithUid(uid, conversationType)
	conversations := make([]wkdb.Conversation, 0, len(updates))

	for _, update := range updates {
		conversations = append(conversations, wkdb.Conversation{
			Uid:          uid,
			Type:         conversationType,
			ChannelId:    update.channelId,
			ChannelType:  update.channelType,
			ReadToMsgSeq: update.getUserMessageSeq(uid),
		})
	}

	return conversations

}

func (c *ConversationManager) DeleteFromCache(uid string, channelId string, channelType uint8) {
	worker := c.worker(channelId, channelType)

	worker.Lock()
	defer worker.Unlock()
	update := worker.getConversationUpdate(channelId, channelType)
	if update != nil {
		update.deleteUser(uid)
	}
}

func (c *ConversationManager) CacheCount() int {
	c.RLock()
	defer c.RUnlock()

	count := 0
	for _, w := range c.workers {
		count += len(w.updates)
	}
	return count
}

// func (c *ConversationManager) existConversationInCache(uid string, channelId string, channelType uint8) bool {
// 	userconversation := c.worker(uid).getUserConversation(uid)
// 	if userconversation == nil {
// 		return false
// 	}
// 	return userconversation.existConversation(channelId, channelType)

// }

type conversationWorker struct {
	wklog.Log
	index   int
	stopper *syncutil.Stopper

	sync.RWMutex

	updates []*conversationUpdate // 需要更新的集合

}

func newConversationWorker(i int) *conversationWorker {
	return &conversationWorker{
		Log:     wklog.NewWKLog(fmt.Sprintf("conversationWorker[%d]", i)),
		index:   i,
		stopper: syncutil.NewStopper(),
	}
}

func (c *conversationWorker) start() error {
	// c.stopper.RunWorker(c.loop)
	c.stopper.RunWorker(c.loopPropose)
	return nil
}

func (c *conversationWorker) stop() {
	c.stopper.Stop()
}

// func (c *conversationWorker) push(req *conversationReq) {
// 	select {
// 	case c.reqCh <- req:
// 	default:
// 		c.Error("conversationWorker push req failed, reqCh is full", zap.String("flag", "chanFull"))
// 	}
// }

// func (c *conversationWorker) loop() {
// 	for {
// 		select {
// 		case req := <-c.reqCh:
// 			c.handleReq(req)
// 		case <-c.stopper.ShouldStop():
// 			return
// 		}
// 	}
// }

func (c *conversationWorker) handleReq(fakeChannelId string, channelType uint8, tagKey string, events []*eventbus.Event) {

	c.Lock()
	defer c.Unlock()

	if len(events) == 0 { // 没有消息不更新最近会话
		return
	}

	// 过滤掉不需要存储的消息
	messages := make([]*eventbus.Event, 0, len(events))

	for _, event := range events {
		if event.MessageSeq > 0 {
			messages = append(messages, event)
		}
	}
	if len(messages) == 0 {
		return
	}
	firstMsg := messages[0]
	lastMsg := messages[len(messages)-1]

	// 获取频道的最近会话更新对象
	update := c.getConversationUpdate(fakeChannelId, channelType)
	if update == nil {
		update = newConversationUpdate(fakeChannelId, channelType, "", uint64(firstMsg.MessageSeq))
		c.updates = append(c.updates, update)
	}
	update.keepActive()

	// 消息发送者的最近会话更新
	for _, msg := range messages {
		fromUid := msg.Conn.Uid
		if msg.Conn.Uid == options.G.SystemUID { // 忽略系统账号
			continue
		}
		leaderId, err := service.Cluster.SlotLeaderIdOfChannel(fromUid, wkproto.ChannelTypePerson)
		if err != nil {
			c.Error("handleReq failed, SlotLeaderIdOfChannel is err", zap.Error(err), zap.String("uid", fromUid))
			continue
		}
		if leaderId != options.G.Cluster.NodeId { // 如果发送者不在本节点则不需要更新最近会话
			continue
		}
		update.addOrUpdateUser(fromUid, uint64(msg.MessageSeq))
	}

	if channelType == wkproto.ChannelTypePerson {
		// 如果是个人频道并且不是第一条消息，则不需要更新最近会话
		if firstMsg.MessageSeq > 1 {
			if options.G.IsCmdChannel(fakeChannelId) {
				err := c.updateConversationPerson(fakeChannelId, update, lastMsg.MessageSeq-1)
				if err != nil {
					c.Error("updateConversationPerson err", zap.Error(err))
					return
				}
			}
			return
		} else {
			// 如果是第一条消息，则需要更新最近会话
			err := c.updateConversationPerson(fakeChannelId, update, 0)
			if err != nil {
				c.Error("updateConversationPerson err", zap.Error(err))
				return
			}

		}
		return
	}

	// 命令频道每次需要更新最近会话
	if options.G.IsCmdChannel(fakeChannelId) {
		update.suggestMessageSeq = lastMsg.MessageSeq
		update.updateLastTagKey(tagKey)
		update.shouldUpdateAll() // 整个频道的订阅者都更新最近会话
		return
	}

	// 如果tag不一样了说明订阅者发生了变化，需要更新频道的最近会话
	if update.lastTagKey != tagKey {
		update.updateLastTagKey(tagKey)
		update.shouldUpdateAll()
	}

}

func (c *conversationWorker) loopPropose() {
	tk := time.NewTicker(options.G.Conversation.SyncInterval)
	defer tk.Stop()

	clean := time.NewTicker(options.G.Conversation.SyncInterval * 2)

	for {
		select {
		case <-tk.C:
			c.propose()
		case <-clean.C:
			c.cleanUpdate() // 清理更新缓存
		case <-c.stopper.ShouldStop():
			return
		}
	}
}

func (c *conversationWorker) propose() {

	c.Lock()
	conversations := make([]wkdb.Conversation, 0)

	var deleteUpdates []*conversationUpdate
	for _, update := range c.updates {
		conversationsWithUpdater, err := c.getConversationWithUpdater(update)
		if err != nil {
			c.Error("getConversationWithUpdater err", zap.Error(err))
			continue
		}

		// 如果为0，则删除update
		if len(conversationsWithUpdater) == 0 {
			deleteUpdates = append(deleteUpdates, update)
		}
		conversations = append(conversations, conversationsWithUpdater...)
	}
	if len(deleteUpdates) > 0 {
		for _, update := range deleteUpdates {
			for i, u := range c.updates {
				if u == update {
					c.updates = append(c.updates[:i], c.updates[i+1:]...)
					break
				}
			}
		}
	}
	c.Unlock()

	if len(conversations) == 0 {
		return
	}

	c.Info("conversations update", zap.Int("count", len(conversations)))

	// 如果conversations的数据超过500则分批提交
	if len(conversations) > 500 {
		for i := 0; i < len(conversations); i += 500 {
			end := i + 500
			if end > len(conversations) {
				end = len(conversations)
			}
			err := service.Store.AddOrUpdateConversations(conversations[i:end])
			if err != nil {
				c.Error("propose: AddOrUpdateConversations err", zap.Error(err))
				return
			}
		}
	} else {
		err := service.Store.AddOrUpdateConversations(conversations)
		if err != nil {
			c.Error("propose: AddOrUpdateConversations err", zap.Error(err))
			return
		}
	}

	c.Lock()

	for _, conversation := range conversations {
		for _, update := range c.updates {
			if update.channelId == conversation.ChannelId && update.channelType == conversation.ChannelType {
				update.removeUserIfSeqLE(conversation.Uid, conversation.ReadToMsgSeq)
			}
			update.shouldNotUpdateAll()
		}
	}

	c.Unlock()

}

func (c *conversationWorker) cleanUpdate() {
	c.Lock()
	defer c.Unlock()
	// 如果updateAll为false并且users为空的时候就可以移除update了
	for i := 0; i < len(c.updates); {

		udpate := c.updates[i]
		if !udpate.isUpdateAll() && len(udpate.users) == 0 && time.Since(udpate.activeTime) > options.G.Conversation.CacheExpire {
			c.updates = append(c.updates[:i], c.updates[i+1:]...)
		} else {
			i++
		}
	}
}

func (c *conversationWorker) getConversationWithUpdater(update *conversationUpdate) ([]wkdb.Conversation, error) {
	createdAt := time.Now()
	updatedAt := time.Now()
	conversations := make([]wkdb.Conversation, 0)

	// 指定要更新的最近会话
	for _, user := range update.users {
		id := service.Store.NextPrimaryKey()
		conversations = append(conversations, wkdb.Conversation{
			Id:           id,
			Uid:          user.uid,
			ChannelId:    update.channelId,
			ChannelType:  update.channelType,
			Type:         update.conversationType,
			ReadToMsgSeq: user.messageSeq,
			CreatedAt:    &createdAt,
			UpdatedAt:    &updatedAt,
		})
	}

	var willUpdateUids []string // 将要更新最近会话的用户集合
	if update.isUpdateAll() {
		tag := service.TagManager.Get(update.lastTagKey)
		if tag == nil {
			c.Warn("getConversationWithUpdater: getReceiverTag is nil", zap.String("tagKey", update.lastTagKey))
		} else {
			nodeUsers := tag.GetNodeUsers(options.G.Cluster.NodeId)
			if len(nodeUsers) > 0 {
				willUpdateUids = nodeUsers
			}
		}
	}

	var needUpdateUids []string
	// 判断实际只需要更新的用户
	if len(willUpdateUids) > 0 {
		// 从数据库获取当前频道的在本节点的所有用户的最近会话uid
		updatedUids, err := service.Store.GetChannelConversationLocalUsers(update.channelId, update.channelType)
		if err != nil {
			return nil, err
		}

		// 比较willUpdateUids和updatedUids获得updatedUids里不存在的uid集合
		if len(updatedUids) > 0 {
			for _, uid := range willUpdateUids {
				exist := false
				for _, updatedUid := range updatedUids {
					if uid == updatedUid {
						exist = true
						break
					}
				}
				if !exist {
					needUpdateUids = append(needUpdateUids, uid)
				}
			}
		} else {
			needUpdateUids = willUpdateUids
		}

	}

	sugguestSeq := update.suggestMessageSeq
	if update.suggestMessageSeq > 0 {
		sugguestSeq = sugguestSeq - 1
	}

	for _, uid := range needUpdateUids {
		id := service.Store.NextPrimaryKey()

		//  如果update.users 里面已经存在了uid则不需要再次更新
		exist := false
		for _, user := range update.users {
			if user.uid == uid {
				exist = true
				break
			}
		}
		if exist {
			continue
		}
		conversations = append(conversations, wkdb.Conversation{
			Id:           id,
			Uid:          uid,
			Type:         update.conversationType,
			ChannelId:    update.channelId,
			ChannelType:  update.channelType,
			ReadToMsgSeq: sugguestSeq,
			CreatedAt:    &createdAt,
			UpdatedAt:    &updatedAt,
		})
	}
	return conversations, nil
}

// 更新个人频道的最近会话
func (c *conversationWorker) updateConversationPerson(fakeChannelId string, update *conversationUpdate, seq uint64) error {
	orgFakeChannelId := fakeChannelId
	if options.G.IsCmdChannel(fakeChannelId) {
		orgFakeChannelId = options.G.CmdChannelConvertOrginalChannel(fakeChannelId)
	}
	u1, u2 := options.GetFromUIDAndToUIDWith(orgFakeChannelId)

	u1LeaderInfo, err := service.Cluster.SlotLeaderOfChannel(u1, wkproto.ChannelTypePerson)
	if err != nil {
		c.Error("updateConversationPerson failed, SlotLeaderOfChannel is err", zap.Error(err), zap.String("uid", u1))
		return err
	}
	u2LeaderInfo, err := service.Cluster.SlotLeaderOfChannel(u2, wkproto.ChannelTypePerson)
	if err != nil {
		c.Error("updateConversationPerson failed, SlotLeaderOfChannel is err", zap.Error(err), zap.String("uid", u2))
		return err
	}

	if u1LeaderInfo.Id == options.G.Cluster.NodeId {
		update.addOrUpdateUser(u1, seq)
	}
	if u2LeaderInfo.Id == options.G.Cluster.NodeId {
		update.addOrUpdateUser(u2, seq)
	}

	return nil

}

func (c *conversationWorker) getConversationUpdate(channelId string, channelType uint8) *conversationUpdate {
	for _, u := range c.updates {
		if u.channelId == channelId && u.channelType == channelType {
			return u
		}
	}
	return nil
}

type conversationUpdate struct {
	channelId        string                // 需要更新最近会话的频道Id
	channelType      uint8                 // 需要更新最近会话的频道类型
	conversationType wkdb.ConversationType // 需要更新最近会话的频道类型
	users            []userUpdate          // 指定需要更新最近会话的用户集合
	deleted          map[string]struct{}   // 已删除最近会话的用户
	lastTagKey       string                // 最后一次更新所有最近会话的tagKey
	updateAll        bool                  // 是否需要更新整个频道的订阅者的最近会话
	sync.RWMutex
	suggestMessageSeq uint64 // 更新所有的时候建议使用的messageSeq

	activeTime time.Time // 最后一次更新时间
}

type userUpdate struct {
	messageSeq uint64
	uid        string
}

func newConversationUpdate(channelId string, channelType uint8, lastTagKey string, suggestMessageSeq uint64) *conversationUpdate {

	conversationType := wkdb.ConversationTypeChat
	if options.G.IsCmdChannel(channelId) {
		conversationType = wkdb.ConversationTypeCMD
	}

	return &conversationUpdate{
		deleted:           make(map[string]struct{}),
		channelId:         channelId,
		channelType:       channelType,
		conversationType:  conversationType,
		lastTagKey:        lastTagKey,
		suggestMessageSeq: suggestMessageSeq,
	}
}

func (c *conversationUpdate) addOrUpdateUser(uid string, messageSeq uint64) {

	c.Lock()
	defer c.Unlock()

	delete(c.deleted, uid) // 移除删除最近会话的标记，因为有新最近会话了

	for i, u := range c.users {
		if u.uid == uid {
			if messageSeq > u.messageSeq {
				c.users[i].messageSeq = messageSeq
			}
			return
		}
	}
	c.users = append(c.users, userUpdate{uid: uid, messageSeq: messageSeq})
}

func (c *conversationUpdate) deleteUser(uid string) {
	c.Lock()
	defer c.Unlock()

	for i, u := range c.users {
		if u.uid == uid {
			c.users = append(c.users[:i], c.users[i+1:]...)
			break
		}
	}
	c.deleted[uid] = struct{}{}

}

// 移除小于等于messageSeq的指定用户
func (c *conversationUpdate) removeUserIfSeqLE(uid string, messageSeq uint64) {

	c.Lock()
	defer c.Unlock()
	for i, u := range c.users {
		if u.uid == uid && u.messageSeq <= messageSeq {

			c.users = append(c.users[:i], c.users[i+1:]...)
			break
		}
	}
}

func (c *conversationUpdate) exist(uid string) bool {
	c.RLock()
	defer c.RUnlock()

	_, ok := c.deleted[uid]
	if ok {
		return false
	}

	for _, u := range c.users {
		if u.uid == uid {
			return true
		}
	}

	if c.updateAll {
		if c.channelType != wkproto.ChannelTypePerson && c.lastTagKey != "" {
			tag := service.TagManager.Get(c.lastTagKey)
			if tag != nil && tag.ExistUserInNode(uid, options.G.Cluster.NodeId) {
				return true
			}
		}
	}
	return false
}

func (c *conversationUpdate) getUserMessageSeq(uid string) uint64 {
	c.RLock()
	defer c.RUnlock()

	for _, u := range c.users {
		if u.uid == uid {
			return u.messageSeq
		}
	}
	return 0
}
func (c *conversationUpdate) shouldUpdateAll() {
	c.Lock()
	defer c.Unlock()

	c.updateAll = true
}

func (c *conversationUpdate) shouldNotUpdateAll() {
	c.Lock()
	defer c.Unlock()

	c.updateAll = false
}

func (c *conversationUpdate) isUpdateAll() bool {
	c.RLock()
	defer c.RUnlock()

	return c.updateAll
}

func (c *conversationUpdate) updateLastTagKey(tagKey string) {
	c.Lock()
	defer c.Unlock()

	c.lastTagKey = tagKey
}

func (c *conversationUpdate) keepActive() {
	c.Lock()
	defer c.Unlock()

	c.activeTime = time.Now()
}
