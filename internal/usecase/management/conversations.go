package management

import (
	"context"
	"errors"
	"strings"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrRecentConversationsUnavailable reports that manager conversation reads are not wired.
var ErrRecentConversationsUnavailable = errors.New("management: recent conversations unavailable")

// ConversationLister exposes membership-backed conversation construction for manager pages.
type ConversationLister interface {
	List(ctx context.Context, request conversationusecase.ListRequest) (conversationusecase.ListResult, error)
}

// RecentConversationsRequest configures one manager recent-conversation query.
type RecentConversationsRequest struct {
	// UID identifies the user whose recent conversations should be listed.
	UID string
	// Limit caps the number of returned conversations.
	Limit int
	// MsgCount caps the number of recent messages embedded per conversation.
	MsgCount int
	// OnlyUnread filters the working set to conversations with unread messages.
	OnlyUnread bool
}

// RecentConversationsResponse contains one bounded manager recent-conversation result.
type RecentConversationsResponse struct {
	// UID is the normalized queried user id.
	UID string
	// Limit is the applied conversation limit.
	Limit int
	// MsgCount is the applied recent-message preview limit.
	MsgCount int
	// OnlyUnread reports whether unread filtering was applied.
	OnlyUnread bool
	// Truncated reports whether more matching conversations were detected.
	Truncated bool
	// Items contains conversations ordered by membership activation priority.
	Items []RecentConversation
}

// RecentConversation is one manager-facing recent conversation row.
type RecentConversation struct {
	// UID is the owner user for this conversation row.
	UID string
	// ChannelID is the display channel id returned by transient construction.
	ChannelID string
	// ChannelType is the WuKong channel type.
	ChannelType uint8
	// Unread counts unread messages for UID in this conversation.
	Unread int
	// Timestamp is the latest message timestamp in Unix seconds.
	Timestamp int64
	// LastMsgSeq is the latest committed message sequence in the constructed view.
	LastMsgSeq uint32
	// LastClientMsgNo is the latest client message number when present.
	LastClientMsgNo string
	// ReadToMsgSeq is UID's read cursor for this conversation.
	ReadToMsgSeq uint32
	// Version is the sync compatibility version timestamp.
	Version int64
	// RecentMessages contains newest message previews for this conversation.
	RecentMessages []Message
}

// Message is the manager-facing channel message DTO.
type Message struct {
	// MessageID is the durable message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence number.
	MessageSeq uint64
	// ClientMsgNo is the client-provided message correlation number.
	ClientMsgNo string
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType int64
	// FromUID is the sender UID recorded on the message.
	FromUID string
	// Timestamp is the server-side message timestamp in Unix seconds.
	Timestamp int64
	// Payload is the raw message payload bytes.
	Payload []byte
}

// ListRecentConversations returns one bounded UID-scoped recent conversation working set.
func (a *App) ListRecentConversations(ctx context.Context, req RecentConversationsRequest) (RecentConversationsResponse, error) {
	uid := strings.TrimSpace(req.UID)
	maxInt := int(^uint(0) >> 1)
	if uid == "" || req.Limit <= 0 || req.Limit >= maxInt || req.MsgCount < 0 {
		return RecentConversationsResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.conversations == nil {
		return RecentConversationsResponse{}, ErrRecentConversationsUnavailable
	}

	result, err := a.conversations.List(ctx, conversationusecase.ListRequest{UID: uid, Limit: req.Limit + 1})
	if err != nil {
		return RecentConversationsResponse{}, err
	}

	conversations := make([]conversationusecase.Conversation, 0, len(result.Items))
	for _, item := range result.Items {
		if req.OnlyUnread && item.Unread == 0 {
			continue
		}
		conversations = append(conversations, item)
	}
	truncated := len(conversations) > req.Limit
	if truncated {
		conversations = conversations[:req.Limit]
	}
	resp := RecentConversationsResponse{
		UID:        uid,
		Limit:      req.Limit,
		MsgCount:   req.MsgCount,
		OnlyUnread: req.OnlyUnread,
		Truncated:  truncated,
		Items:      make([]RecentConversation, 0, len(conversations)),
	}
	for _, item := range conversations {
		resp.Items = append(resp.Items, recentConversationFromMembership(uid, item, req.MsgCount))
	}
	return resp, nil
}

func recentConversationFromMembership(uid string, item conversationusecase.Conversation, msgCount int) RecentConversation {
	result := RecentConversation{
		UID: uid, ChannelID: item.ChannelID, ChannelType: uint8(item.ChannelType),
		Unread: boundedInt(item.Unread), ReadToMsgSeq: boundedUint32(item.ReadSeq), Version: item.UpdatedAt,
	}
	if item.LastMessage == nil {
		return result
	}
	result.Timestamp = item.LastMessage.ServerTimestampMS / 1000
	result.LastMsgSeq = boundedUint32(item.LastMessage.MessageSeq)
	result.LastClientMsgNo = item.LastMessage.ClientMsgNo
	if msgCount > 0 {
		result.RecentMessages = []Message{{
			MessageID: item.LastMessage.MessageID, MessageSeq: item.LastMessage.MessageSeq,
			ClientMsgNo: item.LastMessage.ClientMsgNo, ChannelID: item.ChannelID,
			ChannelType: item.ChannelType, FromUID: item.LastMessage.FromUID,
			Timestamp: item.LastMessage.ServerTimestampMS / 1000,
			Payload:   append([]byte(nil), item.LastMessage.Payload...),
		}}
	}
	return result
}

func boundedUint32(value uint64) uint32 {
	if value > uint64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(value)
}

func boundedInt(value uint64) int {
	max := uint64(^uint(0) >> 1)
	if value > max {
		return int(max)
	}
	return int(value)
}
