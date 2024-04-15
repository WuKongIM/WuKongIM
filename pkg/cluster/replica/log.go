package replica

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type replicaLog struct {
	wklog.Log
	unstable *unstable

	committedIndex       uint64 // 已提交的日志下标
	lastLogIndex         uint64 // 最后一条日志下标
	leaderLastLogIndex   uint64 // 领导者最后一条日志下标
	leaderCommittedIndex uint64 // 领导者已提交的日志下标
	term                 uint32 // 当前任期

	// applying is the highest log position that the application has
	// been instructed to apply to its state machine. Some of these
	// entries may be in the process of applying and have not yet
	// reached applied.
	// Use: The field is incremented when accepting a Ready struct.
	// Invariant: applied <= applying && applying <= committed
	applyingIndex uint64
	// applied is the highest log position that the application has
	// successfully applied to its state machine.
	// Use: The field is incremented when advancing after the committed
	// entries in a Ready struct have been applied (either synchronously
	// or asynchronously).
	// Invariant: applied <= committed
	appliedIndex uint64 // 已应用的日志下标

	// maxApplyingLogsSize limits the outstanding byte size of the messages
	// returned from calls to nextCommittedLogs that have not been acknowledged
	// by a call to appliedTo.
	maxApplyingLogsSize logEncodingSize

	// applyingLogsSize is the current outstanding byte size of the messages
	// returned from calls to nextCommittedLogs that have not been acknowledged
	// by a call to appliedTo.
	applyingLogsSize logEncodingSize

	// applyingLogsPaused is true when log application has been paused until
	// enough progress is acknowledged.
	applyingLogsPaused bool

	// 已追加
	appendedIndex uint64

	opts *Options
}

func newReplicaLog(opts *Options) *replicaLog {
	rg := &replicaLog{
		unstable:            newUnstable(),
		Log:                 wklog.NewWKLog("replica"),
		opts:                opts,
		maxApplyingLogsSize: noLimit,
	}

	lastIndex, err := rg.opts.Storage.LastIndex()
	if err != nil {
		rg.Panic("get last index failed", zap.Error(err))
	}
	lastLeaderTerm, err := rg.opts.Storage.LeaderLastTerm()
	if err != nil {
		rg.Panic("get last leader term failed", zap.Error(err))
	}
	rg.lastLogIndex = lastIndex
	rg.committedIndex = opts.AppliedIndex
	if lastLeaderTerm == 0 {
		rg.term = 1
	} else {
		rg.term = lastLeaderTerm
	}
	rg.unstable.offset = lastIndex + 1
	rg.unstable.offsetInProgress = lastIndex + 1

	rg.appendedIndex = lastIndex

	rg.appliedTo(opts.AppliedIndex, 0)

	return rg
}

// func (r *replicaLog) nextApplyLogs() []Log {

// 	lo, hi := r.applyingIndex+1, r.maxAppliableIndex()+1 // [lo, hi)
// 	if lo >= hi {
// 		// Nothing to apply.
// 		return nil
// 	}
// 	maxSize := r.maxApplyingLogsSize - r.applyingLogsSize
// 	if maxSize <= 0 {
// 		r.Panic(fmt.Sprintf("applying entry size (%d-%d)=%d not positive",
// 			r.maxApplyingLogsSize, r.applyingLogsSize, maxSize))
// 	}

// 	logs, err := r.getLogs(lo, hi, maxSize)
// 	if err != nil {
// 		r.Panic(fmt.Sprintf("unexpected error when getting unapplied entries (%v)", err))
// 	}
// 	return logs
// }

func (r *replicaLog) appendLog(logs ...Log) {
	lastLog := logs[len(logs)-1]
	r.unstable.truncateAndAppend(logs)
	r.lastLogIndex = lastLog.Index
}

func (r *replicaLog) appliedTo(index uint64, size logEncodingSize) {
	oldApplied := r.appliedIndex
	newApplied := max(index, oldApplied)

	if r.committedIndex < newApplied {
		r.Panic(fmt.Sprintf("applied index [%d] is out of range [prevApplied:%d, committed:%d]",
			newApplied, oldApplied, r.committedIndex))
	}
	r.appliedIndex = newApplied
	r.applyingIndex = max(r.applyingIndex, newApplied)

	if r.applyingLogsSize > size {
		r.applyingLogsSize -= size
	} else {
		// Defense against underflow.
		r.applyingLogsSize = 0
	}
	r.applyingLogsPaused = r.applyingLogsSize >= r.maxApplyingLogsSize
}

func (r *replicaLog) acceptApplying(i uint64, size logEncodingSize) {
	if r.committedIndex < i {
		r.Panic(fmt.Sprintf("applying(%d) is out of range [prevApplying(%d), committed(%d)]", i, r.applyingIndex, r.committedIndex))
	}
	r.applyingIndex = i
	r.applyingLogsSize += size
	// Determine whether to pause entry application until some progress is
	// acknowledged. We pause in two cases:
	// 1. the outstanding entry size equals or exceeds the maximum size.
	// 2. the outstanding entry size does not equal or exceed the maximum size,
	//    but we determine that the next entry in the log will push us over the
	//    limit. We determine this by comparing the last entry returned from
	//    raftLog.nextCommittedEnts to the maximum entry that the method was
	//    allowed to return had there been no size limit. If these indexes are
	//    not equal, then the returned entries slice must have been truncated to
	//    adhere to the memory limit.
	r.applyingLogsPaused = r.applyingLogsSize >= r.maxApplyingLogsSize ||
		i < r.maxAppliableIndex()
}

func (r *replicaLog) acceptUnstable() { r.unstable.acceptInProgress() }

func (r *replicaLog) stableTo(i uint64) {
	r.appendedIndex = i
	r.unstable.stableTo(i)
}

// nextUnstableLogs returns all logs that are available to be written to the
// local stable log and are not already in-progress.
func (r *replicaLog) nextUnstableLogs() []Log {
	return r.unstable.nextLogs()
}

// hasNextUnstableLogs returns if there are any logs that are available to be
// written to the local stable log and are not already in-progress.
func (r *replicaLog) hasNextUnstableLogs() bool {
	return r.unstable.hasNextLogs()
}

// 是否有未应用的日志
func (r *replicaLog) hasUnapplyLogs() bool {
	if r.applyingLogsPaused {
		// Log application outstanding size limit reached.
		return false
	}
	if r.appliedIndex >= r.appendedIndex {
		return false
	}

	if r.appendedIndex < r.committedIndex {
		return false
	}

	return r.committedIndex > r.applyingIndex
}

func (r *replicaLog) getLogsFromUnstable(lo, hi uint64, maxSize logEncodingSize) ([]Log, error) {
	if err := r.mustCheckOutOfBounds(lo, hi); err != nil {
		return nil, err
	}
	if lo == hi {
		return nil, nil
	}
	if lo >= r.unstable.offset {
		logs := limitSize(r.unstable.slice(lo, hi), maxSize)
		return logs[:len(logs):len(logs)], nil
	}
	return nil, nil
}

// func (r *replicaLog) getLogs(lo, hi uint64, maxSize logEncodingSize) ([]Log, error) {
// 	if err := r.mustCheckOutOfBounds(lo, hi); err != nil {
// 		return nil, err
// 	}
// 	if lo == hi {
// 		return nil, nil
// 	}
// 	if lo >= r.unstable.offset {
// 		logs := limitSize(r.unstable.slice(lo, hi), maxSize)
// 		return logs[:len(logs):len(logs)], nil
// 	}
// 	cut := min(hi, r.unstable.offset)
// 	logs, err := r.opts.Storage.Logs(lo, cut, uint64(maxSize))
// 	if err != nil {
// 		return nil, err
// 	}
// 	if hi <= r.unstable.offset {
// 		return logs, nil
// 	}

// 	if uint64(len(logs)) < cut-lo {
// 		return logs, nil
// 	}
// 	size := LogsSize(logs)
// 	if size >= maxSize {
// 		return logs, nil
// 	}
// 	unstable := limitSize(r.unstable.slice(r.unstable.offset, hi), maxSize-size)
// 	if len(unstable) == 1 && size+LogsSize(unstable) > maxSize {
// 		return logs, nil
// 	}
// 	return extend(logs, unstable), nil
// }

// maxAppliableIndex returns the maximum committed index that can be applied.
// If allowUnstable is true, committed entries from the unstable log can be
// applied; otherwise, only entries known to reside locally on stable storage
// can be applied.
func (r *replicaLog) maxAppliableIndex() uint64 {
	return r.committedIndex
}

func (r *replicaLog) mustCheckOutOfBounds(lo, hi uint64) error {
	if lo > hi {
		r.Panic(fmt.Sprintf("invalid slice %d > %d", lo, hi))
	}
	fi := r.firstIndex()
	if lo < fi {
		r.Error("mustCheckOutOfBounds err", zap.Uint64("lo", lo), zap.Uint64("firstIndex", fi))
		return ErrCompacted
	}

	length := r.lastIndex() + 1 - fi
	if hi > fi+length {
		r.Panic(fmt.Sprintf("slice[%d,%d) out of bound [%d,%d]", lo, hi, fi, r.lastIndex()))
	}
	return nil
}

// func (r *replicaLog) lastIndex() uint64 {
// 	if i, ok := r.unstable.maybeLastIndex(); ok {
// 		return i
// 	}
// 	i, err := r.opts.Storage.LastIndex()
// 	if err != nil {
// 		r.Panic("get last index failed", zap.Error(err))
// 	}
// 	return i
// }

func (r *replicaLog) lastIndex() uint64 {
	return r.lastLogIndex
}

func (r *replicaLog) firstIndex() uint64 {
	if i, ok := r.unstable.maybeFirstIndex(); ok {
		return i
	}
	i, err := r.opts.Storage.FirstIndex()
	if err != nil {
		r.Panic("get first index failed", zap.Error(err))
	}
	return i
}
