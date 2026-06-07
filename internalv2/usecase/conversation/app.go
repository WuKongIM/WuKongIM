package conversation

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

var (
	// ErrStoreRequired indicates that the conversation usecase has no storage backend.
	ErrStoreRequired = errors.New("internalv2/usecase/conversation: store required")
	// ErrInvalidRequest indicates that a list request is malformed.
	ErrInvalidRequest = errors.New("internalv2/usecase/conversation: invalid request")
	// ErrProjectorConfig indicates that the conversation projector is missing required dependencies.
	ErrProjectorConfig = errors.New("internalv2/usecase/conversation: projector config invalid")
)

// Store pages UID-owned conversation active rows.
type Store interface {
	ListUserConversationActivePage(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
}

// LastVisibleMessageRequest identifies one channel tail read and its visibility floor.
type LastVisibleMessageRequest struct {
	// ChannelID identifies the message log to read.
	ChannelID string
	// ChannelType identifies the message log namespace.
	ChannelType int64
	// VisibleAfterSeq hides messages at or below this sequence.
	VisibleAfterSeq uint64
}

// MessageStore reads channel-owned message log tails for the current page.
type MessageStore interface {
	GetLastVisibleMessages(ctx context.Context, requests []LastVisibleMessageRequest) (map[metadb.ConversationKey]LastMessage, error)
}

// Options contains dependencies and read bounds for the conversation usecase.
type Options struct {
	// Store reads UID-owned active conversation rows.
	Store Store
	// Messages reads the newest visible message for returned rows.
	Messages MessageStore
}

// App coordinates entry-agnostic conversation list reads.
type App struct {
	store    Store
	messages MessageStore
}

// New creates a conversation usecase.
func New(opts Options) *App {
	return &App{store: opts.Store, messages: opts.Messages}
}

// List returns one active-index conversation page for uid.
func (a *App) List(ctx context.Context, req ListRequest) (ListResult, error) {
	if a == nil || a.store == nil || a.messages == nil {
		return ListResult{}, ErrStoreRequired
	}
	if err := validateListRequest(req); err != nil {
		return ListResult{}, err
	}
	limit := normalizeListLimit(req.Limit)
	rows, cursor, done, err := a.store.ListUserConversationActivePage(ctx, req.UID, req.Cursor.toMeta(), limit+1)
	if err != nil {
		return ListResult{}, err
	}
	hasMore := !done
	if len(rows) > limit {
		hasMore = true
		rows = rows[:limit]
	}
	lastMessages, err := a.messages.GetLastVisibleMessages(ctx, lastVisibleMessageRequests(rows))
	if err != nil {
		return ListResult{}, err
	}
	items := conversationsFromRows(rows, lastMessages)
	result := ListResult{
		Items:   items,
		HasMore: hasMore,
	}
	if hasMore {
		if len(items) > 0 {
			result.NextCursor = cursorFromConversation(items[len(items)-1])
		} else {
			result.NextCursor = cursorFromMeta(cursor)
		}
	}
	return result, nil
}

func validateListRequest(req ListRequest) error {
	if req.UID == "" {
		return ErrInvalidRequest
	}
	if req.Limit < 0 || req.Limit > maxListLimit {
		return ErrInvalidRequest
	}
	if req.Cursor != (Cursor{}) && (req.Cursor.ActiveAt <= 0 || req.Cursor.ChannelID == "" || req.Cursor.ChannelType == 0) {
		return ErrInvalidRequest
	}
	return nil
}

func normalizeListLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	return limit
}

func (c Cursor) toMeta() metadb.UserConversationActiveCursor {
	if c == (Cursor{}) {
		return metadb.UserConversationActiveCursor{}
	}
	return metadb.UserConversationActiveCursor{
		ActiveAt:    c.ActiveAt,
		ChannelID:   c.ChannelID,
		ChannelType: c.ChannelType,
	}
}

func cursorFromMeta(cursor metadb.UserConversationActiveCursor) Cursor {
	return Cursor{
		ActiveAt:    cursor.ActiveAt,
		ChannelID:   cursor.ChannelID,
		ChannelType: cursor.ChannelType,
	}
}

func cursorFromConversation(item Conversation) Cursor {
	return Cursor{
		ActiveAt:    item.ActiveAt,
		ChannelID:   item.ChannelID,
		ChannelType: item.ChannelType,
	}
}

func lastVisibleMessageRequests(rows []metadb.UserConversationState) []LastVisibleMessageRequest {
	requests := make([]LastVisibleMessageRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, LastVisibleMessageRequest{
			ChannelID:       row.ChannelID,
			ChannelType:     row.ChannelType,
			VisibleAfterSeq: row.DeletedToSeq,
		})
	}
	return requests
}

func conversationsFromRows(rows []metadb.UserConversationState, lastMessages map[metadb.ConversationKey]LastMessage) []Conversation {
	items := make([]Conversation, 0, len(rows))
	for _, row := range rows {
		key := metadb.ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
		var last *LastMessage
		var unread uint64
		if msg, ok := lastMessages[key]; ok {
			msg.Payload = append([]byte(nil), msg.Payload...)
			last = &msg
			unread = unreadCount(row, msg.MessageSeq)
		}
		items = append(items, Conversation{
			ChannelID:    row.ChannelID,
			ChannelType:  row.ChannelType,
			ActiveAt:     row.ActiveAt,
			ReadSeq:      row.ReadSeq,
			DeletedToSeq: row.DeletedToSeq,
			SparseActive: row.SparseActive,
			UpdatedAt:    row.UpdatedAt,
			LastMessage:  last,
			Unread:       unread,
		})
	}
	return items
}

func unreadCount(row metadb.UserConversationState, lastMessageSeq uint64) uint64 {
	floor := row.ReadSeq
	if row.DeletedToSeq > floor {
		floor = row.DeletedToSeq
	}
	if lastMessageSeq <= floor {
		return 0
	}
	return lastMessageSeq - floor
}
