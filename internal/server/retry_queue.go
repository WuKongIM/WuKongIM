package server

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// RetryQueue 重试队列
type RetryQueue struct {
	inFlightPQ       inFlightPqueue
	inFlightMessages map[string]*Message
	inFlightMutex    sync.Mutex
	s                *Server
	fakeMessageID    int64
}

// NewRetryQueue NewRetryQueue
func NewRetryQueue(s *Server) *RetryQueue {

	return &RetryQueue{
		inFlightPQ:       newInFlightPqueue(1024),
		inFlightMessages: make(map[string]*Message),
		s:                s,
		fakeMessageID:    10000,
	}
}

func (r *RetryQueue) startInFlightTimeout(msg *Message) {
	now := time.Now()
	msg.pri = now.Add(r.s.opts.MessageRetry.Interval).UnixNano()
	r.pushInFlightMessage(msg)
	r.addToInFlightPQ(msg)

	r.s.monitor.RetryQueueMsgInc()
}

func (r *RetryQueue) addToInFlightPQ(msg *Message) {
	r.inFlightMutex.Lock()
	defer r.inFlightMutex.Unlock()
	r.inFlightPQ.Push(msg)

}
func (r *RetryQueue) pushInFlightMessage(msg *Message) {
	r.inFlightMutex.Lock()
	defer r.inFlightMutex.Unlock()
	key := r.getInFlightKey(msg.ToUID, msg.toDeviceID, msg.MessageID)
	_, ok := r.inFlightMessages[key]
	if ok {
		return
	}
	r.inFlightMessages[key] = msg

}

func (r *RetryQueue) popInFlightMessage(uid string, deviceID string, messageID int64) (*Message, error) {
	r.inFlightMutex.Lock()
	defer r.inFlightMutex.Unlock()
	key := r.getInFlightKey(uid, deviceID, messageID)
	msg, ok := r.inFlightMessages[key]
	if !ok {
		return nil, errors.New("ID not in flight")
	}
	delete(r.inFlightMessages, key)
	return msg, nil
}

func (r *RetryQueue) getInFlightKey(uid string, deviceID string, messageID int64) string {
	return fmt.Sprintf("%s_%s_%d", uid, deviceID, messageID)
}
func (r *RetryQueue) finishMessage(uid string, deviceID string, messageID int64) error {
	msg, err := r.popInFlightMessage(uid, deviceID, messageID)
	if err != nil {
		return err
	}
	r.removeFromInFlightPQ(msg)

	r.s.monitor.RetryQueueMsgDec()

	return nil
}
func (r *RetryQueue) removeFromInFlightPQ(msg *Message) {
	r.inFlightMutex.Lock()
	if msg.index == -1 {
		// this item has already been popped off the pqueue
		r.inFlightMutex.Unlock()
		return
	}
	r.inFlightPQ.Remove(msg.index)
	r.inFlightMutex.Unlock()
}

func (r *RetryQueue) processInFlightQueue(t int64) {
	for {
		r.inFlightMutex.Lock()
		msg, _ := r.inFlightPQ.PeekAndShift(t)
		r.inFlightMutex.Unlock()
		if msg == nil {
			break
		}
		err := r.finishMessage(msg.ToUID, msg.toDeviceID, msg.MessageID)
		if err != nil {
			r.s.Error("processInFlightQueue-finishMessage失败", zap.Error(err), zap.String("toDeviceID", msg.toDeviceID), zap.Int64("messageID", msg.MessageID))
			break
		}
		r.s.deliveryManager.startRetryDeliveryMsg(msg)
	}
}

// Start 开始运行重试
func (r *RetryQueue) Start() {
	r.s.Schedule(r.s.opts.MessageRetry.ScanInterval, func() {
		now := time.Now().UnixNano()
		r.processInFlightQueue(now)
	})

	r.s.Schedule(time.Minute*5, func() {
		r.inFlightMutex.Lock()
		defer r.inFlightMutex.Unlock()
		r.s.monitor.InFlightMessagesSet(len(r.inFlightMessages))
	})
}

func (r *RetryQueue) Stop() {

}
