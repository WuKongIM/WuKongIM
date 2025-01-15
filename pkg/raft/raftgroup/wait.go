package raftgroup

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type wait struct {
	buckets []*waitBucket
}

func newWait() *wait {
	count := 100
	w := &wait{
		buckets: make([]*waitBucket, count),
	}
	for i := 0; i < count; i++ {
		w.buckets[i] = newWaitBucket(i)
	}
	return w
}

func (m *wait) waitApply(key string, maxIndex uint64) *progress {
	return m.buckets[m.bucketIndex(key)].waitApply(key, maxIndex)
}

func (m *wait) waitCommit(key string, maxIndex uint64) *progress {
	return m.buckets[m.bucketIndex(key)].waitCommit(key, maxIndex)
}

func (m *wait) didCommit(key string, maxLogIndex uint64) {
	m.buckets[m.bucketIndex(key)].didCommit(key, maxLogIndex)
}

func (m *wait) didApply(key string, maxLogIndex uint64) {
	m.buckets[m.bucketIndex(key)].didApply(key, maxLogIndex)
}

func (m *wait) bucketIndex(key string) int {
	return int(fnv32(key) % uint32(len(m.buckets)))
}

func fnv32(key string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	hash := offset32
	for i := 0; i < len(key); i++ {
		hash ^= int(key[i])
		hash *= prime32
	}
	return uint32(hash)
}

type waitBucket struct {
	mu sync.RWMutex
	wklog.Log

	progresses []*progress
}

func newWaitBucket(i int) *waitBucket {
	return &waitBucket{
		Log:        wklog.NewWKLog(fmt.Sprintf("applyWait[%d]", i)),
		progresses: make([]*progress, 0),
	}
}

func (m *waitBucket) waitApply(key string, maxIndex uint64) *progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	progress := newApplyProgress(key, maxIndex)
	m.progresses = append(m.progresses, progress)
	return progress

}

func (m *waitBucket) waitCommit(key string, maxIndex uint64) *progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	progress := newCommitProgress(key, maxIndex)
	m.progresses = append(m.progresses, progress)
	return progress
}

// didCommit 提交 maxLogIndex已提交的最大日志下标
func (m *waitBucket) didCommit(key string, maxLogIndex uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hasDone := false
	for _, progress := range m.progresses {

		if progress.done || !progress.waitCommittied {
			continue
		}
		if progress.key != key {
			continue
		}

		if maxLogIndex >= progress.maxIndex {
			hasDone = true
			progress.done = true
			progress.waitC <- struct{}{}
			close(progress.waitC)
		}
	}
	// 清理已完成的
	if hasDone {
		m.clean()
	}
}

func (m *waitBucket) didApply(key string, maxLogIndex uint64) {

	m.mu.Lock()
	defer m.mu.Unlock()
	// hasDone := false

	for _, progress := range m.progresses {

		if progress.key != key {
			continue
		}

		if progress.done || !progress.waitApplied {
			continue
		}
		if maxLogIndex >= progress.maxIndex {
			// hasDone = true
			progress.done = true
			progress.waitC <- struct{}{}
			close(progress.waitC)
		}
	}

	// 清理已完成的
	// if hasDone {
	// 	m.clean()
	// }
}

// 清理已完成的
func (m *waitBucket) clean() {
	for i := 0; i < len(m.progresses); {
		if m.progresses[i].done {
			m.progresses = append(m.progresses[:i], m.progresses[i+1:]...)
		} else {
			i++
		}
	}
}

type progress struct {
	maxIndex uint64

	waitC chan struct{}

	// 等待应用
	waitApplied bool
	// 等待提交
	waitCommittied bool

	done bool

	key string
}

func newApplyProgress(key string, maxIndex uint64) *progress {
	return &progress{
		maxIndex:    maxIndex,
		waitC:       make(chan struct{}, 1),
		waitApplied: true,
		key:         key,
	}
}

func newCommitProgress(key string, maxIndex uint64) *progress {
	return &progress{
		key:            key,
		maxIndex:       maxIndex,
		waitC:          make(chan struct{}, 1),
		waitCommittied: true,
	}
}

func (p *progress) String() string {
	return fmt.Sprintf("key:%s, maxIndex:%d, waitApplied:%v, waitCommittied:%v, done:%v", p.key, p.maxIndex, p.waitApplied, p.waitCommittied, p.done)
}
