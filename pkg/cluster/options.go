package cluster

type Options struct {
	NodeID      uint64   // 节点ID
	ListenAddr  string   // gossip监听地址 格式：ip:port
	Seed        []string // 种子节点 格式：ip:port
	offsetPort  int      // 节点端口偏移量
	clusterAddr string   // 集群数据通讯地址 格式：ip:port
}

func NewOptions() *Options {
	return &Options{
		NodeID:     1,
		ListenAddr: "0.0.0.0:10001",
		offsetPort: 1000,
	}
}

type Option func(*Options)
