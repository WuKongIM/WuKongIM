package cluster

import "github.com/WuKongIM/WuKongIM/pkg/transport"

type StaticDiscovery struct {
	nodes map[uint64]NodeInfo
}

func NewStaticDiscovery(configs []NodeConfig) *StaticDiscovery {
	nodes := make(map[uint64]NodeInfo, len(configs))
	for _, c := range configs {
		nodes[uint64(c.NodeID)] = NodeInfo{NodeID: c.NodeID, Addr: c.Addr}
	}
	return &StaticDiscovery{nodes: nodes}
}

func (s *StaticDiscovery) GetNodes() []NodeInfo {
	out := make([]NodeInfo, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	return out
}

func (s *StaticDiscovery) Resolve(nodeID uint64) (string, error) {
	n, ok := s.nodes[nodeID]
	if !ok {
		return "", transport.ErrNodeNotFound
	}
	return n.Addr, nil
}

func (s *StaticDiscovery) Stop() {}
