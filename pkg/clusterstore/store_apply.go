package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"go.uber.org/zap"
)

// 频道元数据应用
func (s *Store) OnMetaApply(slotId uint32, logs []replica.Log) error {
	for _, lg := range logs {
		err := s.onMetaApply(slotId, lg)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) onMetaApply(slotId uint32, log replica.Log) error {
	cmd := &CMD{}
	err := cmd.Unmarshal(log.Data)
	if err != nil {
		s.Error("unmarshal cmd err", zap.Error(err), zap.Uint32("slotId", slotId), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
		return err
	}
	s.Info("收到元数据请求", zap.Uint32("slotId", slotId), zap.String("cmdType", cmd.CmdType.String()), zap.Int("dataLen", len(cmd.Data)))
	switch cmd.CmdType {
	case CMDAddSubscribers: // 添加订阅者
		return s.handleAddSubscribers(cmd)
	case CMDRemoveSubscribers: // 移除订阅者
		return s.handleRemoveSubscribers(cmd)
	case CMDUpdateUserToken: // 更新用户的token
		return s.handleUpdateUserToken(cmd)
	case CMDUpdateMessageOfUserCursorIfNeed: // 更新用户消息队列的游标，用户读到的位置
		return s.handleUpdateMessageOfUserCursorIfNeed(cmd)
	case CMDAddOrUpdateChannel: // 添加或更新频道
		return s.handleAddOrUpdateChannel(cmd)
	case CMDRemoveAllSubscriber: // 移除所有订阅者
		return s.handleRemoveAllSubscriber(cmd)
	case CMDDeleteChannel: // 删除频道
		return s.handleDeleteChannel(cmd)
	case CMDAddDenylist: // 添加黑名单
		return s.handleAddDenylist(cmd)
	case CMDRemoveDenylist: // 移除黑名单
		return s.handleRemoveDenylist(cmd)
	case CMDRemoveAllDenylist: // 移除所有黑名单
		return s.handleRemoveAllDenylist(cmd)
	case CMDAddAllowlist: // 添加白名单
		return s.handleAddAllowlist(cmd)
	case CMDRemoveAllowlist: // 移除白名单
		return s.handleRemoveAllowlist(cmd)
	case CMDRemoveAllAllowlist: // 移除所有白名单
		return s.handleRemoveAllAllowlist(cmd)
	case CMDAddOrUpdateConversations: // 添加或更新会话
		return s.handleAddOrUpdateConversations(cmd)
	case CMDDeleteConversation: // 删除会话
		return s.handleDeleteConversation(cmd)
	case CMDChannelClusterConfigSave: // 保存频道分布式配置
		return s.handleChannelClusterConfigSave(cmd)
	case CMDAppendMessagesOfUser: // 向用户队列里增加消息
		return s.handleAppendMessagesOfUser(cmd)
		// case CMDChannelClusterConfigDelete: // 删除频道分布式配置
		// return s.handleChannelClusterConfigDelete(cmd)

	}
	return nil
}

func (s *Store) handleAddSubscribers(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		s.Error("decode subscribers err", zap.Error(err), zap.String("channelID", channelId), zap.Uint8("channelType", channelType), zap.ByteString("data", cmd.Data))
		return err
	}
	return s.db.AddSubscribers(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveSubscribers(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.RemoveSubscribers(channelId, channelType, subscribers)
}

func (s *Store) handleUpdateUserToken(cmd *CMD) error {
	uid, deviceFlag, deviceLevel, token, err := cmd.DecodeCMDUserToken()
	if err != nil {
		return err
	}
	return s.db.UpdateUserToken(uid, deviceFlag, deviceLevel, token)
}

func (s *Store) handleUpdateMessageOfUserCursorIfNeed(cmd *CMD) error {
	uid, messageSeq, err := cmd.DecodeCMDUpdateMessageOfUserCursorIfNeed()
	if err != nil {
		return err
	}
	return s.db.UpdateMessageOfUserCursorIfNeed(uid, messageSeq)
}

func (s *Store) handleAddOrUpdateChannel(cmd *CMD) error {
	channelInfo, err := cmd.DecodeAddOrUpdateChannel()
	if err != nil {
		return err
	}
	return s.db.AddOrUpdateChannel(channelInfo)
}

func (s *Store) handleRemoveAllSubscriber(cmd *CMD) error {
	channelId, channelType, err := cmd.DecodeChannel()
	if err != nil {
		return err
	}
	return s.db.RemoveAllSubscriber(channelId, channelType)
}

func (s *Store) handleDeleteChannel(cmd *CMD) error {
	channelId, channelType, err := cmd.DecodeChannel()
	if err != nil {
		return err
	}
	return s.db.DeleteChannel(channelId, channelType)
}

func (s *Store) handleAddDenylist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.AddDenylist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveDenylist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.RemoveDenylist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveAllDenylist(cmd *CMD) error {
	channelId, channelType, err := cmd.DecodeChannel()
	if err != nil {
		return err
	}
	return s.db.RemoveAllDenylist(channelId, channelType)
}

func (s *Store) handleAddAllowlist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.AddAllowlist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveAllowlist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.db.RemoveAllowlist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveAllAllowlist(cmd *CMD) error {
	channelId, channelType, err := cmd.DecodeChannel()
	if err != nil {
		return err
	}
	return s.db.RemoveAllAllowlist(channelId, channelType)
}

func (s *Store) handleAddOrUpdateConversations(cmd *CMD) error {
	uid, conversations, err := cmd.DecodeCMDAddOrUpdateConversations()
	if err != nil {
		return err
	}
	return s.db.AddOrUpdateConversations(uid, conversations)
}

func (s *Store) handleDeleteConversation(cmd *CMD) error {
	uid, deleteChannelID, deleteChannelType, err := cmd.DecodeCMDDeleteConversation()
	if err != nil {
		return err
	}
	return s.db.DeleteConversation(uid, deleteChannelID, deleteChannelType)
}

func (s *Store) handleChannelClusterConfigSave(cmd *CMD) error {
	channelId, channelType, configData, err := cmd.DecodeCMDChannelClusterConfigSave()
	if err != nil {
		return err
	}
	channelClusterConfig := &wkstore.ChannelClusterConfig{}
	err = channelClusterConfig.Unmarshal(configData)
	if err != nil {
		return err
	}

	err = s.db.SaveChannelClusterConfig(channelId, channelType, channelClusterConfig)
	if err != nil {
		return err
	}
	return nil
}

// func (s *Store) handleChannelClusterConfigDelete(cmd *CMD) error {
// 	channelId, channelType, err := cmd.DecodeChannel()
// 	if err != nil {
// 		return err
// 	}
// 	return s.db.DeleteChannelClusterConfig(channelId, channelType)
// }

func (s *Store) handleAppendMessagesOfUser(cmd *CMD) error {
	uid, messages, err := cmd.DecodeCMDAppendMessagesOfUser(s.opts.DecodeMessageFnc)
	if err != nil {
		return err
	}
	return s.db.AppendMessagesOfUserQueue(uid, messages)
}
