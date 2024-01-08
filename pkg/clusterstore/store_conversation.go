package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func (s *Store) AddOrUpdateConversations(uid string, conversations []*wkstore.Conversation) error {
	if len(conversations) == 0 {
		return nil
	}
	var (
		channelID   = uid
		channelType = wkproto.ChannelTypePerson
	)
	data := EncodeCMDAddOrUpdateConversations(uid, conversations)
	cmd := NewCMD(CMDAddOrUpdateConversations, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeMetaToChannel(channelID, channelType, cmdData)
}

func (s *Store) DeleteConversation(uid string, channelID string, channelType uint8) error {
	data := EncodeCMDDeleteConversation(uid, channelID, channelType)
	cmd := NewCMD(CMDDeleteConversation, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeMetaToChannel(uid, wkproto.ChannelTypePerson, cmdData)
}

func (s *Store) GetConversations(uid string) ([]*wkstore.Conversation, error) {
	return s.db.GetConversations(uid)
}

func (s *Store) GetConversation(uid string, channelID string, channelType uint8) (*wkstore.Conversation, error) {
	return s.db.GetConversation(uid, channelID, channelType)
}
