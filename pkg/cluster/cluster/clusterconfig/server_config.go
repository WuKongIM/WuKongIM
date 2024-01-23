package clusterconfig

import "github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"

func (s *Server) AddOrUpdateNodes(nodes []*pb.Node) error {
	err := s.configManager.AddOrUpdateNodes(nodes)
	if err != nil {
		return err
	}
	return s.ProposeConfigChange()
}
