package server

import (
	"math/rand/v2"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type retryManager struct {
	retryQueues []*RetryQueue
	s           *Server
	wklog.Log
}

func newRetryManager(s *Server) *retryManager {
	return &retryManager{
		s:           s,
		retryQueues: make([]*RetryQueue, s.opts.MessageRetry.WorkerCount),
		Log:         wklog.NewWKLog("retryManager"),
	}
}

func (r *retryManager) start() error {

	for i := 0; i < r.s.opts.MessageRetry.WorkerCount; i++ {
		retryQueue := NewRetryQueue(i, r.s)
		r.retryQueues[i] = retryQueue
		retryQueue.Start()
	}

	return nil
}

func (r *retryManager) stop() {

	for i := 0; i < r.s.opts.MessageRetry.WorkerCount; i++ {
		r.retryQueues[i].Stop()
	}

}

// retryMessageCount 获取重试消息数量
func (r *retryManager) retryMessageCount() int {
	count := 0
	for i := 0; i < r.s.opts.MessageRetry.WorkerCount; i++ {
		count += r.retryQueues[i].inFlightMessagesCount()
	}
	return count
}

func (r *retryManager) addRetry(msg *retryMessage) {
	index := msg.messageId % int64(len(r.retryQueues))
	r.retryQueues[index].startInFlightTimeout(msg)
}

func (r *retryManager) removeRetry(connId int64, messageId int64) error {
	index := messageId % int64(len(r.retryQueues))
	return r.retryQueues[index].finishMessage(connId, messageId)
}

// 获取重试消息
func (r *retryManager) retryMessage(connId int64, messageId int64) *retryMessage {
	index := messageId % int64(len(r.retryQueues))

	return r.retryQueues[index].getInFlightMessage(connId, messageId)
}

func (r *retryManager) retry(msg *retryMessage) {
	r.Debug("retry msg", zap.Int("retryCount", msg.retry), zap.String("uid", msg.uid), zap.Int64("messageId", msg.messageId), zap.Int64("connId", msg.connId))
	msg.retry++
	if msg.retry > r.s.opts.MessageRetry.MaxCount {
		r.Debug("exceeded the maximum number of retries", zap.String("uid", msg.uid), zap.Int64("messageId", msg.messageId), zap.Int("messageMaxRetryCount", r.s.opts.MessageRetry.MaxCount))
		return
	}
	userHandler := r.s.userReactor.getUserHandler(msg.uid)
	if userHandler == nil {
		r.Debug("user offline, retry end", zap.String("uid", msg.uid), zap.Int64("messageId", msg.messageId), zap.Int64("connId", msg.connId))
		return
	}
	conn := userHandler.getConnById(msg.connId)
	if conn == nil {
		r.Debug("conn offline", zap.String("uid", msg.uid), zap.Int64("messageId", msg.messageId), zap.Int64("connId", msg.connId))
		return
	}
	// 添加到重试队列
	r.addRetry(msg)

	// 发送消息
	// 在需要打印日志的地方添加概率控制
	if rand.Float64() < 0.1 { // 10%的概率
		r.Info("retry send message", zap.Int("retry", msg.retry), zap.String("uid", msg.uid), zap.Int64("messageId", msg.messageId), zap.Int64("connId", msg.connId))
	}
	err := conn.write(msg.recvPacketData, wkproto.RECV)
	if err != nil {
		r.Warn("write message failed", zap.String("uid", msg.uid), zap.Int64("messageId", msg.messageId), zap.Int64("connId", msg.connId), zap.Error(err))
		conn.close()
		return
	}

}

type retryMessage struct {
	recvPacketData []byte // 接受包数据
	channelId      string // 频道id
	channelType    uint8  // 频道类型
	uid            string // 用户id
	connId         int64  // 需要接受的连接id
	messageId      int64  // 消息id
	retry          int    // 重试次数
	index          int    //在切片中的索引值
	pri            int64  // 优先级的时间点 值越小越优先
}
