package conversation

import (
	"context"
	"errors"
	"sort"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultListLimit           = 50
	maxListLimit               = 200
	defaultMembershipPageLimit = 500
	defaultMaxMembershipScan   = 1000
)

var (
	// ErrStoreRequired indicates that the conversation usecase has no storage backend.
	ErrStoreRequired = errors.New("internalv2/usecase/conversation: store required")
	// ErrInvalidRequest indicates that a list request is malformed.
	ErrInvalidRequest = errors.New("internalv2/usecase/conversation: invalid request")
)

// MembershipStore pages UID-owned channel membership rows.
type MembershipStore interface {
	ListUserChannelMembershipPage(ctx context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error)
}

// LatestStore reads channel-owned latest message projection rows.
type LatestStore interface {
	GetChannelLatestBatch(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelLatest, error)
}

// Options contains dependencies and read bounds for the conversation usecase.
type Options struct {
	// Memberships reads the UID-owned channel set.
	Memberships MembershipStore
	// Latest reads channel-owned latest message projections.
	Latest LatestStore
	// MembershipPageLimit bounds one membership page read from storage.
	MembershipPageLimit int
	// MaxMembershipScan bounds per-request membership rows scanned before sorting.
	MaxMembershipScan int
}

// App coordinates entry-agnostic conversation list reads.
type App struct {
	memberships         MembershipStore
	latest              LatestStore
	membershipPageLimit int
	maxMembershipScan   int
}

// New creates a conversation usecase.
func New(opts Options) *App {
	membershipPageLimit := opts.MembershipPageLimit
	if membershipPageLimit <= 0 {
		membershipPageLimit = defaultMembershipPageLimit
	}
	maxMembershipScan := opts.MaxMembershipScan
	if maxMembershipScan <= 0 {
		maxMembershipScan = defaultMaxMembershipScan
	}
	return &App{
		memberships:         opts.Memberships,
		latest:              opts.Latest,
		membershipPageLimit: membershipPageLimit,
		maxMembershipScan:   maxMembershipScan,
	}
}

// List returns one sorted conversation page for uid.
func (a *App) List(ctx context.Context, req ListRequest) (ListResult, error) {
	if a == nil || a.memberships == nil || a.latest == nil {
		return ListResult{}, ErrStoreRequired
	}
	if err := validateListRequest(req); err != nil {
		return ListResult{}, err
	}
	limit := normalizeListLimit(req.Limit)
	memberships, truncated, err := a.scanMemberships(ctx, req.UID)
	if err != nil {
		return ListResult{}, err
	}
	keys := conversationKeysForMemberships(memberships)
	latestRows, err := a.latest.GetChannelLatestBatch(ctx, keys)
	if err != nil {
		return ListResult{}, err
	}
	items := conversationsFromLatest(latestRows)
	sortConversations(items)
	items = applyConversationCursor(items, req.Cursor)
	result := ListResult{
		Truncated:          truncated,
		ScannedMemberships: len(memberships),
	}
	if len(items) > limit {
		result.HasMore = true
		items = items[:limit]
	}
	result.Items = cloneConversations(items)
	if len(result.Items) > 0 && result.HasMore {
		result.NextCursor = cursorFromConversation(result.Items[len(result.Items)-1])
	}
	return result, nil
}

func (a *App) scanMemberships(ctx context.Context, uid string) ([]metadb.UserChannelMembership, bool, error) {
	var (
		rows      []metadb.UserChannelMembership
		cursor    metadb.UserChannelMembershipCursor
		truncated bool
	)
	for len(rows) < a.maxMembershipScan {
		pageLimit := a.membershipPageLimit
		remaining := a.maxMembershipScan - len(rows)
		if pageLimit > remaining {
			pageLimit = remaining
		}
		page, next, done, err := a.memberships.ListUserChannelMembershipPage(ctx, uid, cursor, pageLimit)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, page...)
		cursor = next
		if done || len(page) == 0 {
			return rows, false, nil
		}
	}
	truncated = true
	return rows, truncated, nil
}

func validateListRequest(req ListRequest) error {
	if req.UID == "" {
		return ErrInvalidRequest
	}
	if req.Limit < 0 || req.Limit > maxListLimit {
		return ErrInvalidRequest
	}
	if req.Cursor != (Cursor{}) && (req.Cursor.ChannelID == "" || req.Cursor.ChannelType == 0) {
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

func conversationKeysForMemberships(memberships []metadb.UserChannelMembership) []metadb.ConversationKey {
	keys := make([]metadb.ConversationKey, 0, len(memberships))
	seen := make(map[metadb.ConversationKey]struct{}, len(memberships))
	for _, membership := range memberships {
		key := metadb.ConversationKey{ChannelID: membership.ChannelID, ChannelType: membership.ChannelType}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func conversationsFromLatest(rows map[metadb.ConversationKey]metadb.ChannelLatest) []Conversation {
	items := make([]Conversation, 0, len(rows))
	for _, latest := range rows {
		items = append(items, Conversation{
			ChannelID:      latest.ChannelID,
			ChannelType:    latest.ChannelType,
			LastMessageID:  latest.LastMessageID,
			LastMessageSeq: latest.LastMessageSeq,
			LastAt:         latest.LastAt,
			FromUID:        latest.FromUID,
			ClientMsgNo:    latest.ClientMsgNo,
			Payload:        append([]byte(nil), latest.Payload...),
			UpdatedAt:      latest.UpdatedAt,
		})
	}
	return items
}

func sortConversations(items []Conversation) {
	sort.Slice(items, func(i, j int) bool {
		return compareConversations(items[i], items[j]) < 0
	})
}

func compareConversations(a, b Conversation) int {
	if a.LastAt != b.LastAt {
		if a.LastAt > b.LastAt {
			return -1
		}
		return 1
	}
	if a.LastMessageSeq != b.LastMessageSeq {
		if a.LastMessageSeq > b.LastMessageSeq {
			return -1
		}
		return 1
	}
	if a.ChannelID != b.ChannelID {
		if a.ChannelID < b.ChannelID {
			return -1
		}
		return 1
	}
	if a.ChannelType != b.ChannelType {
		if a.ChannelType < b.ChannelType {
			return -1
		}
		return 1
	}
	return 0
}

func applyConversationCursor(items []Conversation, cursor Cursor) []Conversation {
	if cursor == (Cursor{}) {
		return items
	}
	for i, item := range items {
		if conversationAfterCursor(item, cursor) {
			return items[i:]
		}
	}
	return nil
}

func conversationAfterCursor(item Conversation, cursor Cursor) bool {
	if item.LastAt != cursor.LastAt {
		return item.LastAt < cursor.LastAt
	}
	if item.LastMessageSeq != cursor.LastMessageSeq {
		return item.LastMessageSeq < cursor.LastMessageSeq
	}
	if item.ChannelID != cursor.ChannelID {
		return item.ChannelID > cursor.ChannelID
	}
	return item.ChannelType > cursor.ChannelType
}

func cursorFromConversation(item Conversation) Cursor {
	return Cursor{
		LastAt:         item.LastAt,
		LastMessageSeq: item.LastMessageSeq,
		ChannelID:      item.ChannelID,
		ChannelType:    item.ChannelType,
	}
}

func cloneConversations(items []Conversation) []Conversation {
	out := make([]Conversation, 0, len(items))
	for _, item := range items {
		item.Payload = append([]byte(nil), item.Payload...)
		out = append(out, item)
	}
	return out
}
