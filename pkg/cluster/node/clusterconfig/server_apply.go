package clusterconfig

import (
	"encoding/binary"

	pb "github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) applyLogs(logs []types.Log) error {
	for _, log := range logs {
		err := s.applyLog(log)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) applyLog(log types.Log) error {
	cmd := &CMD{}
	err := cmd.Unmarshal(log.Data)
	if err != nil {
		s.Error("unmarshal cmd err", zap.Error(err), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
		return err
	}

	err = s.handleCmd(cmd)
	if err != nil {
		s.Panic("handle cmd failed", zap.Error(err))
		return err
	}
	s.config.cfg.Term = log.Term
	s.config.cfg.Version = log.Index

	err = s.config.saveConfig()
	if err != nil {
		s.Error("save config err", zap.Error(err))
		return err
	}
	// 配置发送变化
	s.NotifyConfigChangeEvent()
	return nil
}

func (s *Server) handleCmd(cmd *CMD) error {
	switch cmd.CmdType {
	case CMDTypeConfigChange: // 配置改变
		return s.handleConfigChange(cmd)
	case CMDTypeConfigApiServerAddrChange: // 节点api server地址改变
		return s.handleApiServerAddrChange(cmd)
	case CMDTypeNodeOnlineStatusChange: // 节点在线状态改变
		return s.handleNodeOnlineStatusChange(cmd)
	case CMDTypeSlotUpdate: // 槽更新
		return s.handleSlotUpdate(cmd)
	case CMDTypeNodeJoin: // 节点加入
		return s.handleNodeJoin(cmd)
	case CMDTypeNodeJoining: // 节点加入中
		return s.handleNodeJoining(cmd)
	case CMDTypeNodeJoined: // 节点加入完成
		return s.handleNodeJoined(cmd)
	}
	return nil
}

func (s *Server) handleConfigChange(cmd *CMD) error {
	cfg := &pb.Config{}
	err := cfg.Unmarshal(cmd.Data)
	if err != nil {
		s.Error("unmarshal config err", zap.Error(err))
		return err
	}
	s.config.update(cfg)
	return nil
}

func (s *Server) handleApiServerAddrChange(cmd *CMD) error {
	nodeId, apiServerAddr, err := DecodeApiServerAddrChange(cmd.Data)
	if err != nil {
		s.Error("decode api server addr change err", zap.Error(err))
		return err
	}

	s.config.updateApiServerAddr(nodeId, apiServerAddr)
	return nil
}

func (s *Server) handleNodeOnlineStatusChange(cmd *CMD) error {
	nodeId, online, err := DecodeNodeOnlineStatusChange(cmd.Data)
	if err != nil {
		s.Error("decode node online status change err", zap.Error(err))
		return err
	}

	s.config.updateNodeOnlineStatus(nodeId, online)
	return nil
}

func (s *Server) handleSlotUpdate(cmd *CMD) error {
	slotset := pb.SlotSet{}
	err := slotset.Unmarshal(cmd.Data)
	if err != nil {
		s.Error("unmarshal slotset err", zap.Error(err))
		return err
	}
	s.config.updateSlots(slotset)

	return nil
}

func (s *Server) handleNodeJoin(cmd *CMD) error {

	newNode := &pb.Node{}
	err := newNode.Unmarshal(cmd.Data)
	if err != nil {
		s.Error("unmarshal node err", zap.Error(err))
		return err
	}
	s.config.addOrUpdateNode(newNode)

	// 将新节点加入学习者列表
	if !wkutil.ArrayContainsUint64(s.config.cfg.Learners, newNode.Id) {
		s.config.cfg.Learners = append(s.config.cfg.Learners, newNode.Id)
		// 如果是新加入的节点，就是从自己迁移到自己
		s.config.cfg.MigrateFrom = newNode.Id
		s.config.cfg.MigrateTo = newNode.Id
	}
	s.switchConfig(s.config)
	return nil
}

func (s *Server) handleNodeJoining(cmd *CMD) error {
	nodeId := binary.BigEndian.Uint64(cmd.Data)
	s.config.updateNodeJoining(nodeId)
	s.switchConfig(s.config)
	return nil
}

func (s *Server) handleNodeJoined(cmd *CMD) error {
	nodeId, slots, err := DecodeNodeJoined(cmd.Data)
	if err != nil {
		s.Error("decode node joined err", zap.Error(err))
		return err
	}
	s.config.updateNodeJoined(nodeId, slots)
	s.switchConfig(s.config)
	return nil
}
