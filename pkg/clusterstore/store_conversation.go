package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

func (s *Store) AddOrUpdateConversations(uid string, conversations []wkdb.Conversation) error {
	if len(conversations) == 0 {
		return nil
	}
	data, err := EncodeCMDAddOrUpdateConversations(uid, conversations)
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDAddOrUpdateConversations, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	slotId := s.opts.GetSlotId(uid)
	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
	return err
}

func (s *Store) DeleteConversation(uid string, channelID string, channelType uint8) error {
	data := EncodeCMDDeleteConversation(uid, channelID, channelType)
	cmd := NewCMD(CMDDeleteConversation, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	slotId := s.opts.GetSlotId(uid)
	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
	return err
}

func (s *Store) GetConversations(uid string) ([]wkdb.Conversation, error) {
	return s.wdb.GetConversations(uid)
}

func (s *Store) GetConversation(uid string, sessionId uint64) (wkdb.Conversation, error) {
	return s.wdb.GetConversation(uid, sessionId)
}

func (s *Store) GetConversationBySessionIds(uid string, sessionIds []uint64) ([]wkdb.Conversation, error) {
	return s.wdb.GetConversationBySessionIds(uid, sessionIds)
}
