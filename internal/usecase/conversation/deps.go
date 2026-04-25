package conversation

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

type ConversationStateStore interface {
	GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, error)
	ListUserConversationActive(ctx context.Context, uid string, limit int) ([]metadb.UserConversationState, error)
	ScanUserConversationStatePage(ctx context.Context, uid string, after metadb.ConversationCursor, limit int) ([]metadb.UserConversationState, metadb.ConversationCursor, bool, error)
	ClearUserConversationActiveAt(ctx context.Context, uid string, keys []metadb.ConversationKey) error
}

type ChannelUpdateStore interface {
	BatchGetChannelUpdateLogs(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error)
}

type MessageFactsStore interface {
	LoadLatestMessages(ctx context.Context, keys []ConversationKey) (map[ConversationKey]channel.Message, error)
	LoadRecentMessages(ctx context.Context, key ConversationKey, limit int) ([]channel.Message, error)
}

type ProjectorStore interface {
	BatchGetChannelUpdateLogs(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error)
	UpsertChannelUpdateLogs(ctx context.Context, entries []metadb.ChannelUpdateLog) error
	TouchUserConversationActiveAt(ctx context.Context, patches []metadb.UserConversationActivePatch) error
	ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
}
