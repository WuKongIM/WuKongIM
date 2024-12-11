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

package cluster

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

type messageQueue struct {
	ch            chan struct{}
	rl            *RateLimiter // 限制字节流量速度
	lazyFreeCycle uint64       // 懒惰释放周期，n表示n次释放一次
	size          uint64
	left          []*proto.Message // 左边队列
	right         []*proto.Message // 右边队列, 左右的目的是为了重复利用内存
	nodrop        []*proto.Message // 不能drop的消息
	mu            sync.Mutex
	leftInWrite   bool   // 写入时是否使用左边队列
	idx           uint64 // 当前写入的位置下标
	oldIdx        uint64
	stopped       bool // 是否停止
	cycle         uint64
	wklog.Log
}

func newMessageQueue(size uint64, ch bool,
	lazyFreeCycle uint64, maxMemorySize uint64) *messageQueue {

	q := &messageQueue{
		rl:            NewRateLimiter(maxMemorySize),
		size:          size,
		lazyFreeCycle: lazyFreeCycle,
		left:          make([]*proto.Message, size),
		right:         make([]*proto.Message, size),
		nodrop:        make([]*proto.Message, 0),
		Log:           wklog.NewWKLog("messageQueue"),
	}
	if ch {
		q.ch = make(chan struct{}, 1)
	}
	return q
}

func (q *messageQueue) add(msg *proto.Message) (bool, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.idx >= q.size {
		return false, q.stopped
	}
	if q.stopped {
		return false, true
	}
	if !q.tryAdd(msg) {
		return false, false
	}

	w := q.targetQueue()
	w[q.idx] = msg
	q.idx++
	return true, false

}

// 必须要添加的消息不接受drop
func (q *messageQueue) mustAdd(msg *proto.Message) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return false
	}
	q.nodrop = append(q.nodrop, msg)
	return true
}

func (q *messageQueue) get() []*proto.Message {
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

	var result []*proto.Message
	if len(q.nodrop) > 0 {
		ssm := q.nodrop
		q.nodrop = make([]*proto.Message, 0)
		result = append(result, ssm...)
	}
	return append(result, t[:sz]...)
}

func (q *messageQueue) targetQueue() []*proto.Message {
	var t []*proto.Message
	if q.leftInWrite {
		t = q.left
	} else {
		t = q.right
	}
	return t
}

func (q *messageQueue) tryAdd(msg *proto.Message) bool {
	if !q.rl.Enabled() {
		return true
	}
	if q.rl.RateLimited() {
		q.Warn("rate limited dropped", zap.Uint32("msgType", msg.MsgType))
		return false
	}
	q.rl.Increase(uint64(msg.Size()))
	return true
}

func (q *messageQueue) gc() {
	if q.lazyFreeCycle > 0 {
		oldq := q.targetQueue()
		if q.lazyFreeCycle == 1 {
			for i := uint64(0); i < q.oldIdx; i++ {
				oldq[i].Content = nil
			}
		} else if q.cycle%q.lazyFreeCycle == 0 {
			for i := uint64(0); i < q.size; i++ {
				oldq[i].Content = nil
			}
		}
	}
}

func (q *messageQueue) notify() {
	if q.ch != nil {
		select {
		case q.ch <- struct{}{}:
		default:
		}
	}
}

// Ch returns the notification channel.
func (q *messageQueue) notifyCh() <-chan struct{} {
	return q.ch
}
