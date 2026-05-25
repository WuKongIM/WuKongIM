package conversation

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type ConversationStateStore interface {
	GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, error)
	UpsertUserConversationStates(ctx context.Context, states []metadb.UserConversationState) error
	ListUserConversationActive(ctx context.Context, uid string, limit int) ([]metadb.UserConversationState, error)
}

type ConversationDeleteStore interface {
	HideUserConversations(ctx context.Context, reqs []metadb.UserConversationDelete) error
	RemoveUserConversationActiveHints(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error
}

type MessageFactsStore interface {
	LoadLatestMessages(ctx context.Context, keys []ConversationKey) (map[ConversationKey]channel.Message, error)
	LoadRecentMessages(ctx context.Context, key ConversationKey, limit int) ([]channel.Message, error)
}

type ProjectorStore interface {
	SubmitUserConversationActiveHints(ctx context.Context, hints []metadb.UserConversationActiveHint) error
	ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
}
