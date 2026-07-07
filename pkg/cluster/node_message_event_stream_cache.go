package cluster

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

const (
	defaultMessageEventStreamCacheMaxSessions = 50000
)

type messageEventStreamCacheKey struct {
	channelID   string
	channelType int64
	clientMsgNo string
}

type messageEventStreamCacheSession struct {
	states  map[string]metadb.MessageEventState
	applied map[string]metadb.MessageEventAppendResult
	updated time.Time
}

// messageEventStreamCache keeps in-flight stream projections on the Slot leader.
type messageEventStreamCache struct {
	mu          sync.Mutex
	maxSessions int
	sessions    map[messageEventStreamCacheKey]*messageEventStreamCacheSession
}

func newMessageEventStreamCache(maxSessions int) *messageEventStreamCache {
	if maxSessions <= 0 {
		maxSessions = defaultMessageEventStreamCacheMaxSessions
	}
	return &messageEventStreamCache{
		maxSessions: maxSessions,
		sessions:    make(map[messageEventStreamCacheKey]*messageEventStreamCacheSession),
	}
}

func (c *messageEventStreamCache) appendCached(event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error) {
	if c == nil {
		return cachedMessageEventResult(event, cachedMessageEventState(event)), nil
	}
	key := messageEventCacheKey(event)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	session, err := c.sessionLocked(key, now)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	if result, ok := session.applied[event.EventID]; ok {
		return cloneMessageEventAppendResult(result), nil
	}

	state := session.states[event.EventKey]
	if state.EventKey == "" {
		state = cachedMessageEventState(event)
	}
	if isMessageEventTerminalStatus(state.Status) {
		result := cachedMessageEventResult(event, state)
		session.applied[event.EventID] = cloneMessageEventAppendResult(result)
		return result, nil
	}

	state.Status = metadb.EventStatusOpen
	switch event.EventType {
	case metadb.EventTypeStreamDelta:
		state.SnapshotPayload = reduceCachedMessageEventDelta(state.SnapshotPayload, event.Payload)
	case metadb.EventTypeStreamSnapshot:
		state.SnapshotPayload = cloneBytes(event.Payload)
	}
	state.LastEventID = event.EventID
	state.LastEventType = event.EventType
	state.LastVisibility = event.Visibility
	state.LastOccurredAt = event.OccurredAt
	state.UpdatedAt = event.UpdatedAt
	session.states[event.EventKey] = cloneMessageEventState(state)
	session.updated = now

	result := cachedMessageEventResult(event, state)
	session.applied[event.EventID] = cloneMessageEventAppendResult(result)
	return result, nil
}

func (c *messageEventStreamCache) mergeTerminalPayload(event metadb.MessageEventAppend) metadb.MessageEventAppend {
	if c == nil || !isMessageEventTerminalEvent(event.EventType) {
		return event
	}
	key := messageEventCacheKey(event)
	c.mu.Lock()
	defer c.mu.Unlock()

	session := c.sessions[key]
	if session == nil {
		return event
	}
	state := session.states[event.EventKey]
	if state.EventKey == "" || len(state.SnapshotPayload) == 0 {
		return event
	}
	event.Payload = mergeMessageEventTerminalPayload(event.Payload, state.SnapshotPayload)
	return event
}

func (c *messageEventStreamCache) markTerminalPersisted(event metadb.MessageEventAppend, result metadb.MessageEventAppendResult) {
	if c == nil || !isMessageEventTerminalEvent(event.EventType) {
		return
	}
	key := messageEventCacheKey(event)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	session := c.sessions[key]
	if session == nil {
		return
	}
	session.updated = now
	state := cloneMessageEventState(result.State)
	if state.EventKey == "" {
		state = cachedMessageEventState(event)
		state.Status = result.Status
		state.LastMsgEventSeq = result.MsgEventSeq
	}
	session.states[result.EventKey] = state
	session.applied[event.EventID] = cloneMessageEventAppendResult(result)
}

func (c *messageEventStreamCache) openStatesForFinish(event metadb.MessageEventAppend) []metadb.MessageEventState {
	if c == nil || event.EventType != metadb.EventTypeStreamFinish {
		return nil
	}
	key := messageEventCacheKey(event)
	c.mu.Lock()
	defer c.mu.Unlock()

	session := c.sessions[key]
	if session == nil || len(session.states) == 0 {
		return nil
	}
	out := make([]metadb.MessageEventState, 0, len(session.states))
	for _, state := range session.states {
		if state.EventKey == "" || state.EventKey == metadb.EventKeyFinish || isMessageEventTerminalStatus(state.Status) {
			continue
		}
		out = append(out, cloneMessageEventState(state))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EventKey < out[j].EventKey })
	return out
}

func (c *messageEventStreamCache) states(key metadb.MessageEventMessageKey) []metadb.MessageEventState {
	if c == nil {
		return nil
	}
	cacheKey := messageEventStreamCacheKey{
		channelID:   strings.TrimSpace(key.ChannelID),
		channelType: key.ChannelType,
		clientMsgNo: strings.TrimSpace(key.ClientMsgNo),
	}
	if cacheKey.channelID == "" || cacheKey.channelType <= 0 || cacheKey.clientMsgNo == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	session := c.sessions[cacheKey]
	if session == nil || len(session.states) == 0 {
		return nil
	}
	out := make([]metadb.MessageEventState, 0, len(session.states))
	for _, state := range session.states {
		out = append(out, cloneMessageEventState(state))
	}
	return out
}

func (c *messageEventStreamCache) sessionLocked(key messageEventStreamCacheKey, now time.Time) (*messageEventStreamCacheSession, error) {
	session := c.sessions[key]
	if session != nil {
		session.updated = now
		return session, nil
	}
	if len(c.sessions) >= c.maxSessions {
		if !c.evictOldestTerminalLocked() {
			return nil, ErrBackpressured
		}
	}
	session = &messageEventStreamCacheSession{
		states:  make(map[string]metadb.MessageEventState),
		applied: make(map[string]metadb.MessageEventAppendResult),
		updated: now,
	}
	c.sessions[key] = session
	return session, nil
}

func (c *messageEventStreamCache) evictOldestTerminalLocked() bool {
	var (
		oldestKey messageEventStreamCacheKey
		oldestSet bool
		oldestAt  time.Time
	)
	for key, session := range c.sessions {
		if session == nil {
			delete(c.sessions, key)
			return true
		}
		if !isMessageEventTerminalCacheSession(session) {
			continue
		}
		if !oldestSet || session.updated.Before(oldestAt) {
			oldestKey = key
			oldestAt = session.updated
			oldestSet = true
		}
	}
	if oldestSet {
		delete(c.sessions, oldestKey)
		return true
	}
	return false
}

func isMessageEventTerminalCacheSession(session *messageEventStreamCacheSession) bool {
	if session == nil || len(session.states) == 0 {
		return true
	}
	for _, state := range session.states {
		if !isMessageEventTerminalStatus(state.Status) {
			return false
		}
	}
	return true
}

type messageEventAppendRPCRequest struct {
	Op    string                          `json:"op,omitempty"`
	Event metadb.MessageEventAppend       `json:"event,omitempty"`
	Keys  []metadb.MessageEventMessageKey `json:"keys,omitempty"`
	Limit int                             `json:"limit,omitempty"`
}

type messageEventAppendRPCResponse struct {
	Result metadb.MessageEventAppendResult `json:"result,omitempty"`
	States []messageEventStatesRPCEntry    `json:"states,omitempty"`
}

type messageEventStatesRPCEntry struct {
	Key    metadb.MessageEventMessageKey `json:"key"`
	States []metadb.MessageEventState    `json:"states"`
}

type messageEventAppendRPCHandler struct {
	node *Node
}

func (h messageEventAppendRPCHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	var req messageEventAppendRPCRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	switch req.Op {
	case "", "append":
		result, err := h.node.appendMessageEventLocal(ctx, req.Event)
		if err != nil {
			return nil, err
		}
		return json.Marshal(messageEventAppendRPCResponse{Result: result})
	case "states_batch":
		rows, err := h.node.getMessageEventStatesBatchLocal(ctx, req.Keys, req.Limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(messageEventAppendRPCResponse{States: messageEventStateEntriesFromMap(rows)})
	default:
		return nil, metadb.ErrInvalidArgument
	}
}

func (n *Node) forwardMessageEventAppend(ctx context.Context, nodeID uint64, event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error) {
	body, err := json.Marshal(messageEventAppendRPCRequest{Event: event})
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	respBody, err := n.CallRPC(ctx, nodeID, clusternet.RPCMessageEventAppend, body)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	var resp messageEventAppendRPCResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	resp.Result.State.SnapshotPayload = cloneBytes(resp.Result.State.SnapshotPayload)
	return resp.Result, nil
}

func (n *Node) forwardMessageEventStatesBatch(ctx context.Context, nodeID uint64, keys []metadb.MessageEventMessageKey, limit int) (map[metadb.MessageEventMessageKey][]metadb.MessageEventState, error) {
	body, err := json.Marshal(messageEventAppendRPCRequest{Op: "states_batch", Keys: keys, Limit: limit})
	if err != nil {
		return nil, err
	}
	respBody, err := n.CallRPC(ctx, nodeID, clusternet.RPCMessageEventAppend, body)
	if err != nil {
		return nil, err
	}
	var resp messageEventAppendRPCResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	return messageEventStateMapFromEntries(resp.States), nil
}

func messageEventStateEntriesFromMap(rows map[metadb.MessageEventMessageKey][]metadb.MessageEventState) []messageEventStatesRPCEntry {
	entries := make([]messageEventStatesRPCEntry, 0, len(rows))
	for key, states := range rows {
		entries = append(entries, messageEventStatesRPCEntry{Key: key, States: cloneMessageEventStates(states)})
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i].Key, entries[j].Key
		if left.ChannelID != right.ChannelID {
			return left.ChannelID < right.ChannelID
		}
		if left.ChannelType != right.ChannelType {
			return left.ChannelType < right.ChannelType
		}
		return left.ClientMsgNo < right.ClientMsgNo
	})
	return entries
}

func messageEventStateMapFromEntries(entries []messageEventStatesRPCEntry) map[metadb.MessageEventMessageKey][]metadb.MessageEventState {
	out := make(map[metadb.MessageEventMessageKey][]metadb.MessageEventState, len(entries))
	for _, entry := range entries {
		if len(entry.States) == 0 {
			continue
		}
		out[entry.Key] = cloneMessageEventStates(entry.States)
	}
	return out
}

func normalizeClusterMessageEventAppend(event metadb.MessageEventAppend) (metadb.MessageEventAppend, error) {
	event.ChannelID = strings.TrimSpace(event.ChannelID)
	event.ClientMsgNo = strings.TrimSpace(event.ClientMsgNo)
	event.EventID = strings.TrimSpace(event.EventID)
	event.EventKey = strings.TrimSpace(event.EventKey)
	event.EventType = strings.ToLower(strings.TrimSpace(event.EventType))
	event.Visibility = strings.TrimSpace(event.Visibility)
	event.Payload = cloneBytes(event.Payload)
	if event.ChannelID == "" || event.ChannelType <= 0 || event.ClientMsgNo == "" || event.EventID == "" || event.EventType == "" {
		return metadb.MessageEventAppend{}, metadb.ErrInvalidArgument
	}
	if event.EventKey == "" {
		event.EventKey = metadb.EventKeyDefault
	}
	if event.EventType == metadb.EventTypeStreamFinish {
		event.EventKey = metadb.EventKeyFinish
	}
	if event.Visibility == "" {
		event.Visibility = metadb.VisibilityPublic
	}
	switch event.EventType {
	case metadb.EventTypeStreamOpen,
		metadb.EventTypeStreamDelta,
		metadb.EventTypeStreamClose,
		metadb.EventTypeStreamError,
		metadb.EventTypeStreamCancel,
		metadb.EventTypeStreamSnapshot,
		metadb.EventTypeStreamFinish:
		return event, nil
	default:
		return metadb.MessageEventAppend{}, metadb.ErrInvalidArgument
	}
}

func (n *Node) appendMessageEventLocal(ctx context.Context, event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error) {
	event, err := normalizeClusterMessageEventAppend(event)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	route, err := n.RouteKey(event.ChannelID)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	if route.Leader != n.cfg.NodeID {
		return metadb.MessageEventAppendResult{}, ErrNotLeader
	}
	if isMessageEventCacheOnlyEvent(event.EventType) {
		return n.messageEventStreamCache.appendCached(event)
	}
	if isMessageEventTerminalEvent(event.EventType) {
		if err := n.flushMessageEventCachedLanesOnFinish(ctx, event); err != nil {
			return metadb.MessageEventAppendResult{}, err
		}
		event = n.messageEventStreamCache.mergeTerminalPayload(event)
		result, err := n.appendMessageEventDurable(ctx, event)
		if err != nil {
			return metadb.MessageEventAppendResult{}, err
		}
		n.messageEventStreamCache.markTerminalPersisted(event, result)
		return result, nil
	}
	return n.appendMessageEventDurable(ctx, event)
}

func (n *Node) flushMessageEventCachedLanesOnFinish(ctx context.Context, event metadb.MessageEventAppend) error {
	if event.EventType != metadb.EventTypeStreamFinish {
		return nil
	}
	for _, state := range n.messageEventStreamCache.openStatesForFinish(event) {
		laneEvent := finishFlushMessageEvent(event, state)
		result, err := n.appendMessageEventDurable(ctx, laneEvent)
		if err != nil {
			return err
		}
		n.messageEventStreamCache.markTerminalPersisted(laneEvent, result)
	}
	return nil
}

func finishFlushMessageEvent(finish metadb.MessageEventAppend, state metadb.MessageEventState) metadb.MessageEventAppend {
	event := finish
	event.EventID = finishFlushMessageEventID(finish.EventID, state.EventKey)
	event.EventKey = state.EventKey
	event.EventType = metadb.EventTypeStreamClose
	event.Payload = mergeMessageEventTerminalPayload(finish.Payload, state.SnapshotPayload)
	return event
}

func finishFlushMessageEventID(finishEventID string, eventKey string) string {
	return finishEventID + "/flush/" + eventKey
}

func (n *Node) appendMessageEventDurable(ctx context.Context, event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error) {
	command, err := metafsm.EncodeAppendMessageEventCommandChecked(event)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	resultBytes, err := n.ProposeResult(ctx, ProposeRequest{Key: event.ChannelID, Command: command})
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	result, err := metafsm.DecodeAppendMessageEventResult(resultBytes)
	if err != nil {
		if string(resultBytes) == metafsm.ApplyResultStaleMeta {
			return metadb.MessageEventAppendResult{}, metadb.ErrStaleMeta
		}
		return metadb.MessageEventAppendResult{}, err
	}
	return result, nil
}

func messageEventCacheKey(event metadb.MessageEventAppend) messageEventStreamCacheKey {
	return messageEventStreamCacheKey{
		channelID:   event.ChannelID,
		channelType: event.ChannelType,
		clientMsgNo: event.ClientMsgNo,
	}
}

func cachedMessageEventState(event metadb.MessageEventAppend) metadb.MessageEventState {
	return metadb.MessageEventState{
		ChannelID:       event.ChannelID,
		ChannelType:     event.ChannelType,
		ClientMsgNo:     event.ClientMsgNo,
		EventKey:        event.EventKey,
		Status:          metadb.EventStatusOpen,
		LastEventID:     event.EventID,
		LastEventType:   event.EventType,
		LastVisibility:  event.Visibility,
		LastOccurredAt:  event.OccurredAt,
		UpdatedAt:       event.UpdatedAt,
		LastMsgEventSeq: 0,
		EndReason:       0,
		Error:           "",
	}
}

func cachedMessageEventResult(event metadb.MessageEventAppend, state metadb.MessageEventState) metadb.MessageEventAppendResult {
	state = cloneMessageEventState(state)
	return metadb.MessageEventAppendResult{
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

func reduceCachedMessageEventDelta(existing []byte, payload []byte) []byte {
	var delta struct {
		Kind  string `json:"kind"`
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(payload, &delta); err != nil || delta.Kind != metadb.SnapshotKindText {
		return cloneBytes(payload)
	}
	text := ""
	var current struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(existing, &current); err == nil && current.Kind == metadb.SnapshotKindText {
		text = current.Text
	}
	out, err := json.Marshal(struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}{Kind: metadb.SnapshotKindText, Text: text + delta.Delta})
	if err != nil {
		return cloneBytes(payload)
	}
	return out
}

func mergeMessageEventTerminalPayload(payload []byte, snapshot []byte) []byte {
	if len(snapshot) == 0 {
		return cloneBytes(payload)
	}
	body := map[string]json.RawMessage{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &body); err != nil {
			raw, marshalErr := json.Marshal(string(payload))
			if marshalErr != nil {
				return cloneBytes(payload)
			}
			body["raw_payload"] = raw
		}
	}
	if raw, exists := body["snapshot"]; !exists || isEmptyMessageEventSnapshotRaw(raw) {
		body["snapshot"] = cloneJSONRawMessage(snapshot)
	}
	out, err := json.Marshal(body)
	if err != nil {
		return cloneBytes(payload)
	}
	return out
}

func isEmptyMessageEventSnapshotRaw(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null":
		return true
	default:
		return false
	}
}

func cloneJSONRawMessage(in []byte) json.RawMessage {
	if json.Valid(in) {
		return cloneBytes(in)
	}
	out, err := json.Marshal(string(in))
	if err != nil {
		return nil
	}
	return out
}

func isMessageEventCacheOnlyEvent(eventType string) bool {
	switch eventType {
	case metadb.EventTypeStreamOpen, metadb.EventTypeStreamDelta, metadb.EventTypeStreamSnapshot:
		return true
	default:
		return false
	}
}

func isMessageEventTerminalEvent(eventType string) bool {
	switch eventType {
	case metadb.EventTypeStreamClose, metadb.EventTypeStreamError, metadb.EventTypeStreamCancel, metadb.EventTypeStreamFinish:
		return true
	default:
		return false
	}
}

func isMessageEventTerminalStatus(status string) bool {
	switch status {
	case metadb.EventStatusClosed, metadb.EventStatusError, metadb.EventStatusCancelled:
		return true
	default:
		return false
	}
}

func cloneMessageEventState(state metadb.MessageEventState) metadb.MessageEventState {
	state.SnapshotPayload = cloneBytes(state.SnapshotPayload)
	return state
}

func cloneMessageEventStates(states []metadb.MessageEventState) []metadb.MessageEventState {
	out := make([]metadb.MessageEventState, len(states))
	for i, state := range states {
		out[i] = cloneMessageEventState(state)
	}
	return out
}

func cloneMessageEventAppendResult(result metadb.MessageEventAppendResult) metadb.MessageEventAppendResult {
	result.State = cloneMessageEventState(result.State)
	return result
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
