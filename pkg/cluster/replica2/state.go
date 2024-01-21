package replica

type State struct {
	committedIndex uint64 // 已提交的日志下标
	appliedIndex   uint64 // 已应用的日志下标
	lastLogIndex   uint64 // 最后一条日志下标
	term           uint32 // 当前任期
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
