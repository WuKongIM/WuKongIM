package clusterstore

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
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

	start := time.Now()
	defer func() {
		end := time.Since(start)
		if end > time.Millisecond*100 {
			s.Debug("收到元数据请求", zap.Duration("cost", end), zap.Uint32("slotId", slotId), zap.String("cmdType", cmd.CmdType.String()), zap.Int("dataLen", len(cmd.Data)))
		}
	}()
	switch cmd.CmdType {
	case CMDAddSubscribers: // 添加订阅者
		return s.handleAddSubscribers(cmd)
	case CMDRemoveSubscribers: // 移除订阅者
		return s.handleRemoveSubscribers(cmd)
	case CMDAddOrUpdateUser: // 添加或更新用户
		return s.handleAddOrUpdateUser(cmd)
	case CMDAddOrUpdateDevice: // 更新设备信息
		return s.handleAddOrUpdateDevice(cmd)
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
	case CMDDeleteConversations: // 批量删除某个用户的最近会话
		return s.handleDeleteConversations(cmd)
	case CMDChannelClusterConfigSave: // 保存频道分布式配置
		return s.handleChannelClusterConfigSave(cmd)
	// case CMDAppendMessagesOfUser: // 向用户队列里增加消息
	// 	return s.handleAppendMessagesOfUser(cmd)
	case CMDBatchUpdateConversation:
		return s.handleBatchUpdateConversation(cmd)
	case CMDAddOrUpdateUserAndDevice: // 添加或更新用户和设备
		return s.handleAddOrUpdateUserAndDevice(cmd)
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
	return s.wdb.AddSubscribers(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveSubscribers(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.wdb.RemoveSubscribers(channelId, channelType, subscribers)
}

func (s *Store) handleAddOrUpdateUser(cmd *CMD) error {
	u, err := cmd.DecodeCMDUser()
	if err != nil {
		return err
	}
	return s.wdb.AddOrUpdateUser(u)
}

func (s *Store) handleAddOrUpdateDevice(cmd *CMD) error {
	u, err := cmd.DecodeCMDDevice()
	if err != nil {
		return err
	}
	return s.wdb.AddOrUpdateDevice(u)
}

func (s *Store) handleUpdateMessageOfUserCursorIfNeed(cmd *CMD) error {
	uid, messageSeq, err := cmd.DecodeCMDUpdateMessageOfUserCursorIfNeed()
	if err != nil {
		return err
	}
	return s.wdb.UpdateMessageOfUserQueueCursorIfNeed(uid, messageSeq)
}

func (s *Store) handleAddOrUpdateChannel(cmd *CMD) error {
	channelInfo, err := cmd.DecodeAddOrUpdateChannel()
	if err != nil {
		return err
	}
	_, err = s.wdb.AddOrUpdateChannel(channelInfo)
	return err
}

func (s *Store) handleRemoveAllSubscriber(cmd *CMD) error {
	channelId, channelType, err := cmd.DecodeChannel()
	if err != nil {
		return err
	}
	return s.wdb.RemoveAllSubscriber(channelId, channelType)
}

func (s *Store) handleDeleteChannel(cmd *CMD) error {
	channelId, channelType, err := cmd.DecodeChannel()
	if err != nil {
		return err
	}
	return s.wdb.DeleteChannel(channelId, channelType)
}

func (s *Store) handleAddDenylist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.wdb.AddDenylist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveDenylist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.wdb.RemoveDenylist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveAllDenylist(cmd *CMD) error {
	channelId, channelType, err := cmd.DecodeChannel()
	if err != nil {
		return err
	}
	return s.wdb.RemoveAllDenylist(channelId, channelType)
}

func (s *Store) handleAddAllowlist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.wdb.AddAllowlist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveAllowlist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	return s.wdb.RemoveAllowlist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveAllAllowlist(cmd *CMD) error {
	channelId, channelType, err := cmd.DecodeChannel()
	if err != nil {
		return err
	}
	return s.wdb.RemoveAllAllowlist(channelId, channelType)
}

func (s *Store) handleAddOrUpdateConversations(cmd *CMD) error {
	uid, conversations, err := cmd.DecodeCMDAddOrUpdateConversations()
	if err != nil {
		return err
	}
	return s.wdb.AddOrUpdateConversations(uid, conversations)
}

func (s *Store) handleDeleteConversation(cmd *CMD) error {
	uid, deleteChannelID, deleteChannelType, err := cmd.DecodeCMDDeleteConversation()
	if err != nil {
		return err
	}
	return s.wdb.DeleteConversation(uid, deleteChannelID, deleteChannelType)
}

func (s *Store) handleDeleteConversations(cmd *CMD) error {
	uid, channels, err := cmd.DecodeCMDDeleteConversations()
	if err != nil {
		return err
	}
	return s.wdb.DeleteConversations(uid, channels)
}

func (s *Store) handleChannelClusterConfigSave(cmd *CMD) error {
	_, _, configData, err := cmd.DecodeCMDChannelClusterConfigSave()
	if err != nil {
		return err
	}
	channelClusterConfig := wkdb.ChannelClusterConfig{}
	err = channelClusterConfig.Unmarshal(configData)
	if err != nil {
		return err
	}

	waitC := make(chan error, 1)

	select {
	case s.saveChannelClusterConfigReq <- &saveChannelClusterConfigReq{
		cfg:     channelClusterConfig,
		resultC: waitC,
	}:
	case <-s.stopper.ShouldStop():
		return ErrStoreStopped
	}

	select {
	case err := <-waitC:
		return err
	case <-s.stopper.ShouldStop():
		return ErrStoreStopped
	}
}

// func (s *Store) handleChannelClusterConfigDelete(cmd *CMD) error {
// 	channelId, channelType, err := cmd.DecodeChannel()
// 	if err != nil {
// 		return err
// 	}
// 	return s.db.DeleteChannelClusterConfig(channelId, channelType)
// }

func (s *Store) handleAppendMessagesOfUser(cmd *CMD) error {
	uid, messages, err := cmd.DecodeCMDAppendMessagesOfUser()
	if err != nil {
		return err
	}
	return s.wdb.AppendMessagesOfUserQueue(uid, messages)
}

func (s *Store) handleBatchUpdateConversation(cmd *CMD) error {
	models, err := cmd.DecodeCMDBatchUpdateConversation()
	if err != nil {
		return err
	}
	for _, model := range models {
		var conversationType = wkdb.ConversationTypeChat
		if s.opts.IsCmdChannel(model.ChannelId) {
			conversationType = wkdb.ConversationTypeCMD
		}
		for uid, seq := range model.Uids {
			conversation := wkdb.Conversation{
				Uid:          uid,
				Type:         conversationType,
				ChannelId:    model.ChannelId,
				ChannelType:  model.ChannelType,
				ReadToMsgSeq: seq,
			}
			err = s.wdb.AddOrUpdateConversations(uid, []wkdb.Conversation{conversation})
			if err != nil {
				return err
			}
		}

	}
	return nil
}

func (s *Store) handleAddOrUpdateUserAndDevice(cmd *CMD) error {

	id, uid, deviceFlag, deviceLevel, token, err := cmd.DecodeCMDUserAndDevice()
	if err != nil {
		return err
	}

	exist, err := s.wdb.ExistUser(uid)
	if err != nil {
		return err
	}

	if !exist {
		err = s.wdb.AddOrUpdateUser(wkdb.User{
			Id:  id,
			Uid: uid,
		})
		if err != nil {
			return err
		}
	}

	return s.wdb.AddOrUpdateDevice(wkdb.Device{
		Id:          id,
		Uid:         uid,
		DeviceFlag:  deviceFlag,
		DeviceLevel: uint8(deviceLevel),
		Token:       token,
	})
}
