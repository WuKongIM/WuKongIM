// Copyright 2017-2019 Lei Ni (nilei81@gmail.com) and other contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package raftgroup

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type EventQueue struct {
	ch            chan struct{}
	rl            *RateLimiter // 限制字节流量速度
	lazyFreeCycle uint64       // 懒惰释放周期，n表示n次释放一次
	size          uint64
	left          []Event // 左边队列
	right         []Event // 右边队列, 左右的目的是为了重复利用内存
	nodrop        []Event // 不能drop的消息
	mu            sync.Mutex
	leftInWrite   bool   // 写入时是否使用左边队列
	idx           uint64 // 当前写入的位置下标
	oldIdx        uint64
	cycle         uint64
	wklog.Log
}

func NewEventQueue(size uint64, ch bool,
	lazyFreeCycle uint64, maxMemorySize uint64) *EventQueue {

	q := &EventQueue{
		rl:            NewRateLimiter(maxMemorySize),
		size:          size,
		lazyFreeCycle: lazyFreeCycle,
		left:          make([]Event, size),
		right:         make([]Event, size),
		nodrop:        make([]Event, 0),
		Log:           wklog.NewWKLog("messageQueue"),
	}
	if ch {
		q.ch = make(chan struct{}, 1)
	}
	return q
}

func (q *EventQueue) Add(msg Event) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.idx >= q.size {
		return false
	}

	if !q.tryAdd(msg) {
		return false
	}

	w := q.targetQueue()
	w[q.idx] = msg
	q.idx++
	return true

}

// 必须要添加的消息不接受drop
func (q *EventQueue) MustAdd(msg Event) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.nodrop = append(q.nodrop, msg)
	q.shrinkNodropArray()
}

func (q *EventQueue) Get() []Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cycle++
	sz := q.idx
	q.idx = 0
	t := q.targetQueue()
	q.leftInWrite = !q.leftInWrite
	q.gc()
	q.oldIdx = sz
	if q.rl.Enabled() {
		q.rl.Set(0)
	}
	if len(q.nodrop) == 0 {
		return t[:sz]
	}
	// 避免多次分配内存，预分配足够的容量
	result := make([]Event, 0, len(q.nodrop)+int(sz))
	result = append(result, q.nodrop...)
	q.nodrop = q.nodrop[:0] // 重置 nodrop 队列

	result = append(result, t[:sz]...)
	return result
}

// 优化内存占用
func (q *EventQueue) shrinkNodropArray() {
	const lenMultiple = 2
	if len(q.nodrop) == 0 {
		q.nodrop = nil
	} else if len(q.nodrop)*lenMultiple < cap(q.nodrop) {
		newNodrop := make([]Event, len(q.nodrop))
		copy(newNodrop, q.nodrop)
		q.nodrop = newNodrop
	}
}

func (q *EventQueue) targetQueue() []Event {
	var t []Event
	if q.leftInWrite {
		t = q.left
	} else {
		t = q.right
	}
	return t
}

func (q *EventQueue) tryAdd(msg Event) bool {
	if !q.rl.Enabled() {
		return true
	}
	if q.rl.RateLimited() {
		q.Warn("rate limited dropped", zap.String("type", msg.Type.String()))
		return false
	}
	q.rl.Increase(msg.Size())
	return true
}

func (q *EventQueue) gc() {
	if q.lazyFreeCycle > 0 {
		oldq := q.targetQueue()
		if q.lazyFreeCycle == 1 {
			for i := uint64(0); i < q.oldIdx; i++ {
				oldq[i].Logs = nil
			}
		} else if q.cycle%q.lazyFreeCycle == 0 {
			for i := uint64(0); i < q.size; i++ {
				oldq[i].Logs = nil
			}
		}
	}
}

// func (q *EventQueue) notify() {
// 	if q.ch != nil {
// 		select {
// 		case q.ch <- struct{}{}:
// 		default:
// 		}
// 	}
// }

// // Ch returns the notification channel.
// func (q *EventQueue) notifyCh() <-chan struct{} {
// 	return q.ch
// }
