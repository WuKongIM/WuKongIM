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

func (s *Store) DeleteConversations(uid string, channels []wkdb.Channel) error {
	data := EncodeCMDDeleteConversations(uid, channels)
	cmd := NewCMD(CMDDeleteConversations, data)
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

func (s *Store) BatchUpdateConversation(slotId uint32, models []*wkdb.BatchUpdateConversationModel) error {
	cmd := NewCMD(CMDBatchUpdateConversation, EncodeCMDBatchUpdateConversation(models))
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
	return err
}
