package conversation

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	// ErrLegacyMessageReaderRequired indicates that old sync message hydration is not wired.
	ErrLegacyMessageReaderRequired = errors.New("internal/usecase/conversation: legacy message reader required")
	// ErrLegacyMessageResultMismatch indicates that a batch reader broke positional alignment.
	ErrLegacyMessageResultMismatch = errors.New("internal/usecase/conversation: legacy message result mismatch")
	// ErrLegacyDirectoryDidNotAdvance protects compatibility scans from a broken cursor.
	ErrLegacyDirectoryDidNotAdvance = errors.New("internal/usecase/conversation: legacy directory did not advance")
	// ErrLegacySyncUnresolved prevents old clients from mistaking a temporary
	// Channel failure for a complete conversation response.
	ErrLegacySyncUnresolved = errors.New("internal/usecase/conversation: legacy sync has unresolved conversations")
)

const (
	legacyConversationSyncMaxCandidates = 1000
	legacyMessageBatchMaxItems          = 200
)

// LegacyConversationCursor is one client-reported channel position from the
// legacy last_msg_seqs request parameter.
type LegacyConversationCursor struct {
	// ChannelID is the canonical internal channel identifier.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType uint8
	// LastMessageSeq is the exclusive client message floor.
	LastMessageSeq uint64
}

// LegacySyncRequest preserves the public /conversation/sync compatibility
// contract while keeping its transport encoding out of the use case.
type LegacySyncRequest struct {
	// UID owns the membership directory being synchronized.
	UID string
	// Version preserves the old positive-version incremental switch.
	Version int64
	// ClientLastMessageSeqs contains entry-normalized per-channel cursors.
	ClientLastMessageSeqs []LegacyConversationCursor
	// MessageCount bounds recent messages returned per conversation.
	MessageCount int
	// OnlyUnread enables the old effective-read-floor mode.
	OnlyUnread bool
	// ExcludeChannelTypes contains legacy channel categories to omit unless explicitly known by the client.
	ExcludeChannelTypes []uint8
	// Page is the old one-based page; zero disables page slicing.
	Page int
	// PageSize is normalized to the old default of 100 and maximum of 500 when paging.
	PageSize int
}

// LegacyMessageFlags contains durable header flags exposed to older clients.
type LegacyMessageFlags struct {
	// NoPersist mirrors the legacy message header bit.
	NoPersist bool
	// RedDot mirrors the legacy message header bit.
	RedDot bool
	// SyncOnce mirrors the legacy message header bit.
	SyncOnce bool
}

// LegacyMessageEventMeta is the entry-independent event summary embedded in a
// legacy stream message.
type LegacyMessageEventMeta struct {
	// HasEvents reports whether compact event lanes exist.
	HasEvents bool
	// Completed reports whether the reserved finish lane exists.
	Completed bool
	// EventVersion is the latest compatible event projection version.
	EventVersion uint64
	// LastMsgEventSeq is the greatest sequence among returned event lanes.
	LastMsgEventSeq uint64
	// EventCount is the number of non-finish event lanes.
	EventCount int
	// OpenEventCount is the number of event lanes still open.
	OpenEventCount int
	// Events contains event lanes in stable key order.
	Events []LegacyMessageEventKeyMeta
}

// LegacyMessageEventKeyMeta is one compact legacy stream event lane.
type LegacyMessageEventKeyMeta struct {
	// EventKey identifies the event lane.
	EventKey string
	// Status is the projected lane status.
	Status string
	// LastMsgEventSeq is the latest sequence applied to this lane.
	LastMsgEventSeq uint64
	// EndReason is the legacy terminal reason.
	EndReason uint8
	// Error is the legacy terminal error text.
	Error string
	// Snapshot is the decoded full-mode event state.
	Snapshot any
}

// LegacyMessageEventSyncHint identifies the fine-grained event sync cursor.
type LegacyMessageEventSyncHint struct {
	// ClientMsgNo identifies the base message.
	ClientMsgNo string
	// FromMsgEventSeq is the first event sequence a client should request.
	FromMsgEventSeq uint64
}

// LegacyRecentMessage is one message embedded in a legacy conversation row.
type LegacyRecentMessage struct {
	// Flags contains legacy frame-header flags.
	Flags LegacyMessageFlags
	// Setting contains legacy message setting bits.
	Setting uint8
	// MessageID is the durable global message identifier.
	MessageID uint64
	// ClientMsgNo is the client idempotency identifier.
	ClientMsgNo string
	// MessageSeq is the committed channel sequence.
	MessageSeq uint64
	// FromUID is the original sender identifier.
	FromUID string
	// ChannelID is the canonical internal channel identifier.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType uint8
	// Topic is the optional legacy topic.
	Topic string
	// Expire is the legacy expiry value.
	Expire uint32
	// Timestamp is the server append time in Unix seconds.
	Timestamp int32
	// Payload is immutable base-message content owned by this result.
	Payload []byte
	// End is the legacy stream terminal marker.
	End uint8
	// EndReason is the legacy stream terminal reason.
	EndReason uint8
	// Error is the legacy stream terminal error.
	Error string
	// StreamData is the legacy main-lane compact snapshot.
	StreamData []byte
	// EventMeta contains the full legacy stream event summary when present.
	EventMeta *LegacyMessageEventMeta
	// EventHint points to the compatible fine-grained event cursor.
	EventHint *LegacyMessageEventSyncHint
}

// LegacyMessageQuery requests the newest bounded messages after one exclusive
// client or badge position.
type LegacyMessageQuery struct {
	// ChannelID is the canonical channel to read.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType uint8
	// AfterMessageSeq is an exclusive lower message bound.
	AfterMessageSeq uint64
	// Limit bounds returned recent messages.
	Limit int
}

// LegacyMessageReadResult is aligned with one LegacyMessageQuery.
type LegacyMessageReadResult struct {
	// ChannelID preserves positional query identity.
	ChannelID string
	// ChannelType preserves positional query identity.
	ChannelType uint8
	// Messages are ordered by ascending sequence at this port.
	Messages []LegacyRecentMessage
	// Err is scoped to this channel read.
	Err error
}

// LegacyMessageReader performs one bounded committed batch read.
type LegacyMessageReader interface {
	// ReadLegacyMessagesBatch returns one aligned result for every query.
	ReadLegacyMessagesBatch(context.Context, string, []LegacyMessageQuery) ([]LegacyMessageReadResult, error)
}

// LegacyConversation is the entry-independent form of one old sync response.
type LegacyConversation struct {
	// ChannelID is the canonical internal channel identifier.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType uint8
	// Unread is the effective unread badge count.
	Unread uint64
	// Timestamp is the newest returned message time in Unix seconds.
	Timestamp int64
	// LastMessageSeq is the newest returned message sequence.
	LastMessageSeq uint64
	// LastClientMsgNo is the newest returned client message identifier.
	LastClientMsgNo string
	// OffsetMessageSeq preserves the retired legacy response field.
	OffsetMessageSeq int64
	// ReadToMessageSeq is the effective badge floor.
	ReadToMessageSeq uint64
	// Version is the newest returned message timestamp in Unix nanoseconds.
	Version int64
	// Recents contains newest-first compatible messages.
	Recents []LegacyRecentMessage
}

// LegacySyncResult contains the old endpoint's array rows.
type LegacySyncResult struct {
	// Items is serialized as the old raw conversation array.
	Items []LegacyConversation
}

// LegacySyncer is implemented by a conversation application with the legacy
// message-read port wired by the composition root.
type LegacySyncer interface {
	SyncLegacy(context.Context, LegacySyncRequest) (LegacySyncResult, error)
}

// SyncLegacy builds the legacy conversation array from the current durable
// membership directory and committed Channel messages.
func (a *App) SyncLegacy(ctx context.Context, req LegacySyncRequest) (LegacySyncResult, error) {
	uid := strings.TrimSpace(req.UID)
	if uid == "" {
		return LegacySyncResult{}, ErrInvalidRequest
	}
	result := LegacySyncResult{Items: make([]LegacyConversation, 0)}
	if req.MessageCount <= 0 {
		return result, nil
	}
	if a == nil || a.legacyMessages == nil {
		return LegacySyncResult{}, ErrLegacyMessageReaderRequired
	}
	items, err := a.listLegacyConversationCandidates(ctx, uid)
	if err != nil {
		return LegacySyncResult{}, err
	}
	cursors := legacyConversationCursorMap(req.ClientLastMessageSeqs)
	items = legacyConversationPage(items, req.Page, req.PageSize)
	excludedTypes := legacyExcludedChannelTypes(req.ExcludeChannelTypes)
	selected := make([]Conversation, 0, len(items))
	queries := make([]LegacyMessageQuery, 0, len(items))
	for _, item := range items {
		key := ConversationKey{ChannelID: item.ChannelID, ChannelType: item.ChannelType}
		clientSeq, clientKnowsChannel := cursors[key]
		if _, excluded := excludedTypes[uint8(item.ChannelType)]; excluded && !clientKnowsChannel {
			continue
		}
		if req.OnlyUnread && item.Unread == 0 && !clientKnowsChannel {
			continue
		}
		afterMessageSeq := clientSeq
		if (!clientKnowsChannel || clientSeq == 0) && (req.OnlyUnread || req.Version > 0) {
			afterMessageSeq = legacyEffectiveReadSeq(item)
		}
		selected = append(selected, item)
		queries = append(queries, LegacyMessageQuery{
			ChannelID: item.ChannelID, ChannelType: uint8(item.ChannelType),
			AfterMessageSeq: afterMessageSeq,
			Limit:           req.MessageCount,
		})
	}
	if len(queries) == 0 {
		return result, nil
	}
	readResults := make([]LegacyMessageReadResult, 0, len(queries))
	for start := 0; start < len(queries); start += legacyMessageBatchMaxItems {
		end := min(start+legacyMessageBatchMaxItems, len(queries))
		batch, err := a.legacyMessages.ReadLegacyMessagesBatch(ctx, uid, queries[start:end])
		if err != nil {
			return LegacySyncResult{}, err
		}
		if len(batch) != end-start {
			return LegacySyncResult{}, ErrLegacyMessageResultMismatch
		}
		readResults = append(readResults, batch...)
	}
	if len(readResults) != len(selected) {
		return LegacySyncResult{}, ErrLegacyMessageResultMismatch
	}
	for index, item := range selected {
		readResult := readResults[index]
		if readResult.ChannelID != item.ChannelID || int64(readResult.ChannelType) != item.ChannelType {
			return LegacySyncResult{}, ErrLegacyMessageResultMismatch
		}
		if readResult.Err != nil {
			return LegacySyncResult{}, readResult.Err
		}
		messages := cloneLegacyRecentMessages(readResult.Messages)
		if len(messages) == 0 {
			continue
		}
		reverseLegacyRecentMessages(messages)
		last := messages[0]
		result.Items = append(result.Items, LegacyConversation{
			ChannelID: item.ChannelID, ChannelType: uint8(item.ChannelType), Unread: item.Unread,
			Timestamp: int64(last.Timestamp), LastMessageSeq: last.MessageSeq,
			LastClientMsgNo: last.ClientMsgNo, ReadToMessageSeq: legacyEffectiveReadSeq(item),
			Version: time.Unix(int64(last.Timestamp), 0).UnixNano(), Recents: messages,
		})
	}
	return result, nil
}

func legacyEffectiveReadSeq(item Conversation) uint64 {
	readSeq := item.ReadSeq
	if item.LastMessage == nil || item.LastMessage.MessageSeq < item.Unread {
		return readSeq
	}
	effective := item.LastMessage.MessageSeq - item.Unread
	if effective > readSeq {
		return effective
	}
	return readSeq
}

func (a *App) listLegacyConversationCandidates(ctx context.Context, uid string) ([]Conversation, error) {
	items := make([]Conversation, 0, maxListLimit)
	cursor := Cursor{}
	consumed := 0
	for consumed < legacyConversationSyncMaxCandidates {
		limit := min(maxListLimit, legacyConversationSyncMaxCandidates-consumed)
		page, err := a.List(ctx, ListRequest{UID: uid, Cursor: cursor, Limit: limit})
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if len(page.Unresolved) > 0 {
			retry, err := a.Retry(ctx, RetryRequest{UID: uid, Keys: page.Unresolved})
			if err != nil {
				return nil, err
			}
			if len(retry.Unresolved) > 0 {
				return nil, ErrLegacySyncUnresolved
			}
			items = append(items, retry.Items...)
		}
		consumed += max(1, page.ScannedCandidates)
		if page.Done {
			sortLegacyConversations(items)
			return items, nil
		}
		if page.NextCursor.ChannelID == "" || page.NextCursor == cursor {
			return nil, ErrLegacyDirectoryDidNotAdvance
		}
		cursor = page.NextCursor
	}
	sortLegacyConversations(items)
	return items, nil
}

func sortLegacyConversations(items []Conversation) {
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].ActiveAt != items[right].ActiveAt {
			return items[left].ActiveAt > items[right].ActiveAt
		}
		if items[left].ChannelID != items[right].ChannelID {
			return items[left].ChannelID < items[right].ChannelID
		}
		return items[left].ChannelType < items[right].ChannelType
	})
}

func legacyConversationPage(items []Conversation, page, pageSize int) []Conversation {
	if page <= 0 {
		return items
	}
	if pageSize <= 0 {
		pageSize = 100
	} else if pageSize > 500 {
		pageSize = 500
	}
	start64 := int64(page-1) * int64(pageSize)
	if start64 < 0 || start64 >= int64(len(items)) {
		return nil
	}
	end64 := start64 + int64(pageSize)
	if end64 > int64(len(items)) {
		end64 = int64(len(items))
	}
	return items[int(start64):int(end64)]
}

func legacyExcludedChannelTypes(types []uint8) map[uint8]struct{} {
	result := make(map[uint8]struct{}, len(types))
	for _, channelType := range types {
		result[channelType] = struct{}{}
	}
	return result
}

func legacyConversationCursorMap(cursors []LegacyConversationCursor) map[ConversationKey]uint64 {
	result := make(map[ConversationKey]uint64, len(cursors))
	for _, cursor := range cursors {
		channelID := strings.TrimSpace(cursor.ChannelID)
		if channelID == "" || cursor.ChannelType == 0 {
			continue
		}
		result[ConversationKey{ChannelID: channelID, ChannelType: int64(cursor.ChannelType)}] = cursor.LastMessageSeq
	}
	return result
}

func cloneLegacyRecentMessages(messages []LegacyRecentMessage) []LegacyRecentMessage {
	result := make([]LegacyRecentMessage, len(messages))
	copy(result, messages)
	for index := range result {
		result[index].Payload = append([]byte(nil), messages[index].Payload...)
		result[index].StreamData = append([]byte(nil), messages[index].StreamData...)
		result[index].EventMeta = cloneLegacyMessageEventMeta(messages[index].EventMeta)
		if messages[index].EventHint != nil {
			hint := *messages[index].EventHint
			result[index].EventHint = &hint
		}
	}
	return result
}

func cloneLegacyMessageEventMeta(meta *LegacyMessageEventMeta) *LegacyMessageEventMeta {
	if meta == nil {
		return nil
	}
	result := *meta
	result.Events = append([]LegacyMessageEventKeyMeta(nil), meta.Events...)
	return &result
}

func reverseLegacyRecentMessages(messages []LegacyRecentMessage) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}
