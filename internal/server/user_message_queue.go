package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type userActionQueue struct {
	ch            chan struct{}
	rl            *reactor.RateLimiter // 限制字节流量速度
	lazyFreeCycle uint64               // 懒惰释放周期，n表示n次释放一次
	size          uint64
	left          []UserAction // 左边队列
	right         []UserAction // 右边队列, 左右的目的是为了重复利用内存
	nodrop        []UserAction // 不能drop的消息
	mu            sync.Mutex
	leftInWrite   bool   // 写入时是否使用左边队列
	idx           uint64 // 当前写入的位置下标
	oldIdx        uint64
	stopped       bool // 是否停止
	cycle         uint64
	wklog.Log
}

func newUserActionQueue(size uint64, ch bool,
	lazyFreeCycle uint64, maxMemorySize uint64) *userActionQueue {

	q := &userActionQueue{
		rl:            reactor.NewRateLimiter(maxMemorySize),
		size:          size,
		lazyFreeCycle: lazyFreeCycle,
		left:          make([]UserAction, size),
		right:         make([]UserAction, size),
		nodrop:        make([]UserAction, 0),
		Log:           wklog.NewWKLog("messageQueue"),
	}
	if ch {
		q.ch = make(chan struct{}, 1)
	}
	return q
}

func (q *userActionQueue) add(msg UserAction) (bool, bool) {
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
func (q *userActionQueue) mustAdd(msg UserAction) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return false
	}
	q.nodrop = append(q.nodrop, msg)
	return true
}

func (q *userActionQueue) get() []UserAction {
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

	var result []UserAction
	if len(q.nodrop) > 0 {
		ssm := q.nodrop
		q.nodrop = make([]UserAction, 0)
		result = append(result, ssm...)
	}
	return append(result, t[:sz]...)
}

func (q *userActionQueue) targetQueue() []UserAction {
	var t []UserAction
	if q.leftInWrite {
		t = q.left
	} else {
		t = q.right
	}
	return t
}

func (q *userActionQueue) tryAdd(msg UserAction) bool {
	if !q.rl.Enabled() {
		return true
	}
	if q.rl.RateLimited() {
		q.Warn("rate limited dropped", zap.String("actionType", msg.ActionType.String()))
		return false
	}
	q.rl.Increase(uint64(msg.Size()))
	return true
}

func (q *userActionQueue) gc() {
	if q.lazyFreeCycle > 0 {
		oldq := q.targetQueue()
		if q.lazyFreeCycle == 1 {
			for i := uint64(0); i < q.oldIdx; i++ {
				oldq[i].Messages = nil
			}
		} else if q.cycle%q.lazyFreeCycle == 0 {
			for i := uint64(0); i < q.size; i++ {
				oldq[i].Messages = nil
			}
		}
	}
}

func (q *userActionQueue) notify() {
	if q.ch != nil {
		select {
		case q.ch <- struct{}{}:
		default:
		}
	}
}

// Ch returns the notification channel.
func (q *userActionQueue) notifyCh() <-chan struct{} {
	return q.ch
}
