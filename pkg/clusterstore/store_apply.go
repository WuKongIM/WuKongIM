package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"go.uber.org/zap"
)

// 频道元数据应用
func (s *Store) OnMetaApply(channelID string, channelType uint8, logs []replica.Log) error {
	for _, lg := range logs {
		err := s.onMetaApply(channelID, channelType, lg)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) onMetaApply(channelID string, channelType uint8, log replica.Log) error {
	s.Debug("OnMetaApply", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
	cmd := &CMD{}
	err := cmd.Unmarshal(log.Data)
	if err != nil {
		s.Error("unmarshal cmd err", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
		return err
	}
	switch cmd.CmdType {
	case CMDAddSubscribers: // 添加订阅者
		return s.handleAddSubscribers(channelID, channelType, cmd)
	case CMDRemoveSubscribers: // 移除订阅者
		return s.handleRemoveSubscribers(channelID, channelType, cmd)
	case CMDUpdateUserToken: // 更新用户的token
		return s.handleUpdateUserToken(channelID, channelType, cmd)
	case CMDUpdateMessageOfUserCursorIfNeed: // 更新用户消息队列的游标，用户读到的位置
		return s.handleUpdateMessageOfUserCursorIfNeed(channelID, channelType, cmd)
	case CMDAddOrUpdateChannel: // 添加或更新频道
		return s.handleAddOrUpdateChannel(channelID, channelType, cmd)
	case CMDRemoveAllSubscriber: // 移除所有订阅者
		return s.handleRemoveAllSubscriber(channelID, channelType, cmd)
	case CMDDeleteChannel: // 删除频道
		return s.handleDeleteChannel(channelID, channelType, cmd)
	case CMDAddDenylist: // 添加黑名单
		return s.handleAddDenylist(channelID, channelType, cmd)
	case CMDRemoveDenylist: // 移除黑名单
		return s.handleRemoveDenylist(channelID, channelType, cmd)
	case CMDRemoveAllDenylist: // 移除所有黑名单
		return s.handleRemoveAllDenylist(channelID, channelType, cmd)
	case CMDAddAllowlist: // 添加白名单
		return s.handleAddAllowlist(channelID, channelType, cmd)
	case CMDRemoveAllowlist: // 移除白名单
		return s.handleRemoveAllowlist(channelID, channelType, cmd)
	case CMDRemoveAllAllowlist: // 移除所有白名单
		return s.handleRemoveAllAllowlist(channelID, channelType, cmd)
	case CMDAddOrUpdateConversations: // 添加或更新会话
		return s.handleAddOrUpdateConversations(channelID, channelType, cmd)
	case CMDDeleteConversation: // 删除会话
		return s.handleDeleteConversation(channelID, channelType, cmd)

	}
	return nil
}

func (s *Store) handleAddSubscribers(channelID string, channelType uint8, cmd *CMD) error {
	s.Debug("handleAddSubscribers", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.ByteString("data", cmd.Data))
	subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		s.Error("decode subscribers err", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.ByteString("data", cmd.Data))
		return err
	}
	return s.db.AddSubscribers(channelID, channelType, subscribers)
}

func (s *Store) handleRemoveSubscribers(channelID string, channelType uint8, cmd *CMD) error {
	subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.RemoveSubscribers(channelID, channelType, subscribers)
}

func (s *Store) handleUpdateUserToken(channelID string, channelType uint8, cmd *CMD) error {
	uid, deviceFlag, deviceLevel, token, err := cmd.DecodeCMDUserToken()
	if err != nil {
		return err
	}
	return s.db.UpdateUserToken(uid, deviceFlag, deviceLevel, token)
}

func (s *Store) handleUpdateMessageOfUserCursorIfNeed(channelID string, channelType uint8, cmd *CMD) error {
	uid, messageSeq, err := cmd.DecodeCMDUpdateMessageOfUserCursorIfNeed()
	if err != nil {
		return err
	}
	return s.db.UpdateMessageOfUserCursorIfNeed(uid, messageSeq)
}

func (s *Store) handleAddOrUpdateChannel(channelID string, channelType uint8, cmd *CMD) error {
	channelInfo, err := cmd.DecodeAddOrUpdateChannel()
	if err != nil {
		return err
	}
	return s.db.AddOrUpdateChannel(channelInfo)
}

func (s *Store) handleRemoveAllSubscriber(channelID string, channelType uint8, cmd *CMD) error {
	return s.db.RemoveAllSubscriber(channelID, channelType)
}

func (s *Store) handleDeleteChannel(channelID string, channelType uint8, cmd *CMD) error {
	return s.db.DeleteChannel(channelID, channelType)
}

func (s *Store) handleAddDenylist(channelID string, channelType uint8, cmd *CMD) error {
	subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.AddDenylist(channelID, channelType, subscribers)
}

func (s *Store) handleRemoveDenylist(channelID string, channelType uint8, cmd *CMD) error {
	subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.RemoveDenylist(channelID, channelType, subscribers)
}

func (s *Store) handleRemoveAllDenylist(channelID string, channelType uint8, cmd *CMD) error {
	return s.db.RemoveAllDenylist(channelID, channelType)
}

func (s *Store) handleAddAllowlist(channelID string, channelType uint8, cmd *CMD) error {
	subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.AddAllowlist(channelID, channelType, subscribers)
}

func (s *Store) handleRemoveAllowlist(channelID string, channelType uint8, cmd *CMD) error {
	subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.RemoveAllowlist(channelID, channelType, subscribers)
}

func (s *Store) handleRemoveAllAllowlist(channelID string, channelType uint8, cmd *CMD) error {
	return s.db.RemoveAllAllowlist(channelID, channelType)
}

func (s *Store) handleAddOrUpdateConversations(channelID string, channelType uint8, cmd *CMD) error {
	uid, conversations, err := cmd.DecodeCMDAddOrUpdateConversations()
	if err != nil {
		return err
	}
	return s.db.AddOrUpdateConversations(uid, conversations)
}

func (s *Store) handleDeleteConversation(channelID string, channelType uint8, cmd *CMD) error {
	uid, deleteChannelID, deleteChannelType, err := cmd.DecodeCMDDeleteConversation()
	if err != nil {
		return err
	}
	return s.db.DeleteConversation(uid, deleteChannelID, deleteChannelType)
}
