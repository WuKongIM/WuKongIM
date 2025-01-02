package raft

type Config struct {
	MigrateFrom uint64   // 迁移源节点
	MigrateTo   uint64   // 迁移目标节点
	Replicas    []uint64 // 副本集合（不包含节点自己）
	Learners    []uint64 // 学习节点集合
	Role        Role     // 节点角色
	Term        uint32   // 领导任期
	Version     uint64   // 配置版本

	// 不参与编码
	Leader uint64 // 领导ID
}

type stepReq struct {
	event Event
	resp  chan error
}

// 任期对应的开始日志下标
type TermStartIndex struct {
	Term  uint32
	Index uint64
}

type AppendLogsReq struct {
	// Logs 需要追加的日志集合
	Logs []Log
	// TermStartIndex 任期对应的开始日志下标，如果不为空，则需要保存
	TermStartIndex *TermStartIndex
}
