package cluster

import (
	"context"
	"errors"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// MembershipConversationNode exposes the UID directory and Channel-leader
// head-read surfaces used to construct transient conversations.
type MembershipConversationNode interface {
	ListUserChannelMembershipPage(context.Context, string, metadb.UserChannelMembershipCursor, int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error)
	ReadChannelConversationHeads(context.Context, []channelruntime.ChannelID, string) ([]clusterchannels.ConversationHeadResult, error)
}

// MembershipMutationNode exposes UID-owned personal membership state.
type MembershipMutationNode interface {
	GetUserChannelMembership(context.Context, string, string, int64) (metadb.UserChannelMembership, bool, error)
	AdvanceUserChannelMembershipReadSeq(context.Context, string, string, int64, uint64, int64) error
	HideUserChannelMembership(context.Context, string, string, int64, uint64, int64) error
	ActivateUserChannelMembership(context.Context, string, string, int64, int64, int64) error
}

// ConversationNode is the complete cluster facade needed by the
// membership-backed conversation use case.
type ConversationNode interface {
	MembershipConversationNode
	MembershipMutationNode
}

// ConversationStore adapts cluster operations to conversation use-case ports.
type ConversationStore struct {
	node ConversationNode
}

var _ conversationusecase.DirectoryStore = (*ConversationStore)(nil)
var _ conversationusecase.HeadHydrator = (*ConversationStore)(nil)
var _ conversationusecase.MembershipMutationStore = (*ConversationStore)(nil)

// NewConversationStore creates a cluster-backed membership directory.
func NewConversationStore(node ConversationNode) *ConversationStore {
	return &ConversationStore{node: node}
}

// SupportsMembershipDirectory reports whether the complete facade is present.
func (s *ConversationStore) SupportsMembershipDirectory() bool {
	return s != nil && s.node != nil
}

// ListUserChannelMembershipPage reads one stable UID directory page.
func (s *ConversationStore) ListUserChannelMembershipPage(ctx context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	if s == nil || s.node == nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, metadb.ErrNotFound
	}
	rows, cursor, done, err := s.node.ListUserChannelMembershipPage(ctx, uid, after, limit)
	if err != nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, err
	}
	return append([]metadb.UserChannelMembership(nil), rows...), cursor, done, nil
}

// HydrateConversationHeads performs one cluster-facade batch. The cluster
// facade groups channel reads by exact Channel Leader and preserves alignment.
func (s *ConversationStore) HydrateConversationHeads(ctx context.Context, uid string, memberships []metadb.UserChannelMembership) ([]conversationusecase.HydrationResult, error) {
	results := make([]conversationusecase.HydrationResult, len(memberships))
	if len(memberships) == 0 {
		return results, nil
	}
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	ids := make([]channelruntime.ChannelID, len(memberships))
	for index, row := range memberships {
		results[index].Key = conversationusecase.ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
		if row.ChannelID == "" || row.ChannelType <= 0 || row.ChannelType > 255 {
			return nil, conversationusecase.ErrInvalidRequest
		}
		ids[index] = channelruntime.ChannelID{ID: row.ChannelID, Type: uint8(row.ChannelType)}
	}
	heads, err := s.node.ReadChannelConversationHeads(ctx, ids, uid)
	if err != nil {
		return nil, err
	}
	if len(heads) != len(memberships) {
		return nil, channelruntime.ErrInvalidConfig
	}
	for index, item := range heads {
		if item.Err != nil {
			switch {
			case errors.Is(item.Err, channelruntime.ErrChannelNotFound):
				results[index].Outcome = conversationusecase.HydrationDelete
			case retryableConversationHeadError(item.Err):
				results[index].Outcome = conversationusecase.HydrationRetryable
			default:
				return nil, item.Err
			}
			continue
		}
		head := item.Head
		results[index].LastCommittedSeq = head.LastCommittedSeq
		results[index].RetentionThroughSeq = head.RetentionThroughSeq
		results[index].CurrentUserLastSendSeq = head.CurrentUserLastSendSeq
		if head.Found {
			message := lastMessageFromChannel(head.Message)
			results[index].LastMessage = &message
			results[index].Outcome = conversationusecase.HydrationOK
		} else {
			results[index].Outcome = conversationusecase.HydrationNoVisibleMessage
		}
	}
	return results, nil
}

// GetUserChannelMembership reads one ordinary membership row.
func (s *ConversationStore) GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	if s == nil || s.node == nil {
		return metadb.UserChannelMembership{}, false, metadb.ErrNotFound
	}
	return s.node.GetUserChannelMembership(ctx, uid, channelID, channelType)
}

func (s *ConversationStore) AdvanceUserChannelMembershipReadSeq(ctx context.Context, uid, channelID string, channelType int64, readSeq uint64, updatedAt int64) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.AdvanceUserChannelMembershipReadSeq(ctx, uid, channelID, channelType, readSeq, updatedAt)
}

func (s *ConversationStore) HideUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64, deletedToSeq uint64, updatedAt int64) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.HideUserChannelMembership(ctx, uid, channelID, channelType, deletedToSeq, updatedAt)
}

func (s *ConversationStore) ActivateUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64, activatedAt, updatedAt int64) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.ActivateUserChannelMembership(ctx, uid, channelID, channelType, activatedAt, updatedAt)
}

func retryableConversationHeadError(err error) bool {
	return errors.Is(err, channelruntime.ErrNotReady) ||
		errors.Is(err, channelruntime.ErrNotLeader) ||
		errors.Is(err, channelruntime.ErrStaleMeta) ||
		errors.Is(err, channelruntime.ErrBackpressured) ||
		errors.Is(err, pkgcluster.ErrRouteNotReady) ||
		errors.Is(err, pkgcluster.ErrNoSlotLeader) ||
		errors.Is(err, pkgcluster.ErrNotLeader) ||
		errors.Is(err, pkgcluster.ErrNotStarted) ||
		errors.Is(err, pkgcluster.ErrStopping) ||
		errors.Is(err, pkgcluster.ErrBackpressured) ||
		errors.Is(err, clusternet.ErrNodeNotFound) ||
		errors.Is(err, clusternet.ErrServiceNotFound) ||
		errors.Is(err, transport.ErrStopped) ||
		errors.Is(err, transport.ErrTimeout) ||
		errors.Is(err, transport.ErrNodeNotFound) ||
		errors.Is(err, transport.ErrQueueFull) ||
		errors.Is(err, transport.ErrDialFailed) ||
		errors.Is(err, transport.ErrBusy) ||
		errors.Is(err, context.DeadlineExceeded)
}

func lastMessageFromChannel(msg channelruntime.Message) conversationusecase.LastMessage {
	return conversationusecase.LastMessage{
		MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, FromUID: msg.FromUID,
		ClientMsgNo: msg.ClientMsgNo, ServerTimestampMS: msg.ServerTimestampMS,
		Payload: append([]byte(nil), msg.Payload...),
	}
}
