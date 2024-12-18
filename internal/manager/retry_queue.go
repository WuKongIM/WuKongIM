package manager

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

// RetryQueue 重试队列
type RetryQueue struct {
	inFlightPQ       inFlightPqueue
	inFlightMessages map[string]*types.RetryMessage
	inFlightMutex    sync.Mutex
	fakeMessageID    int64
	wklog.Log
	r          *RetryManager
	stopped    atomic.Bool
	retryTimer *timingwheel.Timer
}

// NewRetryQueue NewRetryQueue
func NewRetryQueue(index int, r *RetryManager) *RetryQueue {

	return &RetryQueue{
		r:                r,
		inFlightPQ:       newInFlightPqueue(4056),
		inFlightMessages: make(map[string]*types.RetryMessage),
		fakeMessageID:    10000,
		Log:              wklog.NewWKLog(fmt.Sprintf("RetryQueue[%d]", index)),
	}
}

func (r *RetryQueue) startInFlightTimeout(msg *types.RetryMessage) {
	now := time.Now()
	msg.Pri = now.Add(time.Second * 10).UnixNano()
	r.pushInFlightMessage(msg)
	r.addToInFlightPQ(msg)

}

func (r *RetryQueue) addToInFlightPQ(msg *types.RetryMessage) {
	r.inFlightMutex.Lock()
	defer r.inFlightMutex.Unlock()
	r.inFlightPQ.Push(msg)

}
func (r *RetryQueue) pushInFlightMessage(msg *types.RetryMessage) {
	r.inFlightMutex.Lock()
	defer r.inFlightMutex.Unlock()
	key := r.getInFlightKey(msg.FromNode, msg.ConnId, msg.MessageId)
	_, ok := r.inFlightMessages[key]
	if ok {
		r.Warn("ID already in flight", zap.String("key", key), zap.String("uid", msg.Uid), zap.Int64("connId", msg.ConnId), zap.Uint64("fromNode", msg.FromNode), zap.Int64("messageId", msg.MessageId))
		return
	}
	r.inFlightMessages[key] = msg

}

func (r *RetryQueue) popInFlightMessage(fromNodeId uint64, connId int64, messageId int64) (*types.RetryMessage, error) {
	r.inFlightMutex.Lock()
	defer r.inFlightMutex.Unlock()
	key := r.getInFlightKey(fromNodeId, connId, messageId)
	msg, ok := r.inFlightMessages[key]
	if !ok {
		r.Warn("ID not in flight", zap.String("key", key), zap.Int64("connId", connId), zap.Int64("messageId", messageId))
		return nil, errors.New("ID not in flight")
	}
	delete(r.inFlightMessages, key)
	return msg, nil
}

func (r *RetryQueue) getInFlightMessage(fromNodeId uint64, connId int64, messageId int64) *types.RetryMessage {
	r.inFlightMutex.Lock()
	defer r.inFlightMutex.Unlock()
	key := r.getInFlightKey(fromNodeId, connId, messageId)
	msg := r.inFlightMessages[key]
	return msg
}

func (r *RetryQueue) getInFlightKey(fromNodeId uint64, connId int64, messageId int64) string {
	var b strings.Builder
	b.WriteString(strconv.FormatUint(fromNodeId, 10))
	b.WriteString(":")
	b.WriteString(strconv.FormatInt(connId, 10))
	b.WriteString(":")
	b.WriteString(strconv.FormatInt(messageId, 10))
	return b.String()
}
func (r *RetryQueue) finishMessage(fromNode uint64, connId int64, messageId int64) error {
	msg, err := r.popInFlightMessage(fromNode, connId, messageId)
	if err != nil {
		return err
	}
	r.removeFromInFlightPQ(msg)

	return nil
}
func (r *RetryQueue) removeFromInFlightPQ(msg *types.RetryMessage) {
	r.inFlightMutex.Lock()
	if msg.Index == -1 {
		// this item has already been popped off the pqueue
		r.inFlightMutex.Unlock()
		return
	}
	r.inFlightPQ.Remove(msg.Index)
	r.inFlightMutex.Unlock()
}

func (r *RetryQueue) processInFlightQueue(t int64) {
	for !r.stopped.Load() {
		r.inFlightMutex.Lock()
		msg, _ := r.inFlightPQ.PeekAndShift(t)
		r.inFlightMutex.Unlock()
		if msg == nil {
			break
		}
		err := r.finishMessage(msg.FromNode, msg.ConnId, msg.MessageId)
		if err != nil {
			r.Error("processInFlightQueue-finishMessage失败", zap.Error(err), zap.Int64("connId", msg.ConnId), zap.Int64("messageId", msg.MessageId))
			break
		}
		r.r.retry(msg) // 重试
	}
}

// inFlightMessagesCount 返回正在飞行的消息数量
func (r *RetryQueue) inFlightMessagesCount() int {
	r.inFlightMutex.Lock()
	defer r.inFlightMutex.Unlock()
	return max(len(r.inFlightMessages), len(r.inFlightPQ))
}

// Start 开始运行重试
func (r *RetryQueue) Start() {
	r.retryTimer = r.r.schedule(time.Second*5, func() {
		now := time.Now().UnixNano()
		r.processInFlightQueue(now)
	})
}

func (r *RetryQueue) Stop() {
	r.stopped.Store(true)
	if r.retryTimer != nil {
		r.retryTimer.Stop()
		r.retryTimer = nil
	}
}
