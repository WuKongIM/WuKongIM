package meta

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	EventTypeStreamDelta    = "stream.delta"
	EventTypeStreamClose    = "stream.close"
	EventTypeStreamError    = "stream.error"
	EventTypeStreamCancel   = "stream.cancel"
	EventTypeStreamSnapshot = "stream.snapshot"
	EventTypeStreamFinish   = "stream.finish"

	EventStatusOpen      = "open"
	EventStatusClosed    = "closed"
	EventStatusError     = "error"
	EventStatusCancelled = "cancelled"

	EventKeyDefault = "main"
	EventKeyFinish  = "__finish__"

	VisibilityPublic     = "public"
	VisibilityPrivate    = "private"
	VisibilityRestricted = "restricted"

	SnapshotKindText = "text"
)

const (
	messageEventStateColumnChannelID       uint16 = 1
	messageEventStateColumnChannelType     uint16 = 2
	messageEventStateColumnClientMsgNo     uint16 = 3
	messageEventStateColumnEventKey        uint16 = 4
	messageEventStateColumnStatus          uint16 = 5
	messageEventStateColumnLastSeq         uint16 = 6
	messageEventStateColumnLastEventID     uint16 = 7
	messageEventStateColumnLastEventType   uint16 = 8
	messageEventStateColumnLastVisibility  uint16 = 9
	messageEventStateColumnLastOccurredAt  uint16 = 10
	messageEventStateColumnSnapshotPayload uint16 = 11
	messageEventStateColumnEndReason       uint16 = 12
	messageEventStateColumnError           uint16 = 13
	messageEventStateColumnUpdatedAt       uint16 = 14

	messageEventCursorColumnChannelID   uint16 = 1
	messageEventCursorColumnChannelType uint16 = 2
	messageEventCursorColumnClientMsgNo uint16 = 3
	messageEventCursorColumnLastSeq     uint16 = 4
	messageEventCursorColumnUpdatedAt   uint16 = 5
)

var messageEventStateTable = registerMetaTable(TableSpec[MessageEventState]{
	ID:   TableIDMessageEventState,
	Name: "message_event_state",
	Columns: []schema.Column{
		{ID: messageEventStateColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: messageEventStateColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: messageEventStateColumnClientMsgNo, Name: "client_msg_no", Type: schema.TypeString, Required: true},
		{ID: messageEventStateColumnEventKey, Name: "event_key", Type: schema.TypeString, Required: true},
		{ID: messageEventStateColumnStatus, Name: "status", Type: schema.TypeString},
		{ID: messageEventStateColumnLastSeq, Name: "last_msg_event_seq", Type: schema.TypeUint64},
		{ID: messageEventStateColumnLastEventID, Name: "last_event_id", Type: schema.TypeString},
		{ID: messageEventStateColumnLastEventType, Name: "last_event_type", Type: schema.TypeString},
		{ID: messageEventStateColumnLastVisibility, Name: "last_visibility", Type: schema.TypeString},
		{ID: messageEventStateColumnLastOccurredAt, Name: "last_occurred_at", Type: schema.TypeInt64},
		{ID: messageEventStateColumnSnapshotPayload, Name: "snapshot_payload", Type: schema.TypeBytes},
		{ID: messageEventStateColumnEndReason, Name: "end_reason", Type: schema.TypeUint8},
		{ID: messageEventStateColumnError, Name: "error", Type: schema.TypeString},
		{ID: messageEventStateColumnUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
	},
	Families: []schema.Family{{ID: messageEventStatePrimaryFamilyID, Name: "primary", Columns: []uint16{
		messageEventStateColumnStatus,
		messageEventStateColumnLastSeq,
		messageEventStateColumnLastEventID,
		messageEventStateColumnLastEventType,
		messageEventStateColumnLastVisibility,
		messageEventStateColumnLastOccurredAt,
		messageEventStateColumnSnapshotPayload,
		messageEventStateColumnEndReason,
		messageEventStateColumnError,
		messageEventStateColumnUpdatedAt,
	}}},
	Primary: PrimarySpec[MessageEventState]{
		IndexID:  messageEventStatePrimaryIndexID,
		FamilyID: messageEventStatePrimaryFamilyID,
		Name:     "pk_message_event_state",
		Columns: []uint16{
			messageEventStateColumnChannelID,
			messageEventStateColumnChannelType,
			messageEventStateColumnClientMsgNo,
			messageEventStateColumnEventKey,
		},
		Layout: KeyLayout{KeyString, KeyInt64Ordered, KeyString, KeyString},
		Key: func(state MessageEventState) KeyParts {
			return messageEventStatePrimaryKey(state.ChannelID, state.ChannelType, state.ClientMsgNo, state.EventKey)
		},
	},
	Validate: validateMessageEventState,
	EncodeValue: func(state MessageEventState) ([]byte, error) {
		return encodeMessageEventStateValue(state), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (MessageEventState, error) {
		return decodeMessageEventStateValue(primary[0].S, primary[1].I64, primary[2].S, primary[3].S, value)
	},
})

var messageEventCursorTable = registerMetaTable(TableSpec[MessageEventCursor]{
	ID:   TableIDMessageEventCursor,
	Name: "message_event_cursor",
	Columns: []schema.Column{
		{ID: messageEventCursorColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: messageEventCursorColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: messageEventCursorColumnClientMsgNo, Name: "client_msg_no", Type: schema.TypeString, Required: true},
		{ID: messageEventCursorColumnLastSeq, Name: "last_msg_event_seq", Type: schema.TypeUint64},
		{ID: messageEventCursorColumnUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
	},
	Families: []schema.Family{{ID: messageEventCursorPrimaryFamilyID, Name: "primary", Columns: []uint16{
		messageEventCursorColumnLastSeq,
		messageEventCursorColumnUpdatedAt,
	}}},
	Primary: PrimarySpec[MessageEventCursor]{
		IndexID:  messageEventCursorPrimaryIndexID,
		FamilyID: messageEventCursorPrimaryFamilyID,
		Name:     "pk_message_event_cursor",
		Columns: []uint16{
			messageEventCursorColumnChannelID,
			messageEventCursorColumnChannelType,
			messageEventCursorColumnClientMsgNo,
		},
		Layout: KeyLayout{KeyString, KeyInt64Ordered, KeyString},
		Key: func(cursor MessageEventCursor) KeyParts {
			return messageEventCursorPrimaryKey(cursor.ChannelID, cursor.ChannelType, cursor.ClientMsgNo)
		},
	},
	Validate: validateMessageEventCursor,
	EncodeValue: func(cursor MessageEventCursor) ([]byte, error) {
		return encodeMessageEventCursorValue(cursor), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (MessageEventCursor, error) {
		return decodeMessageEventCursorValue(primary[0].S, primary[1].I64, primary[2].S, value)
	},
})

// MessageEventStateTable describes the message event state table schema.
var MessageEventStateTable = messageEventStateTable.Schema()

// MessageEventCursorTable describes the message event cursor table schema.
var MessageEventCursorTable = messageEventCursorTable.Schema()

// GetMessageEventState returns one projected message event lane.
func (s *Shard) GetMessageEventState(ctx context.Context, channelID string, channelType int64, clientMsgNo string, eventKey string) (MessageEventState, bool, error) {
	if err := s.check(ctx); err != nil {
		return MessageEventState{}, false, err
	}
	channelID, clientMsgNo, eventKey, err := normalizeMessageEventStateKey(channelID, channelType, clientMsgNo, eventKey)
	if err != nil {
		return MessageEventState{}, false, err
	}
	state, ok, err := messageEventStateTable.Get(ctx, s, messageEventStatePrimaryKey(channelID, channelType, clientMsgNo, eventKey))
	if err != nil || !ok {
		return MessageEventState{}, ok, err
	}
	return cloneMessageEventState(state), true, nil
}

// ListMessageEventStates returns projected lanes for one message in event-key order.
func (s *Shard) ListMessageEventStates(ctx context.Context, channelID string, channelType int64, clientMsgNo string, limit int) ([]MessageEventState, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	channelID, clientMsgNo, err := normalizeMessageEventMessageKey(channelID, channelType, clientMsgNo)
	if err != nil {
		return nil, err
	}
	rows, _, _, err := messageEventStateTable.ScanPrimaryPrefix(ctx, s, KeyParts{String(channelID), Int64Ordered(channelType), String(clientMsgNo)}, nil, limit)
	if err != nil {
		return nil, err
	}
	out := make([]MessageEventState, 0, len(rows))
	for _, row := range rows {
		out = append(out, cloneMessageEventState(row))
	}
	return out, nil
}

// AppendMessageEvent applies one message event projection update.
func (s *Shard) AppendMessageEvent(ctx context.Context, event MessageEventAppend) (MessageEventAppendResult, error) {
	if err := s.check(ctx); err != nil {
		return MessageEventAppendResult{}, err
	}
	event, err := normalizeMessageEventAppend(event)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	unlock := s.lock()
	defer unlock()

	state, stateExists, err := messageEventStateTable.getByPrimaryKey(s.db, s.hashSlot, messageEventStatePrimaryKey(event.ChannelID, event.ChannelType, event.ClientMsgNo, event.EventKey))
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	cursor, cursorExists, err := messageEventCursorTable.getByPrimaryKey(s.db, s.hashSlot, messageEventCursorPrimaryKey(event.ChannelID, event.ChannelType, event.ClientMsgNo))
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	nextState, nextCursor, applied, result := reduceMessageEventAppend(state, stateExists, cursor, cursorExists, event)
	if !applied {
		return result, nil
	}

	stateKey, err := messageEventStateRowKey(s.hashSlot, nextState.ChannelID, nextState.ChannelType, nextState.ClientMsgNo, nextState.EventKey)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	cursorKey, err := messageEventCursorRowKey(s.hashSlot, nextCursor.ChannelID, nextCursor.ChannelType, nextCursor.ClientMsgNo)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(stateKey, encodeMessageEventStateValue(nextState)); err != nil {
		return MessageEventAppendResult{}, err
	}
	if err := batch.Set(cursorKey, encodeMessageEventCursorValue(nextCursor)); err != nil {
		return MessageEventAppendResult{}, err
	}
	if err := batch.Commit(true); err != nil {
		return MessageEventAppendResult{}, err
	}
	return result, nil
}

// AppendMessageEvent stages one message event projection update.
func (b *Batch) AppendMessageEvent(hashSlot HashSlot, event MessageEventAppend) (MessageEventAppendResult, error) {
	if err := b.ensureOpen(); err != nil {
		return MessageEventAppendResult{}, err
	}
	event, err := normalizeMessageEventAppend(event)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	stateKey, err := messageEventStateRowKey(hashSlot, event.ChannelID, event.ChannelType, event.ClientMsgNo, event.EventKey)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	cursorKey, err := messageEventCursorRowKey(hashSlot, event.ChannelID, event.ChannelType, event.ClientMsgNo)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	state, stateExists, err := b.loadMessageEventStateForAppend(hashSlot, stateKey, event)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	cursor, cursorExists, err := b.loadMessageEventCursorForAppend(hashSlot, cursorKey, event)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	nextState, nextCursor, applied, result := reduceMessageEventAppend(state, stateExists, cursor, cursorExists, event)
	if !applied {
		return result, nil
	}
	if b.messageEventStates == nil {
		b.messageEventStates = make(map[string]MessageEventState)
	}
	if b.messageEventCursors == nil {
		b.messageEventCursors = make(map[string]MessageEventCursor)
	}
	b.messageEventStates[string(stateKey)] = cloneMessageEventState(nextState)
	b.messageEventCursors[string(cursorKey)] = nextCursor

	stateValue := encodeMessageEventStateValue(nextState)
	cursorValue := encodeMessageEventCursorValue(nextCursor)
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		if err := batch.Set(stateKey, stateValue); err != nil {
			return err
		}
		if err := batch.Set(cursorKey, cursorValue); err != nil {
			return err
		}
		state.tableRows[string(stateKey)] = tableRowOverlay{value: append([]byte(nil), stateValue...), exists: true}
		state.tableRows[string(cursorKey)] = tableRowOverlay{value: append([]byte(nil), cursorValue...), exists: true}
		return nil
	})
	return result, nil
}

func (b *Batch) loadMessageEventStateForAppend(hashSlot HashSlot, stateKey []byte, event MessageEventAppend) (MessageEventState, bool, error) {
	if b.messageEventStates != nil {
		if state, ok := b.messageEventStates[string(stateKey)]; ok {
			return cloneMessageEventState(state), true, nil
		}
	}
	return messageEventStateTable.getByPrimaryKey(b.db, hashSlot, messageEventStatePrimaryKey(event.ChannelID, event.ChannelType, event.ClientMsgNo, event.EventKey))
}

func (b *Batch) loadMessageEventCursorForAppend(hashSlot HashSlot, cursorKey []byte, event MessageEventAppend) (MessageEventCursor, bool, error) {
	if b.messageEventCursors != nil {
		if cursor, ok := b.messageEventCursors[string(cursorKey)]; ok {
			return cursor, true, nil
		}
	}
	return messageEventCursorTable.getByPrimaryKey(b.db, hashSlot, messageEventCursorPrimaryKey(event.ChannelID, event.ChannelType, event.ClientMsgNo))
}

func reduceMessageEventAppend(state MessageEventState, stateExists bool, cursor MessageEventCursor, cursorExists bool, event MessageEventAppend) (MessageEventState, MessageEventCursor, bool, MessageEventAppendResult) {
	if stateExists {
		state = cloneMessageEventState(state)
		if state.LastEventID == event.EventID || isMessageEventTerminal(state.Status) {
			return state, cursor, false, messageEventAppendResult(event, state)
		}
	} else {
		state = MessageEventState{
			ChannelID:   event.ChannelID,
			ChannelType: event.ChannelType,
			ClientMsgNo: event.ClientMsgNo,
			EventKey:    event.EventKey,
			Status:      EventStatusOpen,
		}
	}
	if !cursorExists {
		cursor = MessageEventCursor{ChannelID: event.ChannelID, ChannelType: event.ChannelType, ClientMsgNo: event.ClientMsgNo}
	}

	nextSeq := cursor.LastMsgEventSeq + 1
	switch event.EventType {
	case EventTypeStreamDelta:
		state.Status = EventStatusOpen
		state.SnapshotPayload = reduceMessageEventDelta(state.SnapshotPayload, event.Payload)
	case EventTypeStreamSnapshot:
		state.Status = EventStatusOpen
		state.SnapshotPayload = cloneBytes(event.Payload)
	case EventTypeStreamClose:
		state.Status = EventStatusClosed
		payload := decodeMessageEventTerminalPayload(event.Payload)
		if payload.snapshot != nil {
			state.SnapshotPayload = payload.snapshot
		}
		state.EndReason = payload.endReason
	case EventTypeStreamError:
		state.Status = EventStatusError
		payload := decodeMessageEventTerminalPayload(event.Payload)
		if payload.snapshot != nil {
			state.SnapshotPayload = payload.snapshot
		}
		state.Error = payload.errorText
	case EventTypeStreamCancel:
		state.Status = EventStatusCancelled
		payload := decodeMessageEventTerminalPayload(event.Payload)
		if payload.snapshot != nil {
			state.SnapshotPayload = payload.snapshot
		}
	case EventTypeStreamFinish:
		state.Status = EventStatusClosed
	}
	state.LastMsgEventSeq = nextSeq
	state.LastEventID = event.EventID
	state.LastEventType = event.EventType
	state.LastVisibility = event.Visibility
	state.LastOccurredAt = event.OccurredAt
	state.UpdatedAt = event.UpdatedAt

	cursor.LastMsgEventSeq = nextSeq
	cursor.UpdatedAt = event.UpdatedAt
	return state, cursor, true, messageEventAppendResult(event, state)
}

func messageEventAppendResult(event MessageEventAppend, state MessageEventState) MessageEventAppendResult {
	state = cloneMessageEventState(state)
	return MessageEventAppendResult{
		ChannelID:   event.ChannelID,
		ChannelType: event.ChannelType,
		ClientMsgNo: event.ClientMsgNo,
		EventID:     event.EventID,
		EventKey:    state.EventKey,
		MsgEventSeq: state.LastMsgEventSeq,
		Status:      state.Status,
		State:       state,
	}
}

func normalizeMessageEventAppend(event MessageEventAppend) (MessageEventAppend, error) {
	event.ChannelID = strings.TrimSpace(event.ChannelID)
	event.ClientMsgNo = strings.TrimSpace(event.ClientMsgNo)
	event.EventID = strings.TrimSpace(event.EventID)
	event.EventKey = strings.TrimSpace(event.EventKey)
	event.EventType = strings.ToLower(strings.TrimSpace(event.EventType))
	event.Visibility = strings.TrimSpace(event.Visibility)
	event.Payload = cloneBytes(event.Payload)
	if event.ChannelID == "" || event.ChannelType == 0 || event.ClientMsgNo == "" || event.EventID == "" || event.EventType == "" {
		return MessageEventAppend{}, dberrors.ErrInvalidArgument
	}
	if event.EventKey == "" {
		event.EventKey = EventKeyDefault
	}
	if event.EventType == EventTypeStreamFinish {
		event.EventKey = EventKeyFinish
	}
	if event.Visibility == "" {
		event.Visibility = VisibilityPublic
	}
	switch event.EventType {
	case EventTypeStreamDelta, EventTypeStreamClose, EventTypeStreamError, EventTypeStreamCancel, EventTypeStreamSnapshot, EventTypeStreamFinish:
	default:
		return MessageEventAppend{}, dberrors.ErrInvalidArgument
	}
	return event, nil
}

func normalizeMessageEventStateKey(channelID string, channelType int64, clientMsgNo string, eventKey string) (string, string, string, error) {
	channelID, clientMsgNo, err := normalizeMessageEventMessageKey(channelID, channelType, clientMsgNo)
	if err != nil {
		return "", "", "", err
	}
	eventKey = strings.TrimSpace(eventKey)
	if eventKey == "" {
		eventKey = EventKeyDefault
	}
	if err := validateKeyString(eventKey); err != nil {
		return "", "", "", err
	}
	return channelID, clientMsgNo, eventKey, nil
}

func normalizeMessageEventMessageKey(channelID string, channelType int64, clientMsgNo string) (string, string, error) {
	channelID = strings.TrimSpace(channelID)
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	if channelType == 0 {
		return "", "", dberrors.ErrInvalidArgument
	}
	if err := validateKeyString(channelID); err != nil {
		return "", "", err
	}
	if err := validateKeyString(clientMsgNo); err != nil {
		return "", "", err
	}
	return channelID, clientMsgNo, nil
}

func validateMessageEventState(state MessageEventState) error {
	_, _, _, err := normalizeMessageEventStateKey(state.ChannelID, state.ChannelType, state.ClientMsgNo, state.EventKey)
	return err
}

func validateMessageEventCursor(cursor MessageEventCursor) error {
	_, _, err := normalizeMessageEventMessageKey(cursor.ChannelID, cursor.ChannelType, cursor.ClientMsgNo)
	return err
}

func isMessageEventTerminal(status string) bool {
	return status == EventStatusClosed || status == EventStatusError || status == EventStatusCancelled
}

func reduceMessageEventDelta(existing []byte, payload []byte) []byte {
	var delta struct {
		Kind  string `json:"kind"`
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(payload, &delta); err != nil || delta.Kind != SnapshotKindText {
		return cloneBytes(payload)
	}
	text := ""
	var current struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(existing, &current); err == nil && current.Kind == SnapshotKindText {
		text = current.Text
	}
	out, err := json.Marshal(struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}{Kind: SnapshotKindText, Text: text + delta.Delta})
	if err != nil {
		return cloneBytes(payload)
	}
	return out
}

type messageEventTerminalPayload struct {
	snapshot  []byte
	endReason uint8
	errorText string
}

func decodeMessageEventTerminalPayload(payload []byte) messageEventTerminalPayload {
	var raw struct {
		Snapshot  json.RawMessage `json:"snapshot"`
		EndReason uint8           `json:"end_reason"`
		Error     string          `json:"error"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return messageEventTerminalPayload{}
	}
	out := messageEventTerminalPayload{endReason: raw.EndReason, errorText: raw.Error}
	if len(raw.Snapshot) > 0 && string(raw.Snapshot) != "null" {
		out.snapshot = cloneBytes(raw.Snapshot)
	}
	return out
}

func messageEventStatePrimaryKey(channelID string, channelType int64, clientMsgNo string, eventKey string) KeyParts {
	return KeyParts{String(channelID), Int64Ordered(channelType), String(clientMsgNo), String(eventKey)}
}

func messageEventCursorPrimaryKey(channelID string, channelType int64, clientMsgNo string) KeyParts {
	return KeyParts{String(channelID), Int64Ordered(channelType), String(clientMsgNo)}
}

func messageEventStateRowKey(hashSlot HashSlot, channelID string, channelType int64, clientMsgNo string, eventKey string) ([]byte, error) {
	return messageEventStateTable.primaryRowKey(hashSlot, messageEventStatePrimaryKey(channelID, channelType, clientMsgNo, eventKey))
}

func messageEventCursorRowKey(hashSlot HashSlot, channelID string, channelType int64, clientMsgNo string) ([]byte, error) {
	return messageEventCursorTable.primaryRowKey(hashSlot, messageEventCursorPrimaryKey(channelID, channelType, clientMsgNo))
}

func encodeMessageEventStateValue(state MessageEventState) []byte {
	value := appendValueString(nil, state.Status)
	value = appendValueUint64(value, state.LastMsgEventSeq)
	value = appendValueString(value, state.LastEventID)
	value = appendValueString(value, state.LastEventType)
	value = appendValueString(value, state.LastVisibility)
	value = appendValueInt64(value, state.LastOccurredAt)
	value = appendValueBytes(value, state.SnapshotPayload)
	value = append(value, state.EndReason)
	value = appendValueString(value, state.Error)
	return appendValueInt64(value, state.UpdatedAt)
}

func decodeMessageEventStateValue(channelID string, channelType int64, clientMsgNo string, eventKey string, value []byte) (MessageEventState, error) {
	status, rest, err := readValueString(value)
	if err != nil {
		return MessageEventState{}, err
	}
	lastSeq, rest, err := readValueUint64(rest)
	if err != nil {
		return MessageEventState{}, err
	}
	lastEventID, rest, err := readValueString(rest)
	if err != nil {
		return MessageEventState{}, err
	}
	lastEventType, rest, err := readValueString(rest)
	if err != nil {
		return MessageEventState{}, err
	}
	lastVisibility, rest, err := readValueString(rest)
	if err != nil {
		return MessageEventState{}, err
	}
	lastOccurredAt, rest, err := readValueInt64(rest)
	if err != nil {
		return MessageEventState{}, err
	}
	snapshot, rest, err := readValueBytes(rest)
	if err != nil {
		return MessageEventState{}, err
	}
	if len(rest) < 1 {
		return MessageEventState{}, dberrors.ErrCorruptValue
	}
	endReason := rest[0]
	rest = rest[1:]
	errorText, rest, err := readValueString(rest)
	if err != nil {
		return MessageEventState{}, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil {
		return MessageEventState{}, err
	}
	if len(rest) != 0 {
		return MessageEventState{}, dberrors.ErrCorruptValue
	}
	return MessageEventState{
		ChannelID:       channelID,
		ChannelType:     channelType,
		ClientMsgNo:     clientMsgNo,
		EventKey:        eventKey,
		Status:          status,
		LastMsgEventSeq: lastSeq,
		LastEventID:     lastEventID,
		LastEventType:   lastEventType,
		LastVisibility:  lastVisibility,
		LastOccurredAt:  lastOccurredAt,
		SnapshotPayload: snapshot,
		EndReason:       endReason,
		Error:           errorText,
		UpdatedAt:       updatedAt,
	}, nil
}

func encodeMessageEventCursorValue(cursor MessageEventCursor) []byte {
	value := appendValueUint64(nil, cursor.LastMsgEventSeq)
	return appendValueInt64(value, cursor.UpdatedAt)
}

func decodeMessageEventCursorValue(channelID string, channelType int64, clientMsgNo string, value []byte) (MessageEventCursor, error) {
	lastSeq, rest, err := readValueUint64(value)
	if err != nil {
		return MessageEventCursor{}, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil {
		return MessageEventCursor{}, err
	}
	if len(rest) != 0 {
		return MessageEventCursor{}, dberrors.ErrCorruptValue
	}
	return MessageEventCursor{
		ChannelID:       channelID,
		ChannelType:     channelType,
		ClientMsgNo:     clientMsgNo,
		LastMsgEventSeq: lastSeq,
		UpdatedAt:       updatedAt,
	}, nil
}

func cloneMessageEventState(state MessageEventState) MessageEventState {
	state.SnapshotPayload = cloneBytes(state.SnapshotPayload)
	return state
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}
