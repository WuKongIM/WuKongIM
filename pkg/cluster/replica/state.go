package replica

// type State struct {
// 	committedIndex uint64 // 已提交的日志下标

// 	lastLogIndex         uint64 // 最后一条日志下标
// 	leaderLastLogIndex   uint64 // 领导者最后一条日志下标
// 	leaderCommittedIndex uint64 // 领导者已提交的日志下标
// 	term                 uint32 // 当前任期
// 	firstSyncResp        bool   // 是否完成第一次同步

// 	// applying is the highest log position that the application has
// 	// been instructed to apply to its state machine. Some of these
// 	// entries may be in the process of applying and have not yet
// 	// reached applied.
// 	// Use: The field is incremented when accepting a Ready struct.
// 	// Invariant: applied <= applying && applying <= committed
// 	applyingIndex uint64
// 	// applied is the highest log position that the application has
// 	// successfully applied to its state machine.
// 	// Use: The field is incremented when advancing after the committed
// 	// entries in a Ready struct have been applied (either synchronously
// 	// or asynchronously).
// 	// Invariant: applied <= committed
// 	appliedIndex uint64 // 已应用的日志下标

// 	// maxApplyingLogsSize limits the outstanding byte size of the messages
// 	// returned from calls to nextCommittedLogs that have not been acknowledged
// 	// by a call to appliedTo.
// 	maxApplyingLogsSize logEncodingSize

// 	// applyingLogsSize is the current outstanding byte size of the messages
// 	// returned from calls to nextCommittedLogs that have not been acknowledged
// 	// by a call to appliedTo.
// 	applyingLogsSize logEncodingSize

// 	// applyingLogsPaused is true when log application has been paused until
// 	// enough progress is acknowledged.
// 	applyingLogsPaused bool
// }

func (r *Replica) CommittedIndex() uint64 {
	return r.replicaLog.committedIndex
}

func (r *Replica) AppliedIndex() uint64 {
	return r.replicaLog.appliedIndex
}

func (r *Replica) LastLogIndex() uint64 {
	return r.replicaLog.lastIndex()
}

func (r *Replica) Term() uint32 {
	return r.replicaLog.term
}

func (r *Replica) LeaderLastLogIndex() uint64 {
	return r.replicaLog.leaderLastLogIndex
}

func (r *Replica) LeaderCommittedIndex() uint64 {
	return r.replicaLog.leaderCommittedIndex
}

// func (r *Replica) FirstSyncResp() bool {
// 	return r.replicaLog.firstSyncResp
// }
