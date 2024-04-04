package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func (s *Store) AddOrUpdateConversations(uid string, conversations []wkdb.Conversation) error {
	if len(conversations) == 0 {
		return nil
	}
	var (
		channelID   = uid
		channelType = wkproto.ChannelTypePerson
	)
	data, err := EncodeCMDAddOrUpdateConversations(uid, conversations)
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDAddOrUpdateConversations, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	_, err = s.opts.Cluster.ProposeChannelMeta(s.ctx, channelID, channelType, cmdData)
	return err
}

func (s *Store) DeleteConversation(uid string, channelID string, channelType uint8) error {
	data := EncodeCMDDeleteConversation(uid, channelID, channelType)
	cmd := NewCMD(CMDDeleteConversation, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	_, err = s.opts.Cluster.ProposeChannelMeta(s.ctx, uid, wkproto.ChannelTypePerson, cmdData)
	return err
}

func (s *Store) GetConversations(uid string) ([]wkdb.Conversation, error) {
	return s.wdb.GetConversations(uid)
}

func (s *Store) GetConversation(uid string, channelID string, channelType uint8) (wkdb.Conversation, error) {
	return s.wdb.GetConversation(uid, channelID, channelType)
}
