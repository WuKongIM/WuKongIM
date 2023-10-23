
```go


// ---------- 创建多组raft ------------
opts := multiraft.NewOptions()
opts1.Peers = []multiraft.Peer{
	multiraft.NewPeer(1001, "tcp://127.0.0.1:11000"),
	multiraft.NewPeer(1002, "tcp://127.0.0.1:12000"),
}
opts.Transport = multiraft.NewTransport() // 传输层
opts.StateMachine = NewStateMachine() // 状态机
opts.ReplicaStorage = NewReplicaStroage() // 副本数据存储

s := multiraft.New(opts) // 创建多组raft

// ---------- 创建副本 ------------
replicaOptions := multiraft.NewReplicaOptions()
replicaOptions.PeerID = 1001 // 当前节点ID 
replicaOptions.MaxReplicaCount = 3 // 最大副本数数量（包含领导节点）
replicaID := 1 // 副本ID
// start replica
replica := s.StartReplica(replicaID, replicaOptions)

// ---------- 提交数据 ------------
// propose
replica.Propose([]byte("hello world"))


// ---------- 其他操作 ------------
// stop replica
replica.Stop()

// get replica
replica = s.GetReplica(replicaID)

s.AddPeer(multiraft.NewPeer(1003, "tcp://127.0.0.1:13000")) // 添加节点
s.RemovePeer(1003) // 删除节点

// change replica count
s.ChangeReplicaCount(replicaID,4) // 改变指定副本ID的副本数量

s.TransferLeader(replicaID,1002) // 转换指定副本ID的领导节点

s.MoveReplica(replicaID,1002,1003) // 将副本从一个节点移动到另外一个节点

s.Propose(replicaID, data) // 提交数据

```