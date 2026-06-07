package cluster

import (
	"context"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ConversationNode exposes clusterv2 Slot metadata reads for conversation lists.
type ConversationNode interface {
	ListUserChannelMembershipPage(context.Context, string, metadb.UserChannelMembershipCursor, int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error)
	GetChannelLatestBatch(context.Context, []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelLatest, error)
}

// ConversationStore adapts clusterv2 Slot metadata reads to the conversation usecase ports.
type ConversationStore struct {
	node ConversationNode
}

var _ conversationusecase.MembershipStore = (*ConversationStore)(nil)
var _ conversationusecase.LatestStore = (*ConversationStore)(nil)

// NewConversationStore creates a clusterv2-backed conversation store.
func NewConversationStore(node ConversationNode) *ConversationStore {
	return &ConversationStore{node: node}
}

// ListUserChannelMembershipPage reads UID-owned membership rows.
func (s *ConversationStore) ListUserChannelMembershipPage(ctx context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	if s == nil || s.node == nil {
		return nil, metadb.UserChannelMembershipCursor{}, true, metadb.ErrNotFound
	}
	return s.node.ListUserChannelMembershipPage(ctx, uid, after, limit)
}

// GetChannelLatestBatch reads existing channel-owned latest projection rows.
func (s *ConversationStore) GetChannelLatestBatch(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelLatest, error) {
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	return s.node.GetChannelLatestBatch(ctx, append([]metadb.ConversationKey(nil), keys...))
}
