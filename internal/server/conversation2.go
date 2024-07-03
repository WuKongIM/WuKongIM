package server

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type ConversationManager struct {
	stopper *syncutil.Stopper
	wklog.Log
	s *Server

	workers []*conversationWorker

	sync.RWMutex
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

	worker := c.worker(fakeChannelId)

	// 处理发送者的最近会话
	for _, message := range messages {
		if message.FromUid == "" {
			continue
		}
		if message.SendPacket.NoPersist {
			continue
		}
		worker.getOrCreateUserConversation(message.FromUid).updateOrAddConversation(fakeChannelId, channelType, message.MessageSeq)
	}

	// 处理接受者的最近会话
	for _, uid := range uids {
		worker.getOrCreateUserConversation(uid).addConversationIfNotExist(fakeChannelId, channelType, 0)
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

	return nil
}

func (c *ConversationManager) Stop() {

	for _, w := range c.workers {
		w.stop()
	}
}

func (c *ConversationManager) worker(channelId string) *conversationWorker {
	c.Lock()
	defer c.Unlock()
	h := fnv.New32a()
	h.Write([]byte(channelId))

	i := h.Sum32() % uint32(len(c.workers))
	return c.workers[i]
}

func (c *ConversationManager) GetUserConversationFromCache(uid string, conversationType wkdb.ConversationType) []wkdb.Conversation {

	return nil
}

func (c *ConversationManager) DeleteUserConversationFromCache(uid string, channelId string, channelType uint8) {

}

func (c *ConversationManager) existConversationInCache(uid string, channelId string, channelType uint8) bool {
	return false
}

type conversationWorker struct {
	s                 *Server
	userConversations []*userConversation
	wklog.Log
	index   int
	stopper *syncutil.Stopper
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

	tk := time.NewTicker(time.Second * 50)

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

	var conversations []wkdb.Conversation
	for _, cc := range c.userConversations {
		for _, conversation := range cc.conversations {
			if conversation.needUpdate {
				var conversationType wkdb.ConversationType
				if c.s.opts.IsCmdChannel(conversation.channelId) {
					conversationType = wkdb.ConversationTypeCMD
				} else {
					conversationType = wkdb.ConversationTypeChat
				}
				conversations = append(conversations, wkdb.Conversation{
					Uid:            cc.uid,
					Type:           conversationType,
					ChannelId:      conversation.channelId,
					ChannelType:    conversation.channelType,
					ReadedToMsgSeq: uint64(conversation.readedMsgSeq),
				})
			}
		}

		if len(conversations) > 0 {
			err := c.s.store.AddOrUpdateConversations(cc.uid, conversations)
			if err != nil {
				c.Error("add or update conversations err", zap.Error(err))
			}
		}
	}
}

func (c *conversationWorker) getOrCreateUserConversation(uid string) *userConversation {
	for _, cc := range c.userConversations {
		if cc.uid == uid {
			return cc
		}
	}

	cc := &userConversation{
		uid: uid,
	}
	return cc
}

type userConversation struct {
	uid           string
	conversations []*channelConversation

	sync.RWMutex
}

func (c *userConversation) existConversation(channelId string, channelType uint8) bool {
	c.RLock()
	defer c.RUnlock()
	return c.existConversationNotLock(channelId, channelType)
}

func (c *userConversation) existConversationNotLock(channelId string, channelType uint8) bool {
	for _, s := range c.conversations {
		if s.channelId == channelId && s.channelType == channelType {
			return true
		}
	}

	return false
}

func (c *userConversation) getConversation(channelId string, channelType uint8) *channelConversation {
	c.RLock()
	defer c.RUnlock()

	for _, s := range c.conversations {
		if s.channelId == channelId && s.channelType == channelType {
			return s
		}
	}

	return nil
}

func (c *userConversation) getConversationNotLock(channelId string, channelType uint8) *channelConversation {

	for _, s := range c.conversations {
		if s.channelId == channelId && s.channelType == channelType {
			return s
		}
	}

	return nil
}

func (c *userConversation) addConversationIfNotExist(channelId string, channelType uint8, readedMsgSeq uint32) {
	c.Lock()
	defer c.Unlock()
	if !c.existConversationNotLock(channelId, channelType) {
		c.addConversationNotLock(channelId, channelType, readedMsgSeq)
	}
}

func (c *userConversation) updateOrAddConversation(channelId string, channelType uint8, readedMsgSeq uint32) {

	c.Lock()
	defer c.Unlock()

	conversation := c.getConversationNotLock(channelId, channelType)
	if conversation != nil {
		if conversation.readedMsgSeq < readedMsgSeq {
			conversation.readedMsgSeq = readedMsgSeq
			conversation.needUpdate = true
		}
		return
	}

	c.conversations = append(c.conversations, &channelConversation{
		channelId:    channelId,
		channelType:  channelType,
		readedMsgSeq: readedMsgSeq,
		needUpdate:   true,
	})
}

func (c *userConversation) addConversationNotLock(channelId string, channelType uint8, readedMsgSeq uint32) {

	c.conversations = append(c.conversations, &channelConversation{
		channelId:    channelId,
		channelType:  channelType,
		readedMsgSeq: readedMsgSeq,
		needUpdate:   true,
	})

}

type channelConversation struct {
	channelId    string
	channelType  uint8
	readedMsgSeq uint32
	needUpdate   bool
}
