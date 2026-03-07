package wkdb

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

const (
	messageEventDefaultLane = "main"
)

// MessageEvent is an event row bound to a specific client_msg_no.
type MessageEvent struct {
	ChannelId   string            `json:"channel_id"`
	ChannelType uint8             `json:"channel_type"`
	ClientMsgNo string            `json:"client_msg_no"`
	MsgEventSeq uint64            `json:"msg_event_seq"`
	EventID     string            `json:"event_id"`
	LaneID      string            `json:"lane_id"`
	EventType   string            `json:"event_type"`
	Visibility  string            `json:"visibility,omitempty"`
	OccurredAt  int64             `json:"occurred_at,omitempty"`
	Payload     []byte            `json:"payload,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

// MessageLaneState is the projection state per lane.
type MessageLaneState struct {
	ChannelId       string `json:"channel_id"`
	ChannelType     uint8  `json:"channel_type"`
	ClientMsgNo     string `json:"client_msg_no"`
	LaneID          string `json:"lane_id"`
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

func (wk *wukongDB) AppendMessageEventWithLaneState(event *MessageEvent) (*MessageEvent, *MessageLaneState, error) {
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
	event.LaneID = normalizeLaneID(event.LaneID)
	if event.OccurredAt == 0 {
		event.OccurredAt = time.Now().UnixMilli()
	}

	lockKey := "message_event:" + event.ClientMsgNo
	wk.dblock.userLock.Lock(lockKey)
	defer wk.dblock.userLock.Unlock(lockKey)

	laneState, err := wk.GetMessageLaneState(event.ChannelId, event.ChannelType, event.ClientMsgNo, event.LaneID)
	if err != nil {
		return nil, nil, err
	}
	if laneState != nil && laneState.LastEventID != "" && laneState.LastEventID == event.EventID {
		cp := *event
		cp.MsgEventSeq = laneState.LastMsgEventSeq
		cp.LaneID = laneState.LaneID
		cp.EventType = laneState.LastEventType
		cp.Visibility = laneState.LastVisibility
		cp.OccurredAt = laneState.LastOccurredAt
		if len(laneState.SnapshotPayload) > 0 {
			cp.Payload = append([]byte(nil), laneState.SnapshotPayload...)
		}
		return &cp, laneState, nil
	}
	if laneState != nil && isTerminalStatus(laneState.Status) {
		return wk.buildProjectedEvent(event.ChannelId, event.ChannelType, event.ClientMsgNo, *laneState), laneState, nil
	}

	db := wk.shardDB(event.ClientMsgNo)
	batch := db.NewBatch()
	defer batch.Close()

	seq, err := wk.incrMessageEventSeq(event.ChannelId, event.ChannelType, event.ClientMsgNo, db, batch)
	if err != nil {
		return nil, nil, err
	}
	event.MsgEventSeq = seq
	if laneState == nil {
		laneState = &MessageLaneState{
			ChannelId:   event.ChannelId,
			ChannelType: event.ChannelType,
			ClientMsgNo: event.ClientMsgNo,
			LaneID:      event.LaneID,
		}
	}

	mergedState := reduceLaneState(laneState, event)
	laneStateBytes, err := json.Marshal(mergedState)
	if err != nil {
		return nil, nil, err
	}

	if err := batch.Set(key.NewMessageLaneStateKey(mergedState.ChannelId, mergedState.ChannelType, mergedState.ClientMsgNo, mergedState.LaneID), laneStateBytes, wk.noSync); err != nil {
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
	states, err := wk.GetMessageLaneStates(channelId, channelType, clientMsgNo)
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

func (wk *wukongDB) ListMessageEvents(channelId string, channelType uint8, clientMsgNo string, fromMsgEventSeq uint64, laneID string, limit int) ([]MessageEvent, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	laneID = strings.TrimSpace(laneID)
	if clientMsgNo == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	if laneID != "" {
		laneID = normalizeLaneID(laneID)
	}

	states, err := wk.GetMessageLaneStates(channelId, channelType, clientMsgNo)
	if err != nil {
		return nil, err
	}

	res := make([]MessageEvent, 0, len(states))
	for _, state := range states {
		if laneID != "" && state.LaneID != laneID {
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

func (wk *wukongDB) GetMessageLaneStates(channelId string, channelType uint8, clientMsgNo string) ([]MessageLaneState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	if clientMsgNo == "" {
		return nil, nil
	}

	db := wk.shardDB(clientMsgNo)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessageLaneStateLowKey(channelId, channelType, clientMsgNo),
		UpperBound: key.NewMessageLaneStateHighKey(channelId, channelType, clientMsgNo),
	})
	defer iter.Close()

	states := make([]MessageLaneState, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		var state MessageLaneState
		if err := json.Unmarshal(iter.Value(), &state); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (wk *wukongDB) GetMessageLaneStatesBatch(channelId string, channelType uint8, clientMsgNos []string) (map[string][]MessageLaneState, error) {
	if len(clientMsgNos) == 0 {
		return nil, nil
	}

	// Group clientMsgNos by shard to minimize iterator creation.
	type shardGroup struct {
		shardId      uint32
		clientMsgNos []string
	}
	shardMap := make(map[uint32]*shardGroup)
	for _, cmn := range clientMsgNos {
		cmn = strings.TrimSpace(cmn)
		if cmn == "" {
			continue
		}
		sid := wk.shardId(cmn)
		g, ok := shardMap[sid]
		if !ok {
			g = &shardGroup{shardId: sid}
			shardMap[sid] = g
		}
		g.clientMsgNos = append(g.clientMsgNos, cmn)
	}

	result := make(map[string][]MessageLaneState, len(clientMsgNos))
	for sid, g := range shardMap {
		db := wk.shardDBById(sid)
		for _, cmn := range g.clientMsgNos {
			iter := db.NewIter(&pebble.IterOptions{
				LowerBound: key.NewMessageLaneStateLowKey(channelId, channelType, cmn),
				UpperBound: key.NewMessageLaneStateHighKey(channelId, channelType, cmn),
			})
			var states []MessageLaneState
			for iter.First(); iter.Valid(); iter.Next() {
				var state MessageLaneState
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
	}
	return result, nil
}

func (wk *wukongDB) GetMessageLaneState(channelId string, channelType uint8, clientMsgNo, laneID string) (*MessageLaneState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	laneID = normalizeLaneID(laneID)
	if clientMsgNo == "" {
		return nil, nil
	}

	db := wk.shardDB(clientMsgNo)
	v, closer, err := db.Get(key.NewMessageLaneStateKey(channelId, channelType, clientMsgNo, laneID))
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

	var state MessageLaneState
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

func normalizeLaneID(laneID string) string {
	laneID = strings.TrimSpace(laneID)
	if laneID == "" {
		return messageEventDefaultLane
	}
	return laneID
}

func isTerminalStatus(status string) bool {
	switch status {
	case "closed", "error", "cancelled":
		return true
	default:
		return false
	}
}

func reduceLaneState(state *MessageLaneState, event *MessageEvent) *MessageLaneState {
	if state == nil {
		state = &MessageLaneState{}
	}
	if state.ChannelId == "" {
		state.ChannelId = event.ChannelId
		state.ChannelType = event.ChannelType
	}
	if state.ClientMsgNo == "" {
		state.ClientMsgNo = event.ClientMsgNo
	}
	if state.LaneID == "" {
		state.LaneID = event.LaneID
	}
	state.LastEventID = event.EventID
	state.LastEventType = event.EventType
	state.LastVisibility = event.Visibility
	state.LastOccurredAt = event.OccurredAt

	if isTerminalStatus(state.Status) {
		return state
	}

	switch event.EventType {
	case "stream.open":
		state.Status = "open"
	case "stream.delta":
		applyDeltaSnapshot(state, event)
	case "stream.snapshot":
		state.SnapshotPayload = append([]byte(nil), event.Payload...)
	case "stream.close":
		if snapshot := extractSnapshot(event.Payload); len(snapshot) > 0 {
			state.SnapshotPayload = snapshot
		}
		state.Status = "closed"
		state.EndReason = extractEndReason(event.Payload)
	case "stream.error":
		if snapshot := extractSnapshot(event.Payload); len(snapshot) > 0 {
			state.SnapshotPayload = snapshot
		}
		state.Status = "error"
		state.Error = extractError(event.Payload)
	case "stream.cancel":
		if snapshot := extractSnapshot(event.Payload); len(snapshot) > 0 {
			state.SnapshotPayload = snapshot
		}
		state.Status = "cancelled"
	}
	state.LastMsgEventSeq = event.MsgEventSeq
	return state
}

func applyDeltaSnapshot(state *MessageLaneState, event *MessageEvent) {
	if len(event.Payload) == 0 {
		return
	}

	payload := map[string]interface{}{}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		state.SnapshotPayload = append(state.SnapshotPayload, event.Payload...)
		return
	}
	kind, _ := payload["kind"].(string)
	if kind != "text" {
		state.SnapshotPayload = append([]byte(nil), event.Payload...)
		return
	}

	delta, _ := payload["delta"].(string)
	if delta == "" {
		return
	}

	snapshot := map[string]interface{}{"kind": "text", "text": ""}
	if len(state.SnapshotPayload) > 0 {
		_ = json.Unmarshal(state.SnapshotPayload, &snapshot)
	}
	text, _ := snapshot["text"].(string)
	snapshot["kind"] = "text"
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

func (wk *wukongDB) buildProjectedEvent(channelId string, channelType uint8, clientMsgNo string, state MessageLaneState) *MessageEvent {
	eventType := state.LastEventType
	if strings.TrimSpace(eventType) == "" {
		eventType = "projection.snapshot"
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
		LaneID:      state.LaneID,
		EventType:   eventType,
		Visibility:  state.LastVisibility,
		OccurredAt:  state.LastOccurredAt,
		Payload:     payload,
	}
}
