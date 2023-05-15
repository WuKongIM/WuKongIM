package server

import (
	"runtime"
	"sync"

	"github.com/eapache/queue"
)

// Queue Queue
type Queue struct {
	sync.Mutex
	popable *sync.Cond
	buffer  *queue.Queue
	closed  bool
}

// NewQueue 创建队列
func NewQueue() *Queue {
	e := &Queue{
		buffer: queue.New(),
	}
	e.popable = sync.NewCond(&e.Mutex)
	return e
}

// Push Push
func (e *Queue) Push(v interface{}) {
	e.Mutex.Lock()
	defer e.Mutex.Unlock()
	if !e.closed {
		e.buffer.Add(v)
		e.popable.Signal()
	}
}

// Close Close
func (e *Queue) Close() {
	e.Mutex.Lock()
	defer e.Mutex.Unlock()
	if !e.closed {
		e.closed = true
		e.popable.Broadcast() //广播
	}
}

// Pop 取出队列,（阻塞模式）
func (e *Queue) Pop() (v interface{}) {
	c := e.popable
	buffer := e.buffer

	e.Mutex.Lock()
	defer e.Mutex.Unlock()

	for buffer.Length() == 0 && !e.closed {
		c.Wait()
	}

	if e.closed { //已关闭
		return
	}

	if buffer.Length() > 0 {
		v = buffer.Peek()
		buffer.Remove()
	}
	return
}

// TryPop 试着取出队列（非阻塞模式）返回ok == false 表示空
func (e *Queue) TryPop() (v interface{}, ok bool) {
	buffer := e.buffer

	e.Mutex.Lock()
	defer e.Mutex.Unlock()

	if buffer.Length() > 0 {
		v = buffer.Peek()
		buffer.Remove()
		ok = true
	} else if e.closed {
		ok = true
	}

	return
}

// Len 获取队列长度
func (e *Queue) Len() int {
	return e.buffer.Length()
}

// Wait 等待队列消费完成
func (e *Queue) Wait() {
	for {
		if e.closed || e.buffer.Length() == 0 {
			break
		}

		runtime.Gosched() //出让时间片
	}
}
