package service

import "github.com/WuKongIM/WuKongIM/internal/types"

var RetryManager RetryMgr

type RetryMgr interface {
	// RetryMessageCount 重试消息数量
	RetryMessageCount() int
	// AddRetry 添加重试消息
	AddRetry(msg *types.RetryMessage)
	// RemoveRetry 移除重试消息
	RemoveRetry(fromNode uint64, connId int64, messageId int64) error
	// 获取重试消息
	RetryMessage(fromNode uint64, connId int64, messageId int64) *types.RetryMessage
}
