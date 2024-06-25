package clusterconfig

import (
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"go.uber.org/zap"
)

// ProposeConfigInit 提议配置初始化
func (s *Server) ProposeConfigInit(cfg *pb.Config) error {

	data, err := cfg.Marshal()
	if err != nil {
		return err
	}

	cmd := NewCMD(CMDTypeConfigInit, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}

	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeConfigInit failed", zap.Error(err))
		return err
	}

	return nil
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

	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeApiServerAddr failed", zap.Error(err))
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

	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeJoin failed", zap.Error(err))
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

	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeJoining failed", zap.Error(err))
		return err
	}
	return nil

}

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

	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeJoined failed", zap.Error(err))
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

	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeNodeOnlineStatus failed", zap.Error(err))
		return err
	}

	return nil
}

// 提案槽迁移
func (s *Server) ProposeMigrateSlot(slotId uint32, fromNodeId, toNodeId uint64) error {
	data, err := EncodeMigrateSlot(slotId, fromNodeId, toNodeId)
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDTypeSlotMigrate, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}
	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeMigrateSlot failed", zap.Error(err))
		return err
	}

	return nil
}

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

	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeSlots failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) ProposeNodeStatus(nodeId uint64, status pb.NodeStatus) error {
	data, err := EncodeNodeStatusChange(nodeId, status)
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDTypeNodeStatusChange, data)
	cmdBytes, err := cmd.Marshal()
	if err != nil {
		return err
	}
	err = s.proposeAndWait([]replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: cmdBytes,
		},
	})
	if err != nil {
		s.Error("ProposeNodeStatus failed", zap.Error(err))
		return err
	}
	return nil
}
