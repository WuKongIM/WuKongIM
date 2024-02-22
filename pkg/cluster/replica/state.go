package replica

type State struct {
	committedIndex       uint64 // 已提交的日志下标
	appliedIndex         uint64 // 已应用的日志下标
	lastLogIndex         uint64 // 最后一条日志下标
	leaderLastLogIndex   uint64 // 领导者最后一条日志下标
	leaderCommittedIndex uint64 // 领导者已提交的日志下标
	term                 uint32 // 当前任期
	firstSyncResp        bool   // 是否完成第一次同步
}

func (s State) CommittedIndex() uint64 {
	return s.committedIndex
}

func (s State) AppliedIndex() uint64 {
	return s.appliedIndex
}

func (s State) LastLogIndex() uint64 {
	return s.lastLogIndex
}

func (s State) Term() uint32 {
	return s.term
}

func (s State) LeaderLastLogIndex() uint64 {
	return s.leaderLastLogIndex
}

func (s State) LeaderCommittedIndex() uint64 {
	return s.leaderCommittedIndex
}

func (s State) FirstSyncResp() bool {
	return s.firstSyncResp
}
