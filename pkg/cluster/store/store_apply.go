package store

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"go.uber.org/zap"
)

// ApplyLogs 应用槽日志
func (s *Store) ApplySlotLogs(slotId uint32, logs []types.Log) error {
	for _, log := range logs {
		err := s.applyLog(slotId, log)
		if err != nil {
			s.Panic("apply log err", zap.Error(err), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
			return err
		}
	}
	return nil
}

func (s *Store) applyLog(_ uint32, log types.Log) error {
	cmd := &CMD{}
	err := cmd.Unmarshal(log.Data)
	if err != nil {
		s.Error("unmarshal cmd err", zap.Error(err), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
		return err
	}

	switch cmd.CmdType {
	case CMDChannelClusterConfigSave: // 保存频道分布式配置
		return s.handleChannelClusterConfigSave(cmd, log.Index)
	case CMDAddOrUpdateConversations: // 添加或更新会话
		return s.handleAddOrUpdateConversations(cmd)
	case CMDAddSubscribers: // 添加订阅者
		return s.handleAddSubscribers(cmd)
	case CMDRemoveSubscribers: // 移除订阅者
		return s.handleRemoveSubscribers(cmd)
	case CMDAddUser: // 添加用户
		return s.handleAddUser(cmd)
	case CMDUpdateUser: // 更新用户
		return s.handleUpdateUser(cmd)
	case CMDAddDevice: // 添加设备信息
		return s.handleAddDevice(cmd)
	case CMDUpdateDevice: // 更新设备信息
		return s.handleUpdateDevice(cmd)
	case CMDAddChannelInfo: // 添加频道信息
		return s.handleAddChannelInfo(cmd)
	case CMDUpdateChannelInfo: // 更新频道信息
		return s.handleUpdateChannel(cmd)
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
	case CMDAddOrUpdateUserConversations: // 添加或更新会话
		return s.handleAddOrUpdateUserConversations(cmd)
	case CMDDeleteConversation: // 删除会话
		return s.handleDeleteConversation(cmd)
	case CMDDeleteConversations: // 批量删除某个用户的最近会话
		return s.handleDeleteConversations(cmd)
	// case CMDAppendMessagesOfUser: // 向用户队列里增加消息
	// 	return s.handleAppendMessagesOfUser(cmd)
	case CMDBatchUpdateConversation:
		return s.handleBatchUpdateConversation(cmd)
		// case CMDChannelClusterConfigDelete: // 删除频道分布式配置
		// return s.handleChannelClusterConfigDelete(cmd)
	case CMDSystemUIDsAdd: // 添加系统UID
		return s.handleSystemUIDsAdd(cmd)
	case CMDSystemUIDsRemove: // 移除系统UID
		return s.handleSystemUIDsRemove(cmd)
	case CMDAddStreamMeta: // 添加流元数据
		return s.handleAddStreamMeta(cmd)
	case CMDAddStreams: // 添加流
		return s.handleAddStreams(cmd)
	case CMDAddOrUpdateTester: // 添加或更新测试机
		return s.handleAddOrUpdateTester(cmd)
	case CMDRemoveTester: // 移除测试机
		return s.handleRemoveTester(cmd)

	}
	return nil
}

func (s *Store) loopSaveChannelClusterConfig() {
	maxSizeBatch := 1000
	done := false
	cfgs := make([]*channelCfgReq, 0, maxSizeBatch)
	for {
		select {
		case cfg := <-s.channelCfgCh:
			cfgs = append(cfgs, cfg)
			for !done {
				select {
				case cfg = <-s.channelCfgCh:
					cfgs = append(cfgs, cfg)
				default:
					done = true
				}
				if len(cfgs) >= maxSizeBatch {
					break
				}
			}
			err := s.handleChannelClusterConfigSaves(cfgs)
			if err != nil {
				s.Error("handleChannelClusterConfigSaves err", zap.Error(err))
			}
			cfgs = cfgs[:0]
			done = false
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Store) handleChannelClusterConfigSave(cmd *CMD, confVersion uint64) error {
	_, _, configData, err := cmd.DecodeCMDChannelClusterConfigSave()
	if err != nil {
		return err
	}
	channelClusterConfig := wkdb.ChannelClusterConfig{}
	err = channelClusterConfig.Unmarshal(configData)
	if err != nil {
		return err
	}
	channelClusterConfig.ConfVersion = confVersion

	waitC := make(chan error, 1)
	select {
	case s.channelCfgCh <- &channelCfgReq{
		cfg:   channelClusterConfig,
		errCh: waitC,
	}:
	case <-s.stopper.ShouldStop():
		return nil
	}

	select {
	case err := <-waitC:
		return err
	case <-s.stopper.ShouldStop():
		return nil
	}
}

func (s *Store) handleAddSubscribers(cmd *CMD) error {
	channelId, channelType, members, err := cmd.DecodeMembers()
	if err != nil {
		s.Error("decode subscribers err", zap.Error(err), zap.String("channelID", channelId), zap.Uint8("channelType", channelType), zap.ByteString("data", cmd.Data))
		return err
	}
	return s.wdb.AddSubscribers(channelId, channelType, members)
}

func (s *Store) handleRemoveSubscribers(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeChannelUids()
	if err != nil {
		return err
	}
	return s.wdb.RemoveSubscribers(channelId, channelType, subscribers)
}

func (s *Store) handleAddUser(cmd *CMD) error {
	u, err := cmd.DecodeCMDUser()
	if err != nil {
		return err
	}
	return s.wdb.AddUser(u)
}

func (s *Store) handleUpdateUser(cmd *CMD) error {
	u, err := cmd.DecodeCMDUser()
	if err != nil {
		return err
	}
	return s.wdb.UpdateUser(u)
}

func (s *Store) handleAddDevice(cmd *CMD) error {
	u, err := cmd.DecodeCMDDevice()
	if err != nil {
		return err
	}
	return s.wdb.AddDevice(u)
}

func (s *Store) handleUpdateDevice(cmd *CMD) error {
	u, err := cmd.DecodeCMDDevice()
	if err != nil {
		return err
	}
	return s.wdb.UpdateDevice(u)
}

func (s *Store) handleAddChannelInfo(cmd *CMD) error {
	channelInfo, err := cmd.DecodeChannelInfo()
	if err != nil {
		return err
	}
	_, err = s.wdb.AddChannel(channelInfo)
	return err
}

func (s *Store) handleUpdateChannel(cmd *CMD) error {
	channelInfo, err := cmd.DecodeChannelInfo()
	if err != nil {
		return err
	}
	err = s.wdb.UpdateChannel(channelInfo)
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
	channelId, channelType, members, err := cmd.DecodeMembers()
	if err != nil {
		return err
	}
	return s.wdb.AddDenylist(channelId, channelType, members)
}

func (s *Store) handleRemoveDenylist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeChannelUids()
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
	channelId, channelType, subscribers, err := cmd.DecodeMembers()
	if err != nil {
		return err
	}
	return s.wdb.AddAllowlist(channelId, channelType, subscribers)
}

func (s *Store) handleRemoveAllowlist(cmd *CMD) error {
	channelId, channelType, subscribers, err := cmd.DecodeChannelUids()
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

func (s *Store) handleAddOrUpdateUserConversations(cmd *CMD) error {
	uid, conversations, err := cmd.DecodeCMDAddOrUpdateUserConversations()
	if err != nil {
		return err
	}
	return s.wdb.AddOrUpdateConversationsWithUser(uid, conversations)
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

func (s *Store) handleChannelClusterConfigSaves(reqs []*channelCfgReq) error {
	if len(reqs) > 10 {
		fmt.Println("handleChannelClusterConfigSaves...", len(reqs))
	}
	cfgs := make([]wkdb.ChannelClusterConfig, 0, len(reqs))
	for _, req := range reqs {
		cfgs = append(cfgs, req.cfg)
	}
	err := s.wdb.SaveChannelClusterConfigs(cfgs)
	if err != nil {
		s.Error("save channel cluster config err", zap.Error(err))
	}
	for _, req := range reqs {
		req.errCh <- err
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
			err = s.wdb.AddOrUpdateConversationsWithUser(uid, []wkdb.Conversation{conversation})
			if err != nil {
				return err
			}
		}

	}
	return nil
}

func (s *Store) handleSystemUIDsAdd(cmd *CMD) error {
	uids, err := cmd.DecodeCMDSystemUIDs()
	if err != nil {
		return err
	}
	return s.wdb.AddSystemUids(uids)
}

func (s *Store) handleSystemUIDsRemove(cmd *CMD) error {
	uids, err := cmd.DecodeCMDSystemUIDs()
	if err != nil {
		return err
	}
	return s.wdb.RemoveSystemUids(uids)
}

func (s *Store) handleAddStreamMeta(cmd *CMD) error {
	streamMeta, err := cmd.DecodeCMDAddStreamMeta()
	if err != nil {
		return err
	}
	return s.wdb.AddStreamMeta(streamMeta)
}

func (s *Store) handleAddStreams(cmd *CMD) error {
	streams, err := cmd.DecodeCMDAddStreams()
	if err != nil {
		return err
	}
	return s.wdb.AddStreams(streams)
}

func (s *Store) handleAddOrUpdateConversations(cmd *CMD) error {
	conversations, err := cmd.DecodeCMDAddOrUpdateConversations()
	if err != nil {
		return err
	}
	return s.wdb.AddOrUpdateConversations(conversations)
}

func (s *Store) handleAddOrUpdateTester(cmd *CMD) error {
	tester, err := cmd.DecodeCMDAddOrUpdateTester()
	if err != nil {
		return err
	}
	return s.wdb.AddOrUpdateTester(tester)
}

func (s *Store) handleRemoveTester(cmd *CMD) error {
	no, err := cmd.DecodeCMDRemoveTester()
	if err != nil {
		return err
	}
	return s.wdb.RemoveTester(no)
}
