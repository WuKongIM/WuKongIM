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
