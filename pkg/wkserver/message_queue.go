package wkserver

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
)

type message struct {
	msgType proto.MsgType
	data    []byte
	conn    gnet.Conn
}

func (m *message) size() int {
	return len(m.data)
}

type messageQueue struct {
	ch            chan struct{}
	rl            *RateLimiter // 限制字节流量速度
	lazyFreeCycle uint64       // 懒惰释放周期，n表示n次释放一次
	size          uint64
	left          []*message // 左边队列
	right         []*message // 右边队列, 左右的目的是为了重复利用内存
	nodrop        []*message // 不能drop的消息
	mu            sync.Mutex
	leftInWrite   bool   // 写入时是否使用左边队列
	idx           uint64 // 当前写入的位置下标
	oldIdx        uint64
	cycle         uint64
	wklog.Log
}

func newMessageQueue(size uint64, ch bool,
	lazyFreeCycle uint64, maxMemorySize uint64) *messageQueue {

	q := &messageQueue{
		rl:            NewRateLimiter(maxMemorySize),
		size:          size,
		lazyFreeCycle: lazyFreeCycle,
		left:          make([]*message, size),
		right:         make([]*message, size),
		nodrop:        make([]*message, 0),
		Log:           wklog.NewWKLog("messageQueue"),
	}
	if ch {
		q.ch = make(chan struct{}, 1)
	}
	return q
}

func (q *messageQueue) add(msg *message) bool {
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
func (q *messageQueue) mustAdd(msg *message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nodrop = append(q.nodrop, msg)
}

func (q *messageQueue) get() []*message {
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

	var result []*message
	if len(q.nodrop) > 0 {
		ssm := q.nodrop
		q.nodrop = make([]*message, 0)
		result = append(result, ssm...)
	}
	return append(result, t[:sz]...)
}

func (q *messageQueue) targetQueue() []*message {
	var t []*message
	if q.leftInWrite {
		t = q.left
	} else {
		t = q.right
	}
	return t
}

func (q *messageQueue) tryAdd(msg *message) bool {
	if !q.rl.Enabled() {
		return true
	}
	if q.rl.RateLimited() {
		q.Warn("rate limited dropped", zap.Uint8("msgType", msg.msgType.Uint8()))
		return false
	}
	q.rl.Increase(uint64(msg.size()))
	return true
}

func (q *messageQueue) gc() {
	if q.lazyFreeCycle > 0 {
		oldq := q.targetQueue()
		if q.lazyFreeCycle == 1 {
			for i := uint64(0); i < q.oldIdx; i++ {
				oldq[i].data = nil
			}
		} else if q.cycle%q.lazyFreeCycle == 0 {
			for i := uint64(0); i < q.size; i++ {
				oldq[i].data = nil
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
