package clusterconfig

import (
	"encoding/binary"

	pb "github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"go.uber.org/zap"
)

// 提案配置
func (s *Server) ProposeConfig(cfg *pb.Config) error {
	data, err := cfg.Marshal()
	if err != nil {
		return err
	}

	cmd := NewCMD(CMDTypeConfigChange, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}
	_, err = s.ProposeUntilApplied(s.genConfigId(), cmdBytes)
	return err
}

// ProposeApiServerAddr 提案节点api server地址变更
func (s *Server) ProposeApiServerAddr(nodeId uint64, apiServerAddr string) error {

	data, err := EncodeApiServerAddrChange(nodeId, apiServerAddr)
	if err != nil {
		return err
	}

	cmd := NewCMD(CMDTypeConfigApiServerAddrChange, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}

	_, err = s.ProposeUntilApplied(s.genConfigId(), cmdBytes)
	if err != nil {
		s.Error("ProposeApiServerAddr failed", zap.Error(err))
		return err
	}
	return nil
}

// ProposeLeave 提案节点在线状态变更
func (s *Server) ProposeNodeOnlineStatus(nodeId uint64, online bool) error {
	data, err := EncodeNodeOnlineStatusChange(nodeId, online)
	if err != nil {
		return err
	}

	cmd := NewCMD(CMDTypeNodeOnlineStatusChange, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}
	_, err = s.ProposeUntilApplied(s.genConfigId(), cmdBytes)
	if err != nil {
		s.Error("ProposeNodeOnlineStatus failed", zap.Error(err))
		return err
	}

	return nil
}

// ProposeSlots 提案槽变更
func (s *Server) ProposeSlots(slots []*pb.Slot) error {
	slotSet := pb.SlotSet(slots)
	data, err := slotSet.Marshal()
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDTypeSlotUpdate, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}

	logId := s.genConfigId()

	_, err = s.ProposeUntilApplied(logId, cmdBytes)
	if err != nil {
		s.Error("ProposeSlots failed", zap.Error(err))
		return err
	}
	return nil
}

// ProposeJoined 提案节点加入
func (s *Server) ProposeJoined(nodeId uint64, slots []*pb.Slot) error {

	data, err := EncodeNodeJoined(nodeId, slots)
	if err != nil {
		return err
	}

	cmd := NewCMD(CMDTypeNodeJoined, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}

	logId := s.genConfigId()
	_, err = s.ProposeUntilApplied(logId, cmdBytes)
	if err != nil {
		s.Error("ProposeJoined failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) ProposeJoining(nodeId uint64) error {

	nodeIdBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nodeIdBytes, nodeId)

	cmd := NewCMD(CMDTypeNodeJoining, nodeIdBytes)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		s.Error("ProposeJoining cmd marshal failed", zap.Error(err))
		return err
	}

	logId := s.genConfigId()
	_, err = s.ProposeUntilApplied(logId, cmdBytes)
	if err != nil {
		s.Error("ProposeJoining failed", zap.Error(err))
		return err
	}
	return nil

}

// ProposeJoin 提案节点加入
func (s *Server) ProposeJoin(node *pb.Node) error {

	data, err := node.Marshal()
	if err != nil {
		return err
	}

	cmd := NewCMD(CMDTypeNodeJoin, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}

	logId := s.genConfigId()
	_, err = s.ProposeUntilApplied(logId, cmdBytes)
	if err != nil {
		s.Error("ProposeJoin failed", zap.Error(err))
		return err
	}
	return nil
}
