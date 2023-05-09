package wkutil

import (
	"sync"
	"sync/atomic"
)

// WaitGroupWrapper waitGroup的包装结构体
type WaitGroupWrapper struct {
	sync.WaitGroup
	Name  string
	count int64
}

// NewWaitGroupWrapper NewWaitGroupWrapper
func NewWaitGroupWrapper(name string) *WaitGroupWrapper {
	return &WaitGroupWrapper{
		Name: name,
	}
}

// Wrap Wrap
func (w *WaitGroupWrapper) Wrap(cb func()) {
	w.Add(1)
	atomic.AddInt64(&w.count, 1)
	go func() {
		cb()
		w.Done()
		atomic.AddInt64(&w.count, -1)
	}()
}

// GoroutineCount 协程数量
func (w *WaitGroupWrapper) GoroutineCount() int64 {
	return atomic.LoadInt64(&w.count)
}
