package service

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

var ConversationManager IConversationManager

type IConversationManager interface {
	// Push 更新最近会话
	Push(fakeChannelId string, channelType uint8, tagKey string, events []*eventbus.Event)
	// DeleteFromCache 删除用户指定频道的最近会话的缓存
	DeleteFromCache(uid, fakeChannelId string, channelType uint8)
	// GetFromCache 从缓存中获取用户的某一类型的最近会话集合
	GetFromCache(uid string, conversationType wkdb.ConversationType) []wkdb.Conversation
	// CacheCount 最近会话缓存数量
	CacheCount() int
}
