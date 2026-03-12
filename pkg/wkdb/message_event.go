package wkdb

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

// ---- 事件类型常量 ----
const (
	EventTypeStreamDelta    = "stream.delta"
	EventTypeStreamClose    = "stream.close"
	EventTypeStreamError    = "stream.error"
	EventTypeStreamCancel   = "stream.cancel"
	EventTypeStreamSnapshot = "stream.snapshot"
	EventTypeStreamFinish   = "stream.finish"
)

// ---- 事件状态常量 ----
const (
	EventStatusOpen      = "open"
	EventStatusClosed    = "closed"
	EventStatusError     = "error"
	EventStatusCancelled = "cancelled"
)

// ---- 默认 event key ----
const EventKeyDefault = "main"

// ---- 消息级终结 event key ----
const EventKeyFinish = "__finish__"

// ---- 可见性常量 ----
const (
	VisibilityPublic     = "public"
	VisibilityPrivate    = "private"
	VisibilityRestricted = "restricted"
)

// ---- 快照类型常量 ----
const SnapshotKindText = "text"

// MessageEvent is an event row bound to a specific client_msg_no.
type MessageEvent struct {
	ChannelId   string            `json:"channel_id"`
	ChannelType uint8             `json:"channel_type"`
	ClientMsgNo string            `json:"client_msg_no"`
	MsgEventSeq uint64            `json:"msg_event_seq"`
	EventID     string            `json:"event_id"`
	EventKey    string            `json:"event_key"`
	EventType   string            `json:"event_type"`
	Visibility  string            `json:"visibility,omitempty"`
	OccurredAt  int64             `json:"occurred_at,omitempty"`
	Payload     []byte            `json:"payload,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

// MessageEventState is the projection state per event key.
type MessageEventState struct {
	ChannelId       string `json:"channel_id"`
	ChannelType     uint8  `json:"channel_type"`
	ClientMsgNo     string `json:"client_msg_no"`
	EventKey        string `json:"event_key"`
	Status          string `json:"status"`
	LastMsgEventSeq uint64 `json:"last_msg_event_seq"`
	LastEventID     string `json:"last_event_id,omitempty"`
	LastEventType   string `json:"last_event_type,omitempty"`
	LastVisibility  string `json:"last_visibility,omitempty"`
	LastOccurredAt  int64  `json:"last_occurred_at,omitempty"`
	SnapshotPayload []byte `json:"snapshot_payload,omitempty"`
	EndReason       uint8  `json:"end_reason,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (wk *wukongDB) AppendMessageEventWithState(event *MessageEvent) (*MessageEvent, *MessageEventState, error) {
	if event == nil {
		return nil, nil, ErrNotFound
	}
	if strings.TrimSpace(event.ClientMsgNo) == "" {
		return nil, nil, ErrNotFound
	}
	if strings.TrimSpace(event.EventID) == "" {
		return nil, nil, ErrNotFound
	}
	if strings.TrimSpace(event.EventType) == "" {
		return nil, nil, ErrNotFound
	}

	event.ChannelId = strings.TrimSpace(event.ChannelId)
	event.ClientMsgNo = strings.TrimSpace(event.ClientMsgNo)
	event.EventID = strings.TrimSpace(event.EventID)
	event.EventType = strings.ToLower(strings.TrimSpace(event.EventType))
	event.EventKey = normalizeEventKey(event.EventKey)
	if event.OccurredAt == 0 {
		event.OccurredAt = time.Now().UnixMilli()
	}

	lockKey := "message_event:" + event.ClientMsgNo
	wk.dblock.userLock.Lock(lockKey)
	defer wk.dblock.userLock.Unlock(lockKey)

	eventState, err := wk.GetMessageEventState(event.ChannelId, event.ChannelType, event.ClientMsgNo, event.EventKey)
	if err != nil {
		return nil, nil, err
	}
	if eventState != nil && eventState.LastEventID != "" && eventState.LastEventID == event.EventID {
		cp := *event
		cp.MsgEventSeq = eventState.LastMsgEventSeq
		cp.EventKey = eventState.EventKey
		cp.EventType = eventState.LastEventType
		cp.Visibility = eventState.LastVisibility
		cp.OccurredAt = eventState.LastOccurredAt
		if len(eventState.SnapshotPayload) > 0 {
			cp.Payload = append([]byte(nil), eventState.SnapshotPayload...)
		}
		return &cp, eventState, nil
	}
	if eventState != nil && isTerminalStatus(eventState.Status) {
		return wk.buildProjectedEvent(event.ChannelId, event.ChannelType, event.ClientMsgNo, *eventState), eventState, nil
	}

	db := wk.channelDb(event.ChannelId, event.ChannelType)
	batch := db.NewBatch()
	defer batch.Close()

	seq, err := wk.incrMessageEventSeq(event.ChannelId, event.ChannelType, event.ClientMsgNo, db, batch)
	if err != nil {
		return nil, nil, err
	}
	event.MsgEventSeq = seq
	if eventState == nil {
		eventState = &MessageEventState{
			ChannelId:   event.ChannelId,
			ChannelType: event.ChannelType,
			ClientMsgNo: event.ClientMsgNo,
			EventKey:    event.EventKey,
		}
	}

	mergedState := reduceEventState(eventState, event)
	stateBytes, err := json.Marshal(mergedState)
	if err != nil {
		return nil, nil, err
	}
	fmt.Println("NewMessageEventStateKey---->", event.ChannelId, event.ChannelType, event.ClientMsgNo, event.EventKey)
	if err := batch.Set(key.NewMessageEventStateKey(mergedState.ChannelId, mergedState.ChannelType, mergedState.ClientMsgNo, mergedState.EventKey), stateBytes, wk.noSync); err != nil {
		return nil, nil, err
	}

	if err := batch.Commit(wk.sync); err != nil {
		return nil, nil, err
	}
	return event, mergedState, nil
}

func (wk *wukongDB) GetMessageEventByEventID(channelId string, channelType uint8, clientMsgNo, eventID string) (*MessageEvent, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	eventID = strings.TrimSpace(eventID)
	if clientMsgNo == "" || eventID == "" {
		return nil, nil
	}
	states, err := wk.GetMessageEventStates(channelId, channelType, clientMsgNo)
	if err != nil {
		return nil, err
	}
	for _, state := range states {
		if state.LastEventID != eventID {
			continue
		}
		return wk.buildProjectedEvent(channelId, channelType, clientMsgNo, state), nil
	}
	return nil, nil
}

func (wk *wukongDB) ListMessageEvents(channelId string, channelType uint8, clientMsgNo string, fromMsgEventSeq uint64, eventKey string, limit int) ([]MessageEvent, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	eventKey = strings.TrimSpace(eventKey)
	if clientMsgNo == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	if eventKey != "" {
		eventKey = normalizeEventKey(eventKey)
	}

	states, err := wk.GetMessageEventStates(channelId, channelType, clientMsgNo)
	if err != nil {
		return nil, err
	}

	res := make([]MessageEvent, 0, len(states))
	for _, state := range states {
		if eventKey != "" && state.EventKey != eventKey {
			continue
		}
		if state.LastMsgEventSeq <= fromMsgEventSeq {
			continue
		}
		res = append(res, *wk.buildProjectedEvent(channelId, channelType, clientMsgNo, state))
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].MsgEventSeq < res[j].MsgEventSeq
	})
	if len(res) > limit {
		res = res[:limit]
	}
	return res, nil
}

func (wk *wukongDB) GetMessageEventStates(channelId string, channelType uint8, clientMsgNo string) ([]MessageEventState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	if clientMsgNo == "" {
		return nil, nil
	}

	db := wk.channelDb(channelId, channelType)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessageEventStateLowKey(channelId, channelType, clientMsgNo),
		UpperBound: key.NewMessageEventStateHighKey(channelId, channelType, clientMsgNo),
	})
	defer iter.Close()

	states := make([]MessageEventState, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		var state MessageEventState
		if err := json.Unmarshal(iter.Value(), &state); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (wk *wukongDB) GetMessageEventStatesBatch(channelId string, channelType uint8, clientMsgNos []string) (map[string][]MessageEventState, error) {
	if len(clientMsgNos) == 0 {
		return nil, nil
	}

	fmt.Println("GetMessageEventStatesBatch---->", channelId, channelType, clientMsgNos)
	// 同一 channel 的所有 clientMsgNo 都在同一个 shard 上
	db := wk.channelDb(channelId, channelType)
	result := make(map[string][]MessageEventState, len(clientMsgNos))
	for _, cmn := range clientMsgNos {
		cmn = strings.TrimSpace(cmn)
		if cmn == "" {
			continue
		}
		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewMessageEventStateLowKey(channelId, channelType, cmn),
			UpperBound: key.NewMessageEventStateHighKey(channelId, channelType, cmn),
		})
		var states []MessageEventState
		for iter.First(); iter.Valid(); iter.Next() {
			var state MessageEventState
			if err := json.Unmarshal(iter.Value(), &state); err != nil {
				_ = iter.Close()
				return nil, err
			}
			states = append(states, state)
		}
		_ = iter.Close()
		if len(states) > 0 {
			result[cmn] = states
		}
	}
	return result, nil
}

func (wk *wukongDB) GetMessageEventState(channelId string, channelType uint8, clientMsgNo, eventKey string) (*MessageEventState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	eventKey = normalizeEventKey(eventKey)
	if clientMsgNo == "" {
		return nil, nil
	}

	db := wk.channelDb(channelId, channelType)
	v, closer, err := db.Get(key.NewMessageEventStateKey(channelId, channelType, clientMsgNo, eventKey))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	vCopy := append([]byte(nil), v...)
	if closer != nil {
		_ = closer.Close()
	}

	var state MessageEventState
	if err := json.Unmarshal(vCopy, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (wk *wukongDB) incrMessageEventSeq(channelId string, channelType uint8, clientMsgNo string, db *pebble.DB, batch *pebble.Batch) (uint64, error) {
	var current uint64
	seqKey := key.NewMessageEventSeqKey(channelId, channelType, clientMsgNo)
	v, closer, err := db.Get(seqKey)
	if err != nil {
		if err != pebble.ErrNotFound {
			return 0, err
		}
	} else {
		if len(v) == 8 {
			current = wk.endian.Uint64(v)
		}
		if closer != nil {
			_ = closer.Close()
		}
	}

	next := current + 1
	buf := make([]byte, 8)
	wk.endian.PutUint64(buf, next)
	if err := batch.Set(seqKey, buf, wk.noSync); err != nil {
		return 0, err
	}
	return next, nil
}

func normalizeEventKey(eventKey string) string {
	eventKey = strings.TrimSpace(eventKey)
	if eventKey == "" {
		return EventKeyDefault
	}
	return eventKey
}

func isTerminalStatus(status string) bool {
	switch status {
	case EventStatusClosed, EventStatusError, EventStatusCancelled:
		return true
	default:
		return false
	}
}

func reduceEventState(state *MessageEventState, event *MessageEvent) *MessageEventState {
	if state == nil {
		state = &MessageEventState{}
	}
	if state.ChannelId == "" {
		state.ChannelId = event.ChannelId
		state.ChannelType = event.ChannelType
	}
	if state.ClientMsgNo == "" {
		state.ClientMsgNo = event.ClientMsgNo
	}
	if state.EventKey == "" {
		state.EventKey = event.EventKey
	}
	state.LastEventID = event.EventID
	state.LastEventType = event.EventType
	state.LastVisibility = event.Visibility
	state.LastOccurredAt = event.OccurredAt

	if isTerminalStatus(state.Status) {
		return state
	}

	switch event.EventType {
	case EventTypeStreamDelta:
		if state.Status == "" {
			state.Status = EventStatusOpen
		}
		applyDeltaSnapshot(state, event)
	case EventTypeStreamSnapshot:
		state.SnapshotPayload = append([]byte(nil), event.Payload...)
	case EventTypeStreamClose:
		if snapshot := extractSnapshot(event.Payload); len(snapshot) > 0 {
			state.SnapshotPayload = snapshot
		}
		state.Status = EventStatusClosed
		state.EndReason = extractEndReason(event.Payload)
	case EventTypeStreamError:
		if snapshot := extractSnapshot(event.Payload); len(snapshot) > 0 {
			state.SnapshotPayload = snapshot
		}
		state.Status = EventStatusError
		state.Error = extractError(event.Payload)
	case EventTypeStreamCancel:
		if snapshot := extractSnapshot(event.Payload); len(snapshot) > 0 {
			state.SnapshotPayload = snapshot
		}
		state.Status = EventStatusCancelled
	case EventTypeStreamFinish:
		state.Status = EventStatusClosed
	}
	state.LastMsgEventSeq = event.MsgEventSeq
	return state
}

func applyDeltaSnapshot(state *MessageEventState, event *MessageEvent) {
	if len(event.Payload) == 0 {
		return
	}

	payload := map[string]interface{}{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		state.SnapshotPayload = append(state.SnapshotPayload, event.Payload...)
		return
	}
	kind, _ := payload["kind"].(string)
	if kind != SnapshotKindText {
		state.SnapshotPayload = append([]byte(nil), event.Payload...)
		return
	}

	delta, _ := payload["delta"].(string)
	if delta == "" {
		return
	}

	snapshot := map[string]interface{}{"kind": SnapshotKindText, "text": ""}
	if len(state.SnapshotPayload) > 0 {
		_ = json.Unmarshal(state.SnapshotPayload, &snapshot)
	}
	text, _ := snapshot["text"].(string)
	snapshot["kind"] = SnapshotKindText
	snapshot["text"] = text + delta
	data, _ := json.Marshal(snapshot)
	state.SnapshotPayload = data
}

func extractEndReason(payload []byte) uint8 {
	if len(payload) == 0 {
		return 0
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return 0
	}
	v, ok := m["end_reason"]
	if !ok {
		return 0
	}
	switch vv := v.(type) {
	case float64:
		return uint8(vv)
	case int:
		return uint8(vv)
	case string:
		n, _ := strconv.ParseUint(vv, 10, 8)
		return uint8(n)
	default:
		return 0
	}
}

func extractError(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	errMsg, _ := m["error"].(string)
	return errMsg
}

func extractSnapshot(payload []byte) []byte {
	if len(payload) == 0 {
		return nil
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil
	}
	raw, ok := m["snapshot"]
	if !ok {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return data
}

func (wk *wukongDB) buildProjectedEvent(channelId string, channelType uint8, clientMsgNo string, state MessageEventState) *MessageEvent {
	eventType := state.LastEventType
	if strings.TrimSpace(eventType) == "" {
		eventType = EventTypeStreamSnapshot
	}
	payload := append([]byte(nil), state.SnapshotPayload...)
	if len(payload) == 0 {
		payloadMap := map[string]interface{}{
			"status":     state.Status,
			"end_reason": state.EndReason,
		}
		if state.Error != "" {
			payloadMap["error"] = state.Error
		}
		if data, err := json.Marshal(payloadMap); err == nil {
			payload = data
		}
	}
	return &MessageEvent{
		ChannelId:   channelId,
		ChannelType: channelType,
		ClientMsgNo: clientMsgNo,
		MsgEventSeq: state.LastMsgEventSeq,
		EventID:     state.LastEventID,
		EventKey:    state.EventKey,
		EventType:   eventType,
		Visibility:  state.LastVisibility,
		OccurredAt:  state.LastOccurredAt,
		Payload:     payload,
	}
}
