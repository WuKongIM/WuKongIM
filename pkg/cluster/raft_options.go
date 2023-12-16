package cluster

type RaftOptions struct {
	ID           NodeID // 节点ID
	Addr         string // 节点地址 例如： ip:port
	ServerAddr   string
	DataDir      string
	Join         string            // 加入集群的节点地址 例如： nodeID@ip:port
	ShardID      uint32            // 分区ID
	InitNodes    map[NodeID]string // 初始化的节点 key为节点ID value为raft通讯地址
	NodeRegistry INodeRegistry
}

func NewRaftOptions() *RaftOptions {
	return &RaftOptions{
		ShardID:    PeerShardID,
		Addr:       "0.0.0.0:11000",
		ServerAddr: "0.0.0.0:11000",
	}
}

type RaftOption func(*RaftOptions)
