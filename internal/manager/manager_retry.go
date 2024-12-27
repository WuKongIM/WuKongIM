package manager

import (
	"math/rand/v2"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type RetryManager struct {
	retryQueues []*RetryQueue
	timingWheel *timingwheel.TimingWheel
	wklog.Log
}

func NewRetryManager() *RetryManager {
	return &RetryManager{
		retryQueues: make([]*RetryQueue, options.G.MessageRetry.WorkerCount),
		Log:         wklog.NewWKLog("retryManager"),
		timingWheel: timingwheel.NewTimingWheel(options.G.TimingWheelTick, options.G.TimingWheelSize),
	}
}

func (r *RetryManager) Start() error {
	r.timingWheel.Start()
	for i := 0; i < options.G.MessageRetry.WorkerCount; i++ {
		retryQueue := NewRetryQueue(i, r)
		r.retryQueues[i] = retryQueue
		retryQueue.Start()
	}

	return nil
}

func (r *RetryManager) Stop() {

	for i := 0; i < options.G.MessageRetry.WorkerCount; i++ {
		r.retryQueues[i].Stop()
	}

}

// retryMessageCount 获取重试消息数量
func (r *RetryManager) RetryMessageCount() int {
	count := 0
	for i := 0; i < options.G.MessageRetry.WorkerCount; i++ {
		count += r.retryQueues[i].inFlightMessagesCount()
	}
	return count
}

func (r *RetryManager) AddRetry(msg *types.RetryMessage) {
	index := msg.MessageId % int64(len(r.retryQueues))
	r.retryQueues[index].startInFlightTimeout(msg)
}

func (r *RetryManager) RemoveRetry(fromNode uint64, connId int64, messageId int64) error {
	index := messageId % int64(len(r.retryQueues))
	return r.retryQueues[index].finishMessage(fromNode, connId, messageId)
}

// 获取重试消息
func (r *RetryManager) RetryMessage(fromNode uint64, connId int64, messageId int64) *types.RetryMessage {
	index := messageId % int64(len(r.retryQueues))

	return r.retryQueues[index].getInFlightMessage(fromNode, connId, messageId)
}

func (r *RetryManager) retry(msg *types.RetryMessage) {
	r.Debug("retry msg", zap.Int("retryCount", msg.Retry), zap.String("uid", msg.Uid), zap.Int64("messageId", msg.MessageId), zap.Int64("connId", msg.ConnId))
	msg.Retry++
	if msg.Retry > options.G.MessageRetry.MaxCount {
		r.Debug("exceeded the maximum number of retries", zap.String("uid", msg.Uid), zap.Int64("messageId", msg.MessageId), zap.Int("messageMaxRetryCount", options.G.MessageRetry.MaxCount))
		return
	}
	conn := eventbus.User.ConnById(msg.Uid, msg.FromNode, msg.ConnId)
	if conn == nil {
		r.Debug("conn offline", zap.String("uid", msg.Uid), zap.Int64("messageId", msg.MessageId), zap.Int64("connId", msg.ConnId))
		return
	}
	// 添加到重试队列
	r.AddRetry(msg)

	// 发送消息
	// 在需要打印日志的地方添加概率控制
	if rand.Float64() < 0.1 { // 10%的概率
		r.Info("retry send message", zap.Int("retry", msg.Retry), zap.Uint64("fromNode", msg.FromNode), zap.String("uid", msg.Uid), zap.Int64("messageId", msg.MessageId), zap.Int64("connId", msg.ConnId))
	}

	eventbus.User.ConnWrite(conn, msg.RecvPacket)

}

// Schedule 延迟任务
func (r *RetryManager) schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return r.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}
