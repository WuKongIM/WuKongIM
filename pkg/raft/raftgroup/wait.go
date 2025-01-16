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

func (m *wait) didApply(key string, maxLogIndex uint64) {
	m.buckets[m.bucketIndex(key)].didApply(key, maxLogIndex)
}

func (m *wait) bucketIndex(key string) int {
	return int(fnv32(key) % uint32(len(m.buckets)))
}

func (m *wait) put(progress *progress) {
	m.buckets[m.bucketIndex(progress.key)].put(progress)
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

	progressPool sync.Pool
}

func newWaitBucket(i int) *waitBucket {
	return &waitBucket{
		Log:        wklog.NewWKLog(fmt.Sprintf("applyWait[%d]", i)),
		progresses: make([]*progress, 0),
		progressPool: sync.Pool{
			New: func() interface{} {
				return &progress{}
			},
		},
	}
}

func (m *waitBucket) waitApply(key string, maxIndex uint64) *progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	progress := m.progressPool.Get().(*progress)
	progress.key = key
	progress.maxIndex = maxIndex
	progress.waitApplied = true
	progress.done = false
	progress.waitC = make(chan struct{}, 1)
	m.progresses = append(m.progresses, progress)
	return progress

}

// func (m *waitBucket) waitCommit(key string, maxIndex uint64) *progress {
// 	m.mu.Lock()
// 	defer m.mu.Unlock()
// 	progress := newCommitProgress(key, maxIndex)
// 	m.progresses = append(m.progresses, progress)
// 	return progress
// }

// // didCommit 提交 maxLogIndex已提交的最大日志下标
// func (m *waitBucket) didCommit(key string, maxLogIndex uint64) {
// 	m.mu.Lock()
// 	defer m.mu.Unlock()
// 	hasDone := false
// 	for _, progress := range m.progresses {

// 		if progress.done || !progress.waitCommittied {
// 			continue
// 		}
// 		if progress.key != key {
// 			continue
// 		}

// 		if maxLogIndex >= progress.maxIndex {
// 			hasDone = true
// 			progress.done = true
// 			progress.waitC <- struct{}{}
// 			close(progress.waitC)
// 		}
// 	}
// 	// 清理已完成的
// 	if hasDone {
// 		m.clean()
// 	}
// }

func (m *waitBucket) didApply(key string, maxLogIndex uint64) {

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, progress := range m.progresses {

		if progress.key != key {
			continue
		}

		if progress.done || !progress.waitApplied {
			continue
		}
		if maxLogIndex >= progress.maxIndex && progress.waitC != nil {
			progress.done = true
			progress.waitC <- struct{}{}
		}
	}
	// 清理
	m.clean()
}

func (m *waitBucket) put(progress *progress) {
	close(progress.waitC)
	progress.reset()
	m.progressPool.Put(progress)
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

	done bool

	key string
}

func (p *progress) reset() {
	p.done = false
	p.maxIndex = 0
	p.waitApplied = false
	p.key = ""
	p.waitC = nil

}

func (p *progress) String() string {
	return fmt.Sprintf("key:%s, maxIndex:%d, waitApplied:%v, done:%v", p.key, p.maxIndex, p.waitApplied, p.done)
}
