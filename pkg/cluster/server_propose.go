package cluster

import (
	"fmt"
	"hash/crc32"

	"go.uber.org/zap"
)

func (s *Server) ProposeToSlot(slotID uint32, data []byte) error {

	// 获取slot对象
	slotLeaderID := s.clusterEventManager.GetSlotLeaderID(slotID)
	if slotLeaderID == 0 {
		return fmt.Errorf("slot[%d] leader is not found", slotID)
	}
	if slotLeaderID != s.opts.NodeID {
		s.Debug("the node is not leader of slot,forward request", zap.Uint32("slotID", slotID), zap.Uint64("leaderID", slotLeaderID), zap.Uint64("nodeID", s.opts.NodeID))
		return s.nodeManager.requestSlotPropse(s.cancelCtx, slotLeaderID, &SlotProposeRequest{
			SlotID: slotID,
			Data:   data,
		})
	}

	slot := s.slotManager.GetSlot(slotID)
	if slot == nil {
		return fmt.Errorf("ProposeToSlot: slot[%d] not found", slotID)
	}
	// 提议数据
	err := slot.Propose(data)
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) ProposeMetaToChannel(channelID string, channelType uint8, data []byte) error {

	// 获取channel对象
	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		return err
	}
	if channel == nil {
		return fmt.Errorf("channel[%s:%d] not found", channelID, channelType)
	}
	if channel.LeaderID() != s.opts.NodeID { // 如果当前节点不是领导节点，则转发给领导节点
		s.Error("the node is not leader of channel,forward request", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("leaderID", channel.LeaderID()))
		return s.nodeManager.requestChannelMetaPropose(s.cancelCtx, channel.LeaderID(), &ChannelProposeRequest{
			ChannelID:   channelID,
			ChannelType: channelType,
			Data:        data,
		})
	}
	// 提议数据
	err = channel.ProposeMeta(data)
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) ProposeMessageToChannel(channelID string, channelType uint8, data []byte) error {
	s.Debug("提交消息提案到指定频道", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Int("dataSize", len(data)))
	// 获取channel对象
	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		return err
	}
	if channel == nil {
		return fmt.Errorf("channel[%s:%d] not found", channelID, channelType)
	}
	if channel.LeaderID() != s.opts.NodeID { // 如果当前节点不是领导节点，则转发给领导节点
		s.Error("ProposeMessageToChannel: the node is not leader of channel,forward request", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("leaderID", channel.LeaderID()))
		return s.nodeManager.requestChannelMessagePropsoe(s.cancelCtx, channel.LeaderID(), &ChannelProposeRequest{
			ChannelID:   channelID,
			ChannelType: channelType,
			Data:        data,
		})
	}
	// 提议数据
	_, err = channel.ProposeMessage(data)
	if err != nil {
		return err
	}
	return nil
}

// 提案频道信息
func (s *Server) ProposeChannelClusterInfo(channelInfo *ChannelClusterInfo) error {
	slotID := s.GetSlotID(channelInfo.ChannelID)
	channelInfoData, err := channelInfo.Marshal()
	if err != nil {
		return err
	}
	cmd := &CMD{
		CmdType: CmdTypeSetChannelInfo,
		Data:    channelInfoData,
	}
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.ProposeToSlot(slotID, cmdData)
}

// 获取频道信息
func (s *Server) GetChannelClusterInfo(channelID string, channelType uint8) (*ChannelClusterInfo, error) {
	return s.stateMachine.getChannelClusterInfo(channelID, channelType)
}

func (s *Server) GetSlotID(channelID string) uint32 {
	slotCount := s.clusterEventManager.GetSlotCount()
	value := crc32.ChecksumIEEE([]byte(channelID))
	return value % slotCount
}
