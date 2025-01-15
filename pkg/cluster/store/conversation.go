package store

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Store) AddOrUpdateConversations(conversations []wkdb.Conversation) error {
	// 将会话按照slotId来分组
	slotConversationsMap := make(map[uint32][]wkdb.Conversation)

	for _, c := range conversations {
		slotId := s.opts.Slot.GetSlotId(c.Uid)
		slotConversationsMap[slotId] = append(slotConversationsMap[slotId], c)
	}

	// 提交最近会话
	timeoutctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	g, _ := errgroup.WithContext(timeoutctx)
	g.SetLimit(100)

	for slotId, conversations := range slotConversationsMap {

		slotId, conversations := slotId, conversations

		g.Go(func() error {
			data, err := EncodeCMDAddOrUpdateConversations(conversations)
			if err != nil {
				return err
			}
			cmd := NewCMD(CMDAddOrUpdateConversations, data)
			cmdData, err := cmd.Marshal()
			if err != nil {
				return err
			}
			_, err = s.opts.Slot.ProposeUntilAppliedTimeout(timeoutctx, slotId, cmdData)
			if err != nil {
				s.Error("ProposeUntilAppliedTimeout failed", zap.Error(err), zap.Uint32("slotId", slotId), zap.Int("conversations", len(conversations)))
				return err
			}
			return nil
		})
	}
	err := g.Wait()
	if err != nil {
		s.Error("AddOrUpdateConversations failed", zap.Error(err), zap.Int("conversations", len(conversations)))
		return err
	}
	return err
}

func (s *Store) AddOrUpdateUserConversations(uid string, conversations []wkdb.Conversation) error {
	if len(conversations) == 0 {
		return nil
	}
	for i, c := range conversations {
		if c.Id == 0 {
			conversations[i].Id = s.wdb.NextPrimaryKey() // 如果id为0，生成一个新的id
		}
	}
	data, err := EncodeCMDAddOrUpdateUserConversations(uid, conversations)
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDAddOrUpdateUserConversations, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	slotId := s.opts.Slot.GetSlotId(uid)
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

// func (s *Store) AddOrUpdateConversationsWithChannel(channelId string, channelType uint8, subscribers []string, readToMsgSeq uint64, conversationType wkdb.ConversationType, unreadCount int) error {

// 	// 按照slotId来分组subscribers
// 	slotSubscriberMap := make(map[uint32][]string)

// 	for _, uid := range subscribers {
// 		slotId := s.opts.GetSlotId(uid)
// 		slotSubscriberMap[slotId] = append(slotSubscriberMap[slotId], uid)
// 	}

// 	// 提按最近会话
// 	timeoutctx, cancel := context.WithTimeout(s.ctx, time.Second*10)
// 	defer cancel()

// 	g, _ := errgroup.WithContext(timeoutctx)

// 	nw := time.Now().UnixNano()

// 	for slotId, subscribers := range slotSubscriberMap {

// 		slotId, subscribers := slotId, subscribers

// 		g.Go(func() error {
// 			data := EncodeCMDAddOrUpdateConversationsWithChannel(channelId, channelType, subscribers, readToMsgSeq, conversationType, unreadCount, nw, nw)
// 			cmd := NewCMD(CMDAddOrUpdateConversationsWithChannel, data)
// 			cmdData, err := cmd.Marshal()
// 			if err != nil {
// 				return err
// 			}
// 			_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
// 			if err != nil {
// 				return err
// 			}
// 			return nil
// 		})

// 	}
// 	err := g.Wait()
// 	return err
// }

func (s *Store) DeleteConversation(uid string, channelID string, channelType uint8) error {
	data := EncodeCMDDeleteConversation(uid, channelID, channelType)
	cmd := NewCMD(CMDDeleteConversation, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	slotId := s.opts.Slot.GetSlotId(uid)
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) DeleteConversations(uid string, channels []wkdb.Channel) error {
	data := EncodeCMDDeleteConversations(uid, channels)
	cmd := NewCMD(CMDDeleteConversations, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	slotId := s.opts.Slot.GetSlotId(uid)
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	return err
}

func (s *Store) GetConversations(uid string) ([]wkdb.Conversation, error) {
	return s.wdb.GetConversations(uid)
}

func (s *Store) GetConversationsByType(uid string, tp wkdb.ConversationType) ([]wkdb.Conversation, error) {
	return s.wdb.GetConversationsByType(uid, tp)
}

func (s *Store) GetConversation(uid string, channelId string, channelType uint8) (wkdb.Conversation, error) {
	return s.wdb.GetConversation(uid, channelId, channelType)
}

func (s *Store) GetLastConversations(uid string, tp wkdb.ConversationType, updatedAt uint64, limit int) ([]wkdb.Conversation, error) {

	return s.wdb.GetLastConversations(uid, tp, updatedAt, limit)
}

func (s *Store) GetChannelLastMessageSeq(channelId string, channelType uint8) (uint64, error) {
	seq, _, err := s.wdb.GetChannelLastMessageSeq(channelId, channelType)
	return seq, err
}

func (s *Store) GetChannelConversationLocalUsers(channelId string, channelType uint8) ([]string, error) {

	return s.wdb.GetChannelConversationLocalUsers(channelId, channelType)
}

func (s *Store) UpdateConversationIfSeqGreaterAsync(uid string, channelId string, channelType uint8, readToMsgSeq uint64) error {
	return s.wdb.UpdateConversationIfSeqGreaterAsync(uid, channelId, channelType, readToMsgSeq)
}
