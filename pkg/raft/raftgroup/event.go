package raftgroup

type IEvent interface {
	// OnAddRaft 添加raft
	OnAddRaft(r IRaft)

	// OnRemoveRaft 移除raft
	OnRemoveRaft(r IRaft)
}
