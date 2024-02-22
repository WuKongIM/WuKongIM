package clusterconfig

import "github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"

func (s *Server) AddOrUpdateNodes(nodes []*pb.Node) error {
	if !s.node.isLeader() {
		return ErrNotLeader
	}
	newCfg := s.configManager.GetConfig().Clone()
	s.configManager.AddOrUpdateNodes(nodes, newCfg)
	return s.ProposeConfigChange(s.configManager.GetConfigDataByCfg(newCfg))
}

// func (s *Server) AddOrUpdateSlots(slots []*pb.Slot) error {
// 	if !s.node.isLeader() {
// 		return ErrNotLeader
// 	}
// 	newCfg := s.configManager.GetConfig().Clone()
// 	newCfg.Version++
// 	s.configManager.AddOrUpdateSlots(slots, newCfg)

// 	return s.ProposeConfigChange(newCfg.Version, s.configManager.GetConfigDataByCfg(newCfg))
// }
