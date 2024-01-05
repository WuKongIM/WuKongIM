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
		s.Warn("the node is not leader of slot,forward request", zap.Uint32("slotID", slotID), zap.Uint64("leaderID", slotLeaderID), zap.Uint64("nodeID", s.opts.NodeID))

		return s.nodeManager.requestSlotPropse(s.cancelCtx, slotLeaderID, &ProposeRequest{
			SlotID: slotID,
			Data:   data,
		})
	}

	slot := s.slotManager.GetSlot(slotID)
	if slot == nil {
		return fmt.Errorf("slot[%d] not found", slotID)
	}
	// 提议数据
	err := slot.Propose(data)
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) ProposeToChannel(channelID string, channelType uint8, data []byte) error {
	// 获取channel对象
	channel := s.channelManager.GetChannel(channelID, channelType)
	if channel == nil {
		return fmt.Errorf("channel[%s:%d] not found", channelID, channelType)
	}
	// 提议数据
	err := channel.Propose(data)
	if err != nil {
		return err
	}
	return nil
}

// 提案频道信息
func (s *Server) ProposeChannelInfo(channelInfo *ChannelInfo) error {
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
func (s *Server) GetChannelInfo(channelID string, channelType uint8) (*ChannelInfo, error) {
	slotID := s.GetSlotID(channelID)
	return s.slotManager.slotStateMachine.getChannelInfo(slotID, channelID, channelType)
}

func (s *Server) GetSlotID(channelID string) uint32 {
	slotCount := s.clusterEventManager.GetSlotCount()
	value := crc32.ChecksumIEEE([]byte(channelID))
	return value % slotCount
}
