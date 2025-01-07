package raft

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type wait struct {
	mu sync.RWMutex
	wklog.Log

	progresses []*progress
}

func newWait(key string) *wait {
	return &wait{
		Log:        wklog.NewWKLog(fmt.Sprintf("applyWait[%s]", key)),
		progresses: make([]*progress, 0),
	}
}

func (m *wait) waitApply(maxIndex uint64) *progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	progress := newApplyProgress(maxIndex)
	m.progresses = append(m.progresses, progress)
	return progress

}

func (m *wait) waitCommit(maxIndex uint64) *progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	progress := newCommitProgress(maxIndex)
	m.progresses = append(m.progresses, progress)
	return progress
}

// didCommit 提交 maxLogIndex已提交的最大日志下标
func (m *wait) didCommit(maxLogIndex uint64) {

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, progress := range m.progresses {

		if progress.done || !progress.waitCommittied {
			continue
		}

		if maxLogIndex >= progress.maxIndex {
			progress.done = true
			progress.waitC <- struct{}{}
			close(progress.waitC)
		}
	}
	// 清理已完成的
	m.clean()
}

func (m *wait) didApply(maxLogIndex uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, progress := range m.progresses {

		if progress.done || !progress.waitApplied {
			continue
		}

		if maxLogIndex >= progress.maxIndex {
			progress.done = true
			progress.waitC <- struct{}{}
			close(progress.waitC)
		}
	}

	// 清理已完成的
	m.clean()
}

// 清理已完成的
func (m *wait) clean() {
	for i := 0; i < len(m.progresses); {
		if m.progresses[i].done {
			m.progresses = append(m.progresses[:i], m.progresses[i+1:]...)
		} else {
			i++
		}
	}
}

// func (m *applyWait) remove(progress *progress) {
// 	m.mu.Lock()
// 	defer m.mu.Unlock()

// 	for i, pg := range m.progresses {
// 		if pg == progress {
// 			m.progresses = append(m.progresses[:i], m.progresses[i+1:]...)
// 			break
// 		}
// 	}
// }

type progress struct {
	maxIndex uint64

	waitC chan struct{}

	// 等待应用
	waitApplied bool
	// 等待提交
	waitCommittied bool

	done bool
}

func newApplyProgress(maxIndex uint64) *progress {
	return &progress{
		maxIndex:    maxIndex,
		waitC:       make(chan struct{}, 1),
		waitApplied: true,
	}
}

func newCommitProgress(maxIndex uint64) *progress {
	return &progress{
		maxIndex:       maxIndex,
		waitC:          make(chan struct{}, 1),
		waitCommittied: true,
	}
}
