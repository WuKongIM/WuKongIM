package message

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const (
	defaultSyncMessagesLimit  = 100
	maxSyncMessagesLimit      = 10000
	maxSyncMessagesBatchItems = 200
	legacySettingStream       = 1 << 1
)

// PullMode selects the compatible /channel/messagesync direction.
type PullMode uint8

const (
	// PullModeDown pulls older messages at or before start_message_seq.
	PullModeDown PullMode = iota
	// PullModeUp pulls newer messages at or after start_message_seq.
	PullModeUp
)

// MessageFlags carries legacy message header flags for compatible HTTP responses.
type MessageFlags struct {
	// NoPersist reports whether the message was marked as non-durable.
	NoPersist bool
	// RedDot reports whether the message should affect unread red-dot state.
	RedDot bool
	// SyncOnce reports whether the message was sent as a one-shot sync command.
	SyncOnce bool
}

// SyncedMessage is a channel message returned by the compatible sync usecase.
type SyncedMessage struct {
	// Flags contains the legacy framer flags exposed in HTTP responses.
	Flags MessageFlags
	// Setting is the legacy message setting bitset.
	Setting uint8
	// MessageID is the durable message id.
	MessageID uint64
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// MessageSeq is the committed channel sequence.
	MessageSeq uint64
	// FromUID is the sender user id.
	FromUID string
	// ChannelID is the normalized channel id.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// Topic is the optional message topic.
	Topic string
	// Expire is the legacy expiration value.
	Expire uint32
	// Timestamp is the legacy message timestamp.
	Timestamp int32
	// Payload is the immutable message payload.
	Payload []byte
	// End reports the legacy stream terminal marker.
	End uint8
	// EndReason stores the stream terminal reason when present.
	EndReason uint8
	// Error stores the stream terminal error when present.
	Error string
	// StreamData stores the legacy compact stream payload derived from the main event lane.
	StreamData []byte
	// EventMeta is the compact event lane summary for compatible clients.
	EventMeta *MessageEventMeta
	// EventHint points to the message-level event sync cursor for compatible clients.
	EventHint *MessageEventSyncHint
}

// SyncChannelMessagesQuery describes a compatible channel message sync request.
type SyncChannelMessagesQuery struct {
	// LoginUID is the current logged-in user, used for person-channel normalization.
	LoginUID string
	// ChannelID is the client-facing channel identifier.
	ChannelID string
	// ChannelType is the client-facing channel type.
	ChannelType uint8
	// StartMessageSeq is the inclusive starting sequence boundary.
	StartMessageSeq uint64
	// EndMessageSeq is the exclusive ending sequence boundary.
	EndMessageSeq uint64
	// Limit is the maximum number of messages to return.
	Limit int
	// PullMode selects whether to pull older or newer messages.
	PullMode PullMode
	// IncludeEventMeta asks sync to include compact event metadata when available.
	IncludeEventMeta bool
	// EventSummaryMode is accepted for compatibility with older stream-message clients.
	EventSummaryMode string
}

// SyncChannelMessagesResult contains one compatible channel message sync page.
type SyncChannelMessagesResult struct {
	// Messages contains synced messages ordered by ascending message sequence.
	Messages []SyncedMessage
	// More reports whether another page exists inside the requested bounds.
	More bool
}

// ChannelMessageQuery is the storage-facing channel message sync request.
type ChannelMessageQuery struct {
	// ChannelID identifies the normalized channel to scan.
	ChannelID ChannelID
	// StartSeq is the inclusive starting sequence boundary.
	StartSeq uint64
	// EndSeq is the exclusive ending sequence boundary.
	EndSeq uint64
	// MinSeq is the lowest membership-visible sequence.
	MinSeq uint64
	// Limit is the maximum number of messages to return.
	Limit int
	// PullMode selects whether to pull older or newer messages.
	PullMode PullMode
}

// ChannelMessagePage is one authoritative channel message sync page.
type ChannelMessagePage struct {
	// Messages contains synced messages ordered by ascending message sequence.
	Messages []SyncedMessage
	// HasMore reports whether another page exists inside the requested bounds.
	HasMore bool
}

// ChannelMessageReadResult is aligned with one storage-facing batch query.
type ChannelMessageReadResult struct {
	// Page contains one authoritative committed message page on success.
	Page ChannelMessagePage
	// Err is scoped to this channel read and preserves batch alignment.
	Err error
}

// SyncChannelMessagesBatchQuery describes one bounded UID-owned batch pull.
type SyncChannelMessagesBatchQuery struct {
	// LoginUID owns every ordinary membership validated before batch reads.
	LoginUID string
	// Items contains the bounded client-visible channel requests.
	Items []SyncChannelMessagesQuery
}

// SyncChannelMessagesBatchItem preserves one requested channel identity.
type SyncChannelMessagesBatchItem struct {
	// ChannelID preserves the client-facing requested channel id.
	ChannelID string
	// ChannelType preserves the client-facing requested channel type.
	ChannelType uint8
	// Result contains the compatible message page on success.
	Result SyncChannelMessagesResult
	// Err is item-scoped after all memberships pass preflight validation.
	Err error
}

// SyncChannelMessagesBatchResult preserves input ordering.
type SyncChannelMessagesBatchResult struct {
	// Items is positionally aligned with SyncChannelMessagesBatchQuery.Items.
	Items []SyncChannelMessagesBatchItem
}

type preparedSyncChannelMessages struct {
	query     ChannelMessageQuery
	eventMode string
}

// SyncChannelMessages returns a compatible message page for a channel.
func (a *App) SyncChannelMessages(ctx context.Context, query SyncChannelMessagesQuery) (SyncChannelMessagesResult, error) {
	prepared, err := a.prepareSyncChannelMessages(ctx, query)
	if err != nil {
		return SyncChannelMessagesResult{}, err
	}
	page, err := a.reader.SyncMessages(ctx, prepared.query)
	if errors.Is(err, metadb.ErrNotFound) {
		return SyncChannelMessagesResult{Messages: []SyncedMessage{}}, nil
	}
	if err != nil {
		return SyncChannelMessagesResult{}, err
	}
	return a.finishSyncChannelMessages(ctx, prepared.eventMode, page)
}

// SyncChannelMessagesBatch validates every UID-owned membership before
// issuing one cluster-routed, item-aligned message read batch.
func (a *App) SyncChannelMessagesBatch(ctx context.Context, query SyncChannelMessagesBatchQuery) (SyncChannelMessagesBatchResult, error) {
	loginUID := strings.TrimSpace(query.LoginUID)
	if loginUID == "" {
		return SyncChannelMessagesBatchResult{}, ErrSyncLoginUIDRequired
	}
	if len(query.Items) == 0 {
		return SyncChannelMessagesBatchResult{}, ErrSyncBatchItemsRequired
	}
	if len(query.Items) > maxSyncMessagesBatchItems {
		return SyncChannelMessagesBatchResult{}, ErrSyncBatchTooLarge
	}
	batchReader, ok := a.reader.(ChannelMessageBatchReader)
	if !ok {
		return SyncChannelMessagesBatchResult{}, ErrSyncBatchReaderRequired
	}
	prepared := make([]preparedSyncChannelMessages, len(query.Items))
	reads := make([]ChannelMessageQuery, len(query.Items))
	for index, item := range query.Items {
		item.LoginUID = loginUID
		preparedItem, err := a.prepareSyncChannelMessages(ctx, item)
		if err != nil {
			return SyncChannelMessagesBatchResult{}, err
		}
		prepared[index] = preparedItem
		reads[index] = preparedItem.query
	}
	readResults, err := batchReader.SyncMessagesBatch(ctx, reads)
	if err != nil {
		return SyncChannelMessagesBatchResult{}, err
	}
	if len(readResults) != len(query.Items) {
		return SyncChannelMessagesBatchResult{}, ErrSyncBatchResultMismatch
	}
	result := SyncChannelMessagesBatchResult{Items: make([]SyncChannelMessagesBatchItem, len(query.Items))}
	for index, readResult := range readResults {
		item := &result.Items[index]
		item.ChannelID = query.Items[index].ChannelID
		item.ChannelType = query.Items[index].ChannelType
		if errors.Is(readResult.Err, metadb.ErrNotFound) {
			item.Result.Messages = []SyncedMessage{}
			continue
		}
		if readResult.Err != nil {
			item.Err = readResult.Err
			continue
		}
		item.Result, err = a.finishSyncChannelMessages(ctx, prepared[index].eventMode, readResult.Page)
		if err != nil {
			return SyncChannelMessagesBatchResult{}, err
		}
	}
	return result, nil
}

func (a *App) prepareSyncChannelMessages(ctx context.Context, query SyncChannelMessagesQuery) (preparedSyncChannelMessages, error) {
	loginUID := strings.TrimSpace(query.LoginUID)
	if loginUID == "" {
		return preparedSyncChannelMessages{}, ErrSyncLoginUIDRequired
	}
	channelID := strings.TrimSpace(query.ChannelID)
	if channelID == "" {
		return preparedSyncChannelMessages{}, ErrSyncChannelIDRequired
	}
	if query.ChannelType == 0 {
		return preparedSyncChannelMessages{}, ErrSyncChannelTypeRequired
	}
	if query.ChannelType == channelTypePerson {
		normalized, err := runtimechannelid.NormalizePersonChannel(loginUID, channelID)
		if err != nil {
			return preparedSyncChannelMessages{}, err
		}
		channelID = normalized
	}
	if a == nil || a.reader == nil {
		return preparedSyncChannelMessages{}, ErrMessageReaderRequired
	}
	if a.memberships == nil {
		return preparedSyncChannelMessages{}, ErrSyncMembershipRequired
	}
	visibilityMinSeq := uint64(0)
	membership, ok, err := a.memberships.GetUserChannelMembership(ctx, loginUID, channelID, int64(query.ChannelType))
	if err != nil {
		return preparedSyncChannelMessages{}, err
	}
	if !ok || membership.Tombstone {
		return preparedSyncChannelMessages{}, ErrSyncMembershipRequired
	}
	visibilityFloor := membership.DeletedToSeq
	if membership.JoinSeq > 0 && membership.JoinSeq-1 > visibilityFloor {
		visibilityFloor = membership.JoinSeq - 1
	}
	if visibilityFloor != ^uint64(0) {
		visibilityMinSeq = visibilityFloor + 1
	} else {
		visibilityMinSeq = visibilityFloor
	}
	if a.channelState != nil {
		channel, err := a.channelState.GetChannelForMessagePull(ctx, channelID, int64(query.ChannelType))
		if err != nil && !errors.Is(err, metadb.ErrNotFound) {
			return preparedSyncChannelMessages{}, err
		}
		if err == nil && channel.Disband != 0 {
			return preparedSyncChannelMessages{}, ErrSyncChannelDisbanded
		}
	}
	startSeq := query.StartMessageSeq
	if query.PullMode == PullModeUp && visibilityMinSeq > startSeq {
		startSeq = visibilityMinSeq
	}
	return preparedSyncChannelMessages{query: ChannelMessageQuery{
		ChannelID: ChannelID{ID: channelID, Type: query.ChannelType},
		StartSeq:  startSeq,
		EndSeq:    query.EndMessageSeq,
		MinSeq:    visibilityMinSeq,
		Limit:     normalizeSyncMessagesLimit(query.Limit),
		PullMode:  query.PullMode,
	}, eventMode: normalizeEventSummaryMode(query)}, nil
}

func (a *App) finishSyncChannelMessages(ctx context.Context, eventMode string, page ChannelMessagePage) (SyncChannelMessagesResult, error) {
	messages := cloneSyncedMessages(page.Messages)
	if err := a.enrichSyncedMessagesWithEvents(ctx, eventMode, messages); err != nil {
		return SyncChannelMessagesResult{}, err
	}
	return SyncChannelMessagesResult{
		Messages: messages,
		More:     page.HasMore,
	}, nil
}

func normalizeSyncMessagesLimit(limit int) int {
	if limit <= 0 {
		return defaultSyncMessagesLimit
	}
	if limit > maxSyncMessagesLimit {
		return maxSyncMessagesLimit
	}
	return limit
}

func cloneSyncedMessages(in []SyncedMessage) []SyncedMessage {
	out := make([]SyncedMessage, len(in))
	copy(out, in)
	for i := range out {
		out[i].Payload = cloneBytes(out[i].Payload)
		out[i].StreamData = cloneBytes(out[i].StreamData)
		out[i].EventMeta = cloneMessageEventMeta(out[i].EventMeta)
		if out[i].EventHint != nil {
			hint := *out[i].EventHint
			out[i].EventHint = &hint
		}
	}
	return out
}

func normalizeEventSummaryMode(query SyncChannelMessagesQuery) string {
	mode := strings.ToLower(strings.TrimSpace(query.EventSummaryMode))
	if mode == "" && query.IncludeEventMeta {
		return "full"
	}
	return mode
}

func (a *App) enrichSyncedMessagesWithEvents(ctx context.Context, mode string, messages []SyncedMessage) error {
	if mode == "" || len(messages) == 0 {
		return nil
	}
	if a == nil || a.eventStore == nil {
		return nil
	}
	keys := make([]MessageEventMessageKey, 0, len(messages))
	seen := make(map[MessageEventMessageKey]struct{}, len(messages))
	for _, msg := range messages {
		if !isLegacyStreamMessage(msg.Setting) {
			continue
		}
		if strings.TrimSpace(msg.ClientMsgNo) == "" || strings.TrimSpace(msg.ChannelID) == "" || msg.ChannelType == 0 {
			continue
		}
		key := MessageEventMessageKey{ChannelID: msg.ChannelID, ChannelType: int64(msg.ChannelType), ClientMsgNo: msg.ClientMsgNo}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil
	}
	stateMap, err := a.eventStore.GetMessageEventStatesBatch(ctx, keys, maxMessageEventSummaryLanes)
	if err != nil {
		return err
	}
	full := strings.EqualFold(mode, "full")
	for i := range messages {
		key := MessageEventMessageKey{ChannelID: messages[i].ChannelID, ChannelType: int64(messages[i].ChannelType), ClientMsgNo: messages[i].ClientMsgNo}
		states := stateMap[key]
		if len(states) == 0 {
			continue
		}
		applyMessageEventSummary(&messages[i], states, full)
	}
	return nil
}

const maxMessageEventSummaryLanes = 32

func isLegacyStreamMessage(setting uint8) bool {
	return setting&legacySettingStream != 0
}

func applyMessageEventSummary(msg *SyncedMessage, states []MessageEventState, full bool) {
	if msg == nil || len(states) == 0 {
		return
	}
	states = cloneMessageEventStates(states)
	sort.Slice(states, func(i, j int) bool { return states[i].EventKey < states[j].EventKey })
	meta := &MessageEventMeta{
		HasEvents: true,
		Events:    make([]MessageEventKeyMeta, 0, len(states)),
	}
	for _, state := range states {
		if state.EventKey == EventKeyFinish {
			meta.Completed = true
			continue
		}
		keyMeta := MessageEventKeyMeta{
			EventKey:        state.EventKey,
			Status:          state.Status,
			LastMsgEventSeq: state.LastMsgEventSeq,
			EndReason:       state.EndReason,
			Error:           state.Error,
		}
		if state.LastMsgEventSeq > meta.LastMsgEventSeq {
			meta.LastMsgEventSeq = state.LastMsgEventSeq
		}
		if state.Status == EventStatusOpen {
			meta.OpenEventCount++
		}
		if full && len(state.SnapshotPayload) > 0 {
			keyMeta.Snapshot = decodeMessageEventSnapshot(state.SnapshotPayload)
		}
		meta.Events = append(meta.Events, keyMeta)
	}
	meta.EventCount = len(meta.Events)
	if meta.EventCount == 0 && !meta.Completed {
		return
	}
	meta.EventVersion = meta.LastMsgEventSeq
	msg.EventMeta = meta
	msg.EventHint = &MessageEventSyncHint{ClientMsgNo: msg.ClientMsgNo, FromMsgEventSeq: 0}
	if mainState := findMainEventState(states); mainState != nil {
		msg.StreamData = toLegacyStreamData(mainState.SnapshotPayload)
		msg.End = toLegacyEnd(mainState.Status)
		msg.EndReason = mainState.EndReason
		msg.Error = mainState.Error
	}
}

func findMainEventState(states []MessageEventState) *MessageEventState {
	for _, state := range states {
		if state.EventKey == EventKeyDefault {
			cp := state
			return &cp
		}
	}
	return nil
}

func toLegacyEnd(status string) uint8 {
	switch status {
	case EventStatusClosed, EventStatusError, EventStatusCancelled:
		return 1
	default:
		return 0
	}
}

func toLegacyStreamData(snapshotPayload []byte) []byte {
	if len(snapshotPayload) == 0 {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(snapshotPayload, &raw); err != nil {
		return cloneBytes(snapshotPayload)
	}
	kind, _ := raw["kind"].(string)
	switch kind {
	case metadb.SnapshotKindText:
		text, _ := raw["text"].(string)
		return []byte(text)
	case "binary":
		data, _ := raw["data"].(string)
		if data != "" {
			return []byte(data)
		}
	}
	return cloneBytes(snapshotPayload)
}

func decodeMessageEventSnapshot(snapshotPayload []byte) any {
	var snapshot any
	if err := json.Unmarshal(snapshotPayload, &snapshot); err != nil {
		return string(snapshotPayload)
	}
	return snapshot
}

func cloneMessageEventMeta(meta *MessageEventMeta) *MessageEventMeta {
	if meta == nil {
		return nil
	}
	cp := *meta
	cp.Events = append([]MessageEventKeyMeta(nil), meta.Events...)
	return &cp
}

func cloneMessageEventStates(states []MessageEventState) []MessageEventState {
	out := make([]MessageEventState, len(states))
	copy(out, states)
	for i := range out {
		out[i].SnapshotPayload = cloneBytes(out[i].SnapshotPayload)
	}
	return out
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
