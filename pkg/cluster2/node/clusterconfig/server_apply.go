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

	return s.config.saveConfig()
}

func (s *Server) handleCmd(cmd *CMD) error {
	switch cmd.CmdType {
	case CMDTypeConfigChange: // 配置改变
		return s.handleConfigChange(cmd)
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
