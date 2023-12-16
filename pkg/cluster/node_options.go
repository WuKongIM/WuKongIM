package cluster

// type NodeOptions struct {
// 	ID         NodeID            // 节点ID
// 	Addr       string            // 节点地址 例如： ip:port
// 	ServerAddr string            // 服务地址 例如： ip:port 节点之间可以互相访问的地址
// 	Join       string            // 加入集群的节点地址 例如： nodeID@ip:port
// 	DataDir    string            // 数据目录
// 	InitNodes  map[NodeID]string // 初始化的节点 key为节点ID value为raft通讯地址
// }

// func NewNodeOptions() *NodeOptions {
// 	return &NodeOptions{
// 		Addr: "0.0.0.0:11000",
// 	}
// }

// func WithInitNodes(initNodes map[NodeID]string) NodeOption {
// 	return func(opts *NodeOptions) {
// 		opts.InitNodes = initNodes
// 	}
// }

// type NodeOption func(*NodeOptions)

type NodeStartOptions struct {
	Join bool // 是否加入集群
}

type NodeStartOption func(*NodeStartOptions)

func NodeStartWithJoin(join bool) NodeStartOption {
	return func(opts *NodeStartOptions) {
		opts.Join = join
	}
}
