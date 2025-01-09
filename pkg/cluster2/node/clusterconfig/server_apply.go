package clusterconfig

import (
	pb "github.com/WuKongIM/WuKongIM/pkg/cluster2/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
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
