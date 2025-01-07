package clusterconfig

import (
	pb "github.com/WuKongIM/WuKongIM/pkg/cluster2/node/types"
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
