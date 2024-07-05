package server

import (
	"fmt"
	"hash/fnv"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/zap"
)

type ConversationManager struct {
	stopper *syncutil.Stopper
	wklog.Log
	s *Server

	workers []*conversationWorker

	deadlock.RWMutex
}

func NewConversationManager(s *Server) *ConversationManager {

	cm := &ConversationManager{
		Log:     wklog.NewWKLog("ConversationManager"),
		stopper: syncutil.NewStopper(),
		s:       s,
	}

	return cm
}

func (c *ConversationManager) Push(fakeChannelId string, channelType uint8, uids []string, messages []ReactorChannelMessage) {
	if strings.TrimSpace(fakeChannelId) == "" || len(uids) == 0 || len(messages) == 0 {
		return
	}

	// 处理发送者的最近会话
	for _, message := range messages {
		if message.FromUid == "" {
			continue
		}
		if message.SendPacket.NoPersist {
			continue
		}

		if message.FromUid == c.s.opts.SystemUID {
			continue
		}

		worker := c.worker(message.FromUid)
		worker.getOrCreateUserConversation(message.FromUid).updateOrAddConversation(fakeChannelId, channelType, message.MessageSeq)
	}

	// 处理接受者的最近会话
	for _, uid := range uids {

		if uid == c.s.opts.SystemUID {
			continue
		}

		worker := c.worker(uid)
		userConversation := worker.getOrCreateUserConversation(uid)

		// 如果用户最近会话缓存中不存在，则加入到缓存，如果存在可以直接忽略
		if !userConversation.existConversation(fakeChannelId, channelType) {
			// 如果数据库中存在会话，则仅仅添加到缓存，不需要更新数据库
			existInDb, err := c.s.store.DB().ExistConversation(uid, fakeChannelId, channelType)
			if err != nil {
				c.Error("exist conversation err", zap.Error(err), zap.String("uid", uid), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
				continue
			}
			if existInDb {
				channelConversation := userConversation.addConversationIfNotExist(fakeChannelId, channelType, 0)
				if channelConversation != nil { // 如果db中存在会话，则不需要更新
					channelConversation.NeedUpdate = false
				}
			} else {
				userConversation.addConversationIfNotExist(fakeChannelId, channelType, 0) // 只有缓存中不存在的时候才添加
			}
		}

	}

}

func (c *ConversationManager) Start() error {

	c.workers = make([]*conversationWorker, c.s.opts.Conversation.WorkerCount)
	for i := 0; i < c.s.opts.Conversation.WorkerCount; i++ {
		cw := newConversationWorker(i, c.s)
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

func (c *ConversationManager) saveToFile() {
	c.Lock()
	defer c.Unlock()

	conversationDir := path.Join(c.s.opts.DataDir, "conversation")
	err := os.MkdirAll(conversationDir, 0755)
	if err != nil {
		c.Error("mkdir conversation dir err", zap.Error(err))
		return
	}

	jsonMap := make(map[string][]*channelConversation)
	for _, w := range c.workers {
		for _, cc := range w.userConversations {
			jsonMap[cc.uid] = cc.conversations
		}
	}
	if len(jsonMap) == 0 {
		return
	}

	err = os.WriteFile(path.Join(conversationDir, "conversation.json"), []byte(wkutil.ToJSON(jsonMap)), 0644)
	if err != nil {
		c.Error("write conversation file err", zap.Error(err))
	}
}

func (c *ConversationManager) recoverFromFile() {

	conversationPath := path.Join(c.s.opts.DataDir, "conversation", "conversation.json")

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

	var jsonMap map[string][]*channelConversation
	err = wkutil.ReadJSONByByte(data, &jsonMap)
	if err != nil {
		c.Panic("read conversation file err", zap.Error(err))
		return
	}

	for uid, conversations := range jsonMap {
		cc := c.worker(uid).getOrCreateUserConversation(uid)
		cc.conversations = conversations
	}

	err = wkutil.RemoveFile(conversationPath)
	if err != nil {
		c.Error("remove conversation file err", zap.Error(err))
	}

}

func (c *ConversationManager) worker(uid string) *conversationWorker {
	c.Lock()
	defer c.Unlock()
	h := fnv.New32a()
	h.Write([]byte(uid))

	i := h.Sum32() % uint32(len(c.workers))
	return c.workers[i]
}

func (c *ConversationManager) GetUserConversationFromCache(uid string, conversationType wkdb.ConversationType) []wkdb.Conversation {

	worker := c.worker(uid)

	uconversation := worker.getUserConversation(uid)
	if uconversation == nil {
		return nil
	}
	return uconversation.getConversationsByType(conversationType)

}

func (c *ConversationManager) DeleteUserConversationFromCache(uid string, channelId string, channelType uint8) {
	userconversation := c.worker(uid).getUserConversation(uid)
	if userconversation == nil {
		return
	}
	userconversation.deleteConversation(channelId, channelType)
}

func (c *ConversationManager) existConversationInCache(uid string, channelId string, channelType uint8) bool {
	userconversation := c.worker(uid).getUserConversation(uid)
	if userconversation == nil {
		return false
	}
	return userconversation.existConversation(channelId, channelType)

}

type conversationWorker struct {
	s                 *Server
	userConversations []*userConversation
	wklog.Log
	index   int
	stopper *syncutil.Stopper

	sync.RWMutex
}

func newConversationWorker(i int, s *Server) *conversationWorker {
	return &conversationWorker{
		s:       s,
		Log:     wklog.NewWKLog(fmt.Sprintf("conversationWorker[%d]", i)),
		index:   i,
		stopper: syncutil.NewStopper(),
	}
}

func (c *conversationWorker) start() error {
	c.stopper.RunWorker(c.loopPropose)
	return nil
}

func (c *conversationWorker) stop() {
	c.stopper.Stop()
}

func (c *conversationWorker) loopPropose() {

	tk := time.NewTicker(time.Minute * 5)

	for {
		select {
		case <-tk.C:
			c.propose()
		case <-c.stopper.ShouldStop():
			return
		}
	}

}

func (c *conversationWorker) propose() {

	c.Lock()
	tmpUserConversations := make([]*userConversation, len(c.userConversations))
	if len(c.userConversations) > 0 {
		copy(tmpUserConversations, c.userConversations)
	}
	c.Unlock()

	for _, cc := range tmpUserConversations {

		var conversations []wkdb.Conversation

		cc.Lock()
		for _, conversation := range cc.conversations {
			if conversation.NeedUpdate {
				conversation.NeedUpdate = false // 提前设置为false，防止在更新的时候再次更新
				var conversationType wkdb.ConversationType
				if c.s.opts.IsCmdChannel(conversation.ChannelId) {
					conversationType = wkdb.ConversationTypeCMD
				} else {
					conversationType = wkdb.ConversationTypeChat
				}
				conversations = append(conversations, wkdb.Conversation{
					Uid:            cc.uid,
					Type:           conversationType,
					ChannelId:      conversation.ChannelId,
					ChannelType:    conversation.ChannelType,
					ReadedToMsgSeq: uint64(conversation.ReadedMsgSeq),
				})
			}
		}
		cc.Unlock()
		if len(conversations) > 0 {
			err := c.s.store.AddOrUpdateConversations(cc.uid, conversations)
			if err != nil {
				c.Error("add or update conversations err", zap.Error(err))

				// 如果更新失败，则需要重新更新
				cc.Lock()
				for _, conversation := range cc.conversations {
					conversation.NeedUpdate = true
				}
				cc.Unlock()
			}

		}
	}
}

func (c *conversationWorker) getOrCreateUserConversation(uid string) *userConversation {
	c.Lock()
	defer c.Unlock()
	for _, cc := range c.userConversations {
		if cc.uid == uid {
			return cc
		}
	}

	cc := newUserConversation(uid, c.s)
	c.userConversations = append(c.userConversations, cc)
	return cc
}

func (c *conversationWorker) getUserConversation(uid string) *userConversation {

	c.Lock()
	defer c.Unlock()

	for _, cc := range c.userConversations {
		if cc.uid == uid {
			return cc
		}
	}
	return nil

}

type userConversation struct {
	uid           string
	conversations []*channelConversation
	s             *Server
	deadlock.RWMutex
}

func newUserConversation(uid string, s *Server) *userConversation {
	return &userConversation{
		uid: uid,
		s:   s,
	}
}

func (c *userConversation) existConversation(channelId string, channelType uint8) bool {
	c.RLock()
	defer c.RUnlock()
	return c.existConversationNotLock(channelId, channelType)
}

func (c *userConversation) existConversationNotLock(channelId string, channelType uint8) bool {
	for _, s := range c.conversations {
		if s.ChannelId == channelId && s.ChannelType == channelType {
			return true
		}
	}

	return false
}

func (c *userConversation) getConversation(channelId string, channelType uint8) *channelConversation {
	c.RLock()
	defer c.RUnlock()

	for _, s := range c.conversations {
		if s.ChannelId == channelId && s.ChannelType == channelType {
			return s
		}
	}

	return nil
}

func (c *userConversation) getConversationsByType(conversationType wkdb.ConversationType) []wkdb.Conversation {

	c.RLock()
	defer c.RUnlock()

	var conversations []wkdb.Conversation

	for _, s := range c.conversations {
		if s.ConversationType == conversationType {
			conversations = append(conversations, wkdb.Conversation{
				Uid:            c.uid,
				Type:           s.ConversationType,
				ChannelId:      s.ChannelId,
				ChannelType:    s.ChannelType,
				ReadedToMsgSeq: uint64(s.ReadedMsgSeq),
			})
		}
	}

	return conversations
}

func (c *userConversation) deleteConversation(channelId string, channelType uint8) {
	c.Lock()
	defer c.Unlock()

	for i, s := range c.conversations {
		if s.ChannelId == channelId && s.ChannelType == channelType {
			c.conversations = append(c.conversations[:i], c.conversations[i+1:]...)
			return
		}
	}
}

func (c *userConversation) getConversationNotLock(channelId string, channelType uint8) *channelConversation {

	for _, s := range c.conversations {
		if s.ChannelId == channelId && s.ChannelType == channelType {
			return s
		}
	}

	return nil
}

func (c *userConversation) addConversationIfNotExist(channelId string, channelType uint8, readedMsgSeq uint32) *channelConversation {
	c.Lock()
	defer c.Unlock()
	if !c.existConversationNotLock(channelId, channelType) {

		return c.addConversationNotLock(channelId, channelType, readedMsgSeq)
	}
	return nil
}

func (c *userConversation) updateOrAddConversation(channelId string, channelType uint8, readedMsgSeq uint32) {

	c.Lock()
	defer c.Unlock()

	conversation := c.getConversationNotLock(channelId, channelType)
	if conversation != nil {
		if conversation.ReadedMsgSeq < readedMsgSeq {
			conversation.ReadedMsgSeq = readedMsgSeq
			conversation.NeedUpdate = true
		}
		return
	}

	var conversationType wkdb.ConversationType
	if c.s.opts.IsCmdChannel(channelId) {
		conversationType = wkdb.ConversationTypeCMD
	} else {
		conversationType = wkdb.ConversationTypeChat
	}

	c.conversations = append(c.conversations, &channelConversation{
		ChannelId:        channelId,
		ChannelType:      channelType,
		ReadedMsgSeq:     readedMsgSeq,
		ConversationType: conversationType,
		NeedUpdate:       true,
	})
}

func (c *userConversation) addConversationNotLock(channelId string, channelType uint8, readedMsgSeq uint32) *channelConversation {

	var conversationType wkdb.ConversationType
	if c.s.opts.IsCmdChannel(channelId) {
		conversationType = wkdb.ConversationTypeCMD
	} else {
		conversationType = wkdb.ConversationTypeChat
	}

	cn := &channelConversation{
		ChannelId:        channelId,
		ChannelType:      channelType,
		ReadedMsgSeq:     readedMsgSeq,
		NeedUpdate:       true,
		ConversationType: conversationType,
	}
	c.conversations = append(c.conversations, cn)

	return cn
}

type channelConversation struct {
	ChannelId        string                `json:"channel_id"`
	ChannelType      uint8                 `json:"channel_type"`
	ReadedMsgSeq     uint32                `json:"readed_msg_seq"`
	NeedUpdate       bool                  `json:"need_update"`
	ConversationType wkdb.ConversationType `json:"conversation_type"`
}
