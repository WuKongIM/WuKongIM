package management

import (
	"context"
	"errors"
	"strings"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrRecentConversationsUnavailable reports that manager conversation reads are not wired.
var ErrRecentConversationsUnavailable = errors.New("management: recent conversations unavailable")

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
	// Items contains conversations ordered by the conversation sync usecase.
	Items []RecentConversation
}

// RecentConversation is one manager-facing recent conversation row.
type RecentConversation struct {
	// UID is the owner user for this conversation row.
	UID string
	// ChannelID is the display channel id returned by conversation sync.
	ChannelID string
	// ChannelType is the WuKong channel type.
	ChannelType uint8
	// Unread counts unread messages for UID in this conversation.
	Unread int
	// Timestamp is the latest message timestamp in Unix seconds.
	Timestamp int64
	// LastMsgSeq is the latest message sequence known to conversation sync.
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

	syncLimit := req.Limit + 1
	result, err := a.conversations.Sync(ctx, conversationusecase.SyncQuery{
		UID:        uid,
		Limit:      syncLimit,
		MsgCount:   req.MsgCount,
		OnlyUnread: req.OnlyUnread,
	})
	if err != nil {
		return RecentConversationsResponse{}, err
	}

	conversations := result.Conversations
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
		resp.Items = append(resp.Items, recentConversationFromSync(uid, item))
	}
	return resp, nil
}

func recentConversationFromSync(uid string, item conversationusecase.SyncConversation) RecentConversation {
	return RecentConversation{
		UID:             uid,
		ChannelID:       item.ChannelID,
		ChannelType:     item.ChannelType,
		Unread:          item.Unread,
		Timestamp:       item.Timestamp,
		LastMsgSeq:      item.LastMsgSeq,
		LastClientMsgNo: item.LastClientMsgNo,
		ReadToMsgSeq:    item.ReadToMsgSeq,
		Version:         item.Version,
		RecentMessages:  messagesFromChannelMessages(item.Recents),
	}
}

func messagesFromChannelMessages(items []channel.Message) []Message {
	out := make([]Message, 0, len(items))
	for _, item := range items {
		out = append(out, messageFromChannelMessage(item))
	}
	return out
}

func messageFromChannelMessage(item channel.Message) Message {
	return Message{
		MessageID:   item.MessageID,
		MessageSeq:  item.MessageSeq,
		ClientMsgNo: item.ClientMsgNo,
		ChannelID:   item.ChannelID,
		ChannelType: int64(item.ChannelType),
		FromUID:     item.FromUID,
		Timestamp:   int64(item.Timestamp),
		Payload:     append([]byte(nil), item.Payload...),
	}
}
