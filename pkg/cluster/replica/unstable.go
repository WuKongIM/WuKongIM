package replica

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type unstable struct {
	logs []Log

	offset           uint64
	offsetInProgress uint64
	wklog.Log
}

func newUnstable(prefix string) *unstable {
	return &unstable{
		Log:    wklog.NewWKLog(fmt.Sprintf("replica.unstable[%s]", prefix)),
		offset: 0,
	}
}

func (u *unstable) maybeFirstIndex() (uint64, bool) {

	return 0, false
}

// appliedTo 标记index之前的日志已经处理完成，可以删除
// [1 2 3 4 5 6 7 8 9]
// appliedTo(5) => [6 7 8 9]
func (u *unstable) appliedTo(index uint64) {
	offsetIndex := u.offsetIndex(index + 1)
	if offsetIndex < u.offset { // offsetIndex小于offset，说明index已经被存储了，不在unstable中
		u.Info(fmt.Sprintf("appliedTo %d is out of bound %d.", offsetIndex, u.offset))
		return
	}
	num := int(offsetIndex - u.offset)

	if num > len(u.logs) {
		u.Panic("appliedTo index is out of bound", zap.Uint64("index", offsetIndex), zap.Uint64("offset", u.offset), zap.Int("num", num), zap.Int("len", len(u.logs)))
		return
	}
	u.logs = u.logs[num:]
	u.offset = offsetIndex
	u.offsetInProgress = max(u.offsetInProgress, u.offset)
	u.shrinkLogsArray()

}

func (u *unstable) offsetIndex(logIndex uint64) uint64 {
	return logIndex - 1
}

// nextLogs 返回未持久化的日志
func (u *unstable) nextLogs() []Log {
	inProgress := int(u.offsetInProgress - u.offset)
	if len(u.logs) == inProgress {
		return nil
	}
	return u.logs[inProgress:]
}

func (u *unstable) hasNextLogs() bool {
	return int(u.offsetInProgress-u.offset) < len(u.logs)
}

// shrinkLogsArray discards the underlying array used by the entries slice
// if most of it isn't being used. This avoids holding references to a bunch of
// potentially large entries that aren't needed anymore. Simply clearing the
// entries wouldn't be safe because clients might still be using them.
func (u *unstable) shrinkLogsArray() {
	// We replace the array if we're using less than half of the space in
	// it. This number is fairly arbitrary, chosen as an attempt to balance
	// memory usage vs number of allocations. It could probably be improved
	// with some focused tuning.
	const lenMultiple = 2
	if len(u.logs) == 0 {
		u.logs = nil
	} else if len(u.logs)*lenMultiple < cap(u.logs) {
		newLogs := make([]Log, len(u.logs))
		copy(newLogs, u.logs)
		u.logs = newLogs
	}
}

func (u *unstable) truncateAndAppend(logs []Log) {
	fromOffsetIndex := u.offsetIndex(logs[0].Index)
	switch {
	case fromOffsetIndex == u.offset+uint64(len(u.logs)):
		// fromIndex is the next index in the u.logs, so append directly.
		u.logs = append(u.logs, logs...)
	case fromOffsetIndex <= u.offset:
		// The log is being truncated to before our current offset
		// portion, so set the offset and replace the logs.
		u.logs = logs
		u.offset = fromOffsetIndex
		u.offsetInProgress = u.offset
		// u.Panic("truncateAndAppend.....1", zap.Uint64("fromOffsetIndex", fromOffsetIndex), zap.Uint64("offset", u.offset), zap.Uint64("offsetInProgress", u.offsetInProgress))

	default:
		// Truncate to the first conflicting index, then append.
		u.Info(fmt.Sprintf("truncateAndAppend------> %d %d %d", u.offset, fromOffsetIndex, len(u.logs)))
		keep := u.slice(u.offset, fromOffsetIndex)
		u.logs = append(keep, logs...)
		u.offsetInProgress = min(u.offsetInProgress, fromOffsetIndex)
		u.Panic("truncateAndAppend.....2")

	}
}

// truncateLogTo 裁剪日志至index， index和index之后的日志全部删除（注意裁剪的内容包含index，也就是保留的值不包含index）
func (u *unstable) truncateLogTo(index uint64) {

	offsetIndex := u.offsetIndex(index)
	if offsetIndex < u.offset {
		u.Info(fmt.Sprintf("truncateLogTo %d is out of bound %d", offsetIndex, u.offset))
		return
	}

	// 从offset开始截取
	up := max(0, min(offsetIndex, u.offset+uint64(len(u.logs))))
	u.logs = u.slice(u.offset, up)
	u.offset = up
	u.offsetInProgress = min(u.offsetInProgress, up)

	u.shrinkLogsArray()
}

// slice [lo, hi)
func (u *unstable) slice(lo uint64, hi uint64) []Log {
	u.mustCheckOutOfBounds(lo, hi)

	return u.logs[lo-u.offset : hi-u.offset : hi-u.offset]
}

// acceptInProgress marks all logs and the snapshot, if any, in the unstable
// as having begun the process of being written to storage. The logs/snapshot
// will no longer be returned from logs/nextSnapshot. However, new
// logs/snapshots added after a call to acceptInProgress will be returned
// from those methods, until the next call to acceptInProgress.
func (u *unstable) acceptInProgress() {
	if len(u.logs) > 0 {
		// NOTE: +1 because offsetInProgress is exclusive, like offset.
		u.offsetInProgress = u.logs[len(u.logs)-1].Index + 1
	}
}

func (u *unstable) mustCheckOutOfBounds(lo, hi uint64) {
	if lo > hi {
		u.Panic(fmt.Sprintf("invalid unstable.slice %d > %d", lo, hi))
	}
	upper := u.offset + uint64(len(u.logs)) + 1
	if lo < u.offset || hi > upper {
		u.Panic(fmt.Sprintf("unstable.slice[%d,%d) out of bound [%d,%d]", lo, hi, u.offset, upper))
	}
}

// lastLog 返回最后一个日志
func (u *unstable) lastLog() Log {
	if len(u.logs) == 0 {
		return Log{}
	}
	return u.logs[len(u.logs)-1]
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b uint64) uint64 {
	if a > b {
		return b
	}
	return a
}
