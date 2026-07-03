package conversation

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200

	defaultSyncLimit           = 200
	maxSyncLimit               = 500
	defaultSyncActiveScanLimit = 2000
)

var (
	// ErrStoreRequired indicates that the conversation usecase has no storage backend.
	ErrStoreRequired = errors.New("internal/usecase/conversation: store required")
	// ErrInvalidRequest indicates that a list request is malformed.
	ErrInvalidRequest = errors.New("internal/usecase/conversation: invalid request")
)

// Store pages authoritative UID-owned conversation active rows.
type Store interface {
	ListConversationActiveView(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (ActiveViewPage, error)
}

// StateStore reads durable UID-owned conversation rows outside the active view window.
type StateStore interface {
	GetConversationState(ctx context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error)
}

// StateMutationStore persists durable UID-owned conversation read state.
type StateMutationStore interface {
	UpsertConversationStates(ctx context.Context, states []metadb.ConversationState) error
}

// DeleteStore persists durable UID-owned conversation delete barriers.
type DeleteStore interface {
	HideConversations(ctx context.Context, reqs []metadb.ConversationDelete) error
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

// RecentMessageStore reads newest channel messages for legacy-compatible sync responses.
type RecentMessageStore interface {
	GetRecentMessages(ctx context.Context, keys []ConversationKey, limit int) (map[ConversationKey][]SyncMessage, error)
}

// Options contains dependencies and read bounds for the conversation usecase.
type Options struct {
	// Store reads UID-owned active conversation rows.
	Store Store
	// StateStore reads durable UID-owned rows for client-known overlay conversations.
	StateStore StateStore
	// StateMutationStore writes durable UID-owned read cursors.
	StateMutationStore StateMutationStore
	// DeleteStore writes durable UID-owned delete barriers.
	DeleteStore DeleteStore
	// Messages reads the newest visible message for returned rows.
	Messages MessageStore
	// ActiveScanLimit bounds the active rows scanned by legacy-compatible sync.
	ActiveScanLimit int
	// Now returns the current time for mutation timestamps.
	Now func() time.Time
}

// App coordinates entry-agnostic conversation list reads.
type App struct {
	store           Store
	stateStore      StateStore
	stateWriter     StateMutationStore
	deleteStore     DeleteStore
	messages        MessageStore
	activeScanLimit int
	now             func() time.Time
}

// New creates a conversation usecase.
func New(opts Options) *App {
	if opts.ActiveScanLimit <= 0 {
		opts.ActiveScanLimit = defaultSyncActiveScanLimit
	}
	if opts.StateStore == nil {
		if stateStore, ok := opts.Store.(StateStore); ok {
			opts.StateStore = stateStore
		}
	}
	if opts.StateMutationStore == nil {
		if stateWriter, ok := opts.Store.(StateMutationStore); ok {
			opts.StateMutationStore = stateWriter
		}
	}
	if opts.DeleteStore == nil {
		if deleteStore, ok := opts.Store.(DeleteStore); ok {
			opts.DeleteStore = deleteStore
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &App{
		store:           opts.Store,
		stateStore:      opts.StateStore,
		stateWriter:     opts.StateMutationStore,
		deleteStore:     opts.DeleteStore,
		messages:        opts.Messages,
		activeScanLimit: opts.ActiveScanLimit,
		now:             opts.Now,
	}
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
	page, err := a.store.ListConversationActiveView(ctx, metadb.ConversationKindNormal, req.UID, req.Cursor.toMeta(), limit+1)
	if err != nil {
		return ListResult{}, err
	}
	rows := page.Rows
	hasMore := !page.Done
	nextCursor := page.Cursor
	if len(rows) > limit {
		hasMore = true
		rows = rows[:limit]
		nextCursor = cursorFromRow(rows[len(rows)-1])
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
		result.NextCursor = cursorFromMeta(nextCursor)
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

func (c Cursor) toMeta() metadb.ConversationActiveCursor {
	if c == (Cursor{}) {
		return metadb.ConversationActiveCursor{}
	}
	return metadb.ConversationActiveCursor{
		ActiveAt:    c.ActiveAt,
		ChannelID:   c.ChannelID,
		ChannelType: c.ChannelType,
	}
}

func cursorFromMeta(cursor metadb.ConversationActiveCursor) Cursor {
	return Cursor{
		ActiveAt:    cursor.ActiveAt,
		ChannelID:   cursor.ChannelID,
		ChannelType: cursor.ChannelType,
	}
}

func cursorFromRow(row metadb.ConversationState) metadb.ConversationActiveCursor {
	return metadb.ConversationActiveCursor{
		ActiveAt:    row.ActiveAt,
		ChannelID:   row.ChannelID,
		ChannelType: row.ChannelType,
	}
}

func lastVisibleMessageRequests(rows []metadb.ConversationState) []LastVisibleMessageRequest {
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

func conversationsFromRows(rows []metadb.ConversationState, lastMessages map[metadb.ConversationKey]LastMessage) []Conversation {
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

func unreadCount(row metadb.ConversationState, lastMessageSeq uint64) uint64 {
	floor := row.ReadSeq
	if row.DeletedToSeq > floor {
		floor = row.DeletedToSeq
	}
	if lastMessageSeq <= floor {
		return 0
	}
	return lastMessageSeq - floor
}
