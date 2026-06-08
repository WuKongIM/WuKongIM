package cluster

import (
	"context"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ConversationProjectionNode exposes clusterv2 metadata calls needed by conversation projection.
type ConversationProjectionNode interface {
	UpsertUserConversationStatesBatch(context.Context, []metadb.UserConversationState) error
	TouchUserConversationActiveAtBatch(context.Context, []metadb.UserConversationActivePatch) error
	ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error)
}

// ConversationProjectionStore adapts clusterv2 metadata facades to conversation projection ports.
type ConversationProjectionStore struct {
	node ConversationProjectionNode
}

var _ conversationusecase.ConversationBatchStore = (*ConversationProjectionStore)(nil)
var _ conversationusecase.MemberSource = (*ConversationProjectionStore)(nil)

// NewConversationProjectionStore creates a clusterv2-backed projection store.
func NewConversationProjectionStore(node ConversationProjectionNode) *ConversationProjectionStore {
	return &ConversationProjectionStore{node: node}
}

// UpsertUserConversationStatesBatch persists UID-owned conversation state rows.
func (s *ConversationProjectionStore) UpsertUserConversationStatesBatch(ctx context.Context, states []metadb.UserConversationState) error {
	if s == nil || s.node == nil || len(states) == 0 {
		return nil
	}
	return s.node.UpsertUserConversationStatesBatch(ctx, append([]metadb.UserConversationState(nil), states...))
}

// TouchUserConversationActiveAtBatch persists UID-owned active-at patches.
func (s *ConversationProjectionStore) TouchUserConversationActiveAtBatch(ctx context.Context, patches []metadb.UserConversationActivePatch) error {
	if s == nil || s.node == nil || len(patches) == 0 {
		return nil
	}
	return s.node.TouchUserConversationActiveAtBatch(ctx, append([]metadb.UserConversationActivePatch(nil), patches...))
}

// ClassifyMembers reads a bounded subscriber page to decide dense or sparse projection.
func (s *ConversationProjectionStore) ClassifyMembers(ctx context.Context, channelID string, channelType int64, limit int) (conversationusecase.MemberClass, error) {
	if s == nil || s.node == nil || limit <= 0 {
		return conversationusecase.MemberClass{}, nil
	}
	uids, _, done, err := s.node.ListChannelSubscribersPage(ctx, channelID, channelType, "", limit)
	if err != nil {
		return conversationusecase.MemberClass{}, err
	}
	members := make([]conversationusecase.Member, 0, len(uids))
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		members = append(members, conversationusecase.Member{UID: uid})
	}
	return conversationusecase.MemberClass{IsSmall: done && len(members) > 0, Members: members}, nil
}
