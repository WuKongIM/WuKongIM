package clusterconfig

import (
	"encoding/binary"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) apply(logs []replica.Log) error {
	for _, log := range logs {
		err := s.applyLog(log)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) applyLog(log replica.Log) error {
	cmd := &CMD{}
	err := cmd.Unmarshal(log.Data)
	if err != nil {
		s.Error("unmarshal cmd err", zap.Error(err), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
		return err
	}

	start := time.Now()
	defer func() {
		end := time.Since(start)
		if end > time.Millisecond*100 {
			s.Info("apply log", zap.Duration("cost", end), zap.Uint64("nodeId", s.opts.NodeId), zap.String("cmd", cmd.CmdType.String()), zap.Uint64("index", log.Index))
		}

	}()

	err = s.handleCmd(cmd)
	if err != nil {
		s.Error("handle cmd failed", zap.Error(err))
		return err
	}

	s.cfg.cfg.Term = log.Term
	s.cfg.cfg.Version = log.Index

	return s.cfg.saveConfig()
}

func (s *Server) handleCmd(cmd *CMD) error {
	switch cmd.CmdType {
	case CMDTypeConfigInit: // 配置初始化
		return s.handleConfigInit(cmd)
	case CMDTypeConfigApiServerAddrChange: // 节点api server地址变更
		return s.handleApiServerAddrChange(cmd)
	case CMDTypeNodeJoin: // 节点加入
		return s.handleNodeJoin(cmd)
	case CMDTypeNodeJoining: // 节点加入中
		return s.handleNodeJoining(cmd)
	case CMDTypeNodeJoined: // 节点加入完成
		return s.handleNodeJoined(cmd)
	case CMDTypeNodeOnlineStatusChange: // 节点在线状态改变
		return s.handleNodeOnlineStatusChange(cmd)
	case CMDTypeSlotMigrate: // 槽迁移
		return s.handleSlotMigrate(cmd)
	case CMDTypeSlotUpdate: // 槽更新
		return s.handleSlotUpdate(cmd)
	case CMDTypeNodeStatusChange: // 节点状态改变
		return s.handleNodeStatusChange(cmd)
	}
	return nil
}

func (s *Server) handleConfigInit(cmd *CMD) error {
	cfg := &pb.Config{}
	err := cfg.Unmarshal(cmd.Data)
	if err != nil {
		s.Error("unmarshal config err", zap.Error(err))
		return err
	}

	s.cfg.update(cfg)

	return nil
}

func (s *Server) handleApiServerAddrChange(cmd *CMD) error {
	nodeId, apiServerAddr, err := DecodeApiServerAddrChange(cmd.Data)
	if err != nil {
		s.Error("decode api server addr change err", zap.Error(err))
		return err
	}

	s.cfg.updateApiServerAddr(nodeId, apiServerAddr)
	return nil
}

func (s *Server) handleNodeJoin(cmd *CMD) error {

	newNode := &pb.Node{}
	err := newNode.Unmarshal(cmd.Data)
	if err != nil {
		s.Error("unmarshal node err", zap.Error(err))
		return err
	}
	s.cfg.addOrUpdateNode(newNode)

	// 将新节点加入学习者列表
	if !wkutil.ArrayContainsUint64(s.cfg.cfg.Learners, newNode.Id) {
		s.cfg.cfg.Learners = append(s.cfg.cfg.Learners, newNode.Id)
		// 如果是新加入的节点，就是从自己迁移到自己
		s.cfg.cfg.MigrateFrom = newNode.Id
		s.cfg.cfg.MigrateTo = newNode.Id
	}

	return s.SwitchConfig(s.cfg.cfg)

}

func (s *Server) handleNodeJoining(cmd *CMD) error {
	nodeId := binary.BigEndian.Uint64(cmd.Data)
	s.cfg.updateNodeJoining(nodeId)
	return s.SwitchConfig(s.cfg.cfg)
}

func (s *Server) handleNodeJoined(cmd *CMD) error {
	nodeId, slots, err := DecodeNodeJoined(cmd.Data)
	if err != nil {
		s.Error("decode node joined err", zap.Error(err))
		return err
	}
	s.cfg.updateNodeJoined(nodeId, slots)
	return s.SwitchConfig(s.cfg.cfg)
}

func (s *Server) handleNodeOnlineStatusChange(cmd *CMD) error {
	nodeId, online, err := DecodeNodeOnlineStatusChange(cmd.Data)
	if err != nil {
		s.Error("decode node online status change err", zap.Error(err))
		return err
	}

	s.cfg.updateNodeOnlineStatus(nodeId, online)
	return nil
}

func (s *Server) handleSlotMigrate(cmd *CMD) error {

	slotId, fromNodeId, toNodeId, err := DecodeMigrateSlot(cmd.Data)
	if err != nil {
		s.Error("decode migrate slot failed", zap.Error(err))
		return err
	}

	s.cfg.updateSlotMigrate(slotId, fromNodeId, toNodeId)
	return s.SwitchConfig(s.cfg.cfg)
}

func (s *Server) handleSlotUpdate(cmd *CMD) error {
	slotset := pb.SlotSet{}
	err := slotset.Unmarshal(cmd.Data)
	if err != nil {
		s.Error("unmarshal slotset err", zap.Error(err))
		return err
	}
	s.cfg.updateSlots(slotset)

	return nil
}

func (s *Server) handleNodeStatusChange(cmd *CMD) error {
	nodeId, status, err := DecodeNodeStatusChange(cmd.Data)
	if err != nil {
		s.Error("decode node status change err", zap.Error(err))
		return err
	}

	s.cfg.updateNodeStatus(nodeId, status)
	return nil
}
