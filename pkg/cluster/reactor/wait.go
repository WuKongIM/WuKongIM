package reactor

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type proposeWait struct {
	mu sync.RWMutex
	wklog.Log

	progresses []*proposeProgress
}

func newProposeWait(key string) *proposeWait {
	return &proposeWait{
		Log:        wklog.NewWKLog(fmt.Sprintf("proposeWait[%s]", key)),
		progresses: make([]*proposeProgress, 0),
	}
}

// TODO: 此方法返回创建ProposeResult导致内存过高，需要优化
func (m *proposeWait) add(key string, minId, maxId uint64) *proposeProgress {
	m.mu.Lock()
	defer m.mu.Unlock()
	if minId == 0 || maxId == 0 {
		m.Panic("add minId or maxId is 0", zap.Uint64("minId", minId), zap.Uint64("maxId", maxId))
	}

	progress := newProposeProgress(key, minId, maxId, 0, 0)
	m.progresses = append(m.progresses, progress)

	// waitC := make(chan []ProposeResult, 1)
	// items := make([]ProposeResult, len(ids))
	// for i, id := range ids {
	// 	items[i] = ProposeResult{
	// 		Id: id,
	// 	}
	// }

	// // m.Debug("addWait", zap.String("key", key), zap.Int("ids", len(ids)))

	// m.proposeResultMap[key] = items
	// m.proposeWaitMap[key] = waitC

	return progress
}

// TODO 此方法startMessageSeq 至 endMessageSeq的跨度十万需要10来秒 很慢 需要优化
func (m *proposeWait) didPropose(key string, minIndex uint64, maxIndex uint64) {
	if minIndex == 0 {
		m.Panic("didPropose minIndex is 0")
	}
	if minIndex > maxIndex {
		m.Panic("didPropose minIndex > maxIndex")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	exist := false
	for _, progress := range m.progresses {
		if progress.key == key {
			progress.minIndex = minIndex
			progress.maxIndex = maxIndex
			exist = true
			break
		}
	}
	if !exist {
		m.Info("didPropose key not exist", zap.String("key", key))
	}
}

// didCommit 提交[startLogIndex, endLogIndex)范围的消息
func (m *proposeWait) didCommit(startLogIndex uint64, endLogIndex uint64) {

	m.mu.Lock()
	defer m.mu.Unlock()

	if startLogIndex == 0 {
		m.Panic("didCommit startLogIndex is 0")
	}

	if endLogIndex == 0 {
		m.Panic("didCommit endLogIndex is 0")
	}

	if startLogIndex > endLogIndex {
		m.Panic("didCommit startLogIndex > endLogIndex")
	}

	for _, progress := range m.progresses {
		if endLogIndex >= progress.maxIndex {
			progress.progressIndex = progress.maxIndex
			if !progress.done {
				progress.done = true
				progress.waitC <- nil
			}

		} else if startLogIndex >= progress.minIndex {
			progress.progressIndex = startLogIndex
		}
	}

}

func (m *proposeWait) remove(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, progress := range m.progresses {
		if progress.key == key {
			m.progresses = append(m.progresses[:i], m.progresses[i+1:]...)
			break
		}
	}
}

func (m *proposeWait) exist(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, progress := range m.progresses {
		if progress.key == key {
			return true
		}
	}
	return false

}

type proposeProgress struct {
	key string

	minId uint64
	maxId uint64

	minIndex uint64
	maxIndex uint64

	progressIndex uint64

	done  bool // 是否已经完成
	waitC chan error
}

func newProposeProgress(key string, minId uint64, maxId uint64, minIndex uint64, maxIndex uint64) *proposeProgress {
	return &proposeProgress{
		key:      key,
		minId:    minId,
		maxId:    maxId,
		minIndex: minIndex,
		maxIndex: maxIndex,
		waitC:    make(chan error, 1),
	}
}
