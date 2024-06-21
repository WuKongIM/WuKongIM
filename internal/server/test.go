package server

import (
	"testing"

	"github.com/spf13/viper"
)

func NewTestServer(t *testing.T, opt ...Option) *Server {

	optList := make([]Option, 0)
	optList = append(
		optList,
		WithRootDir(t.TempDir()),
		WithLoggerDir(t.TempDir()),
		WithDbShardNum(2),
		WithClusterNodeId(1001),
		WithClusterSlotCount(5),
		WithClusterSlotReplicaCount(1),
		WithClusterChannelReplicaCount(1),
	)
	optList = append(optList, opt...)

	opts := NewOptions(optList...)

	vp := viper.New()
	opts.ConfigureWithViper(vp)
	s := New(opts)

	return s
}

// 创建一个二个节点的分布式服务
func NewTestClusterServerTwoNode(t *testing.T) (*Server, *Server) {

	nodes := make([]*Node, 0)

	nodes = append(nodes, &Node{
		Id:         1001,
		ServerAddr: "0.0.0.0:11110",
	})

	nodes = append(nodes, &Node{
		Id:         1002,
		ServerAddr: "0.0.0.0:11111",
	})

	s1 := NewTestServer(t, WithDemoOn(false), WithWSAddr("ws://0.0.0.0:5210"), WithMonitorAddr("0.0.0.0:5310"), WithAddr("tcp://0.0.0.0:5110"), WithHTTPAddr("0.0.0.0:5001"), WithClusterAddr("tcp://0.0.0.0:11110"), WithClusterNodeId(1001), WithClusterNodes(nodes))
	s2 := NewTestServer(t, WithDemoOn(false), WithWSAddr("ws://0.0.0.0:5220"), WithMonitorAddr("0.0.0.0:5320"), WithAddr("tcp://0.0.0.0:5120"), WithHTTPAddr("0.0.0.0:5002"), WithClusterAddr("tcp://0.0.0.0:11111"), WithClusterNodeId(1002), WithClusterNodes(nodes))

	return s1, s2
}

func MustWaitClusterReady(ss ...*Server) {
	for _, s := range ss {
		s.MustWaitClusterReady()
	}
}

// 获取领导者节点服务
func GetLeaderServer(ss ...*Server) *Server {
	for _, s := range ss {
		if s.cluster.LeaderId() == s.opts.Cluster.NodeId {
			return s
		}
	}
	return nil
}
