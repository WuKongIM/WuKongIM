package cluster

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

const (
	defaultMessageEventStreamCacheMaxSessions = 50000
	defaultMessageEventFinishCoalesceWindow   = time.Millisecond
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

type messageEventFinishCoalesceKey struct {
	channelID   string
	channelType int64
}

type messageEventFinishCoalesceRequest struct {
	ctx    context.Context
	event  metadb.MessageEventAppend
	events []metadb.MessageEventAppend
	done   chan messageEventFinishCoalesceResult
}

type messageEventFinishCoalesceResult struct {
	result metadb.MessageEventAppendResult
	path   string
	err    error
}

type messageEventFinishCoalesceGroup struct {
	requests []*messageEventFinishCoalesceRequest
}

// messageEventFinishCoalescer batches concurrent stream.finish flushes for one channel.
type messageEventFinishCoalescer struct {
	mu     sync.Mutex
	window time.Duration
	groups map[messageEventFinishCoalesceKey]*messageEventFinishCoalesceGroup
}

func newMessageEventFinishCoalescer(window time.Duration) *messageEventFinishCoalescer {
	if window <= 0 {
		return nil
	}
	return &messageEventFinishCoalescer{
		window: window,
		groups: make(map[messageEventFinishCoalesceKey]*messageEventFinishCoalesceGroup),
	}
}

// messageEventStreamCache keeps in-flight stream projections on the Slot leader.
type messageEventStreamCache struct {
	mu           sync.Mutex
	maxSessions  int
	sessions     map[messageEventStreamCacheKey]*messageEventStreamCacheSession
	openLanes    int
	payloadBytes int64
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
	result, _, err := c.appendCachedObserved(event)
	return result, err
}

func (c *messageEventStreamCache) appendCachedObserved(event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, MessageEventStreamCacheObservation, error) {
	if c == nil {
		return cachedMessageEventResult(event, cachedMessageEventState(event)), MessageEventStreamCacheObservation{}, nil
	}
	key := messageEventCacheKey(event)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	session, err := c.sessionLocked(key, now)
	if err != nil {
		return metadb.MessageEventAppendResult{}, c.observationLocked(), err
	}
	if result, ok := session.applied[event.EventID]; ok {
		return cloneMessageEventAppendResult(result), c.observationLocked(), nil
	}

	state := session.states[event.EventKey]
	oldState, hadState := session.states[event.EventKey]
	if state.EventKey == "" {
		state = cachedMessageEventState(event)
	}
	if isMessageEventTerminalStatus(state.Status) {
		result := cachedMessageEventResult(event, state)
		session.applied[event.EventID] = cloneMessageEventAppendResult(result)
		return result, c.observationLocked(), nil
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
	c.accountStateReplaceLocked(oldState, hadState, state)
	session.updated = now

	result := cachedMessageEventResult(event, state)
	session.applied[event.EventID] = cloneMessageEventAppendResult(result)
	return result, c.observationLocked(), nil
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
	oldState, hadState := session.states[result.EventKey]
	state := cloneMessageEventState(result.State)
	if state.EventKey == "" {
		state = cachedMessageEventState(event)
		state.Status = result.Status
		state.LastMsgEventSeq = result.MsgEventSeq
	}
	session.states[result.EventKey] = state
	c.accountStateReplaceLocked(oldState, hadState, state)
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

func (c *messageEventStreamCache) remove(event metadb.MessageEventAppend) {
	if c == nil {
		return
	}
	key := messageEventCacheKey(event)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteSessionLocked(key)
}

func (c *messageEventStreamCache) removeObserved(event metadb.MessageEventAppend) MessageEventStreamCacheObservation {
	if c == nil {
		return MessageEventStreamCacheObservation{}
	}
	key := messageEventCacheKey(event)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteSessionLocked(key)
	return c.observationLocked()
}

func (c *messageEventStreamCache) removeHashSlotsObserved(hashSlots map[uint16]struct{}, hashSlotCount uint16) MessageEventStreamCacheObservation {
	if c == nil {
		return MessageEventStreamCacheObservation{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(hashSlots) == 0 || hashSlotCount == 0 {
		return c.observationLocked()
	}
	for key := range c.sessions {
		hashSlot := routing.HashSlotForKey(key.channelID, hashSlotCount)
		if _, ok := hashSlots[hashSlot]; ok {
			c.deleteSessionLocked(key)
		}
	}
	return c.observationLocked()
}

func (c *messageEventStreamCache) observation() MessageEventStreamCacheObservation {
	if c == nil {
		return MessageEventStreamCacheObservation{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observationLocked()
}

func (c *messageEventStreamCache) observationLocked() MessageEventStreamCacheObservation {
	return MessageEventStreamCacheObservation{
		Sessions:     len(c.sessions),
		OpenLanes:    c.openLanes,
		PayloadBytes: c.payloadBytes,
		MaxSessions:  c.maxSessions,
	}
}

func (c *messageEventStreamCache) deleteSessionLocked(key messageEventStreamCacheKey) {
	session := c.sessions[key]
	if session != nil {
		for _, state := range session.states {
			c.accountStateRemoveLocked(state)
		}
	}
	delete(c.sessions, key)
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
			c.deleteSessionLocked(key)
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
		c.deleteSessionLocked(oldestKey)
		return true
	}
	return false
}

func (c *messageEventStreamCache) accountStateReplaceLocked(oldState metadb.MessageEventState, hadOld bool, newState metadb.MessageEventState) {
	if hadOld {
		c.accountStateRemoveLocked(oldState)
	}
	c.accountStateAddLocked(newState)
}

func (c *messageEventStreamCache) accountStateAddLocked(state metadb.MessageEventState) {
	if state.EventKey == "" {
		return
	}
	if !isMessageEventTerminalStatus(state.Status) {
		c.openLanes++
	}
	c.payloadBytes += int64(len(state.SnapshotPayload))
}

func (c *messageEventStreamCache) accountStateRemoveLocked(state metadb.MessageEventState) {
	if state.EventKey == "" {
		return
	}
	if !isMessageEventTerminalStatus(state.Status) && c.openLanes > 0 {
		c.openLanes--
	}
	c.payloadBytes -= int64(len(state.SnapshotPayload))
	if c.payloadBytes < 0 {
		c.payloadBytes = 0
	}
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
		start := time.Now()
		result, observation, err := n.messageEventStreamCache.appendCachedObserved(event)
		n.observeMessageEventAppend(messageEventPathCache, event, messageEventResultForError(err), time.Since(start))
		n.setMessageEventStreamCache(observation)
		return result, err
	}
	if event.EventType == metadb.EventTypeStreamFinish {
		return n.appendMessageEventFinishLocal(ctx, event)
	}
	if isMessageEventTerminalEvent(event.EventType) {
		start := time.Now()
		event = n.messageEventStreamCache.mergeTerminalPayload(event)
		result, err := n.appendMessageEventDurable(ctx, event)
		if err != nil {
			n.observeMessageEventAppend(messageEventPathDurable, event, messageEventResultForError(err), time.Since(start))
			return metadb.MessageEventAppendResult{}, err
		}
		n.messageEventStreamCache.markTerminalPersisted(event, result)
		n.setMessageEventStreamCache(n.messageEventStreamCache.observation())
		n.observeMessageEventAppend(messageEventPathDurable, event, messageEventResultOK, time.Since(start))
		return result, nil
	}
	start := time.Now()
	result, err := n.appendMessageEventDurable(ctx, event)
	n.observeMessageEventAppend(messageEventPathDurable, event, messageEventResultForError(err), time.Since(start))
	return result, err
}

func (n *Node) appendMessageEventFinishLocal(ctx context.Context, event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error) {
	start := time.Now()
	stageStart := time.Now()
	openStates := n.messageEventStreamCache.openStatesForFinish(event)
	cacheOpenDur := time.Since(stageStart)
	if len(openStates) == 0 && !messageEventPayloadHasSnapshot(event.Payload) {
		n.observeMessageEventAppendStage(messageEventPathFinishBatch, messageEventResultCacheMiss, messageEventAppendStageFinishCacheOpen, cacheOpenDur)
		n.observeMessageEventAppend(messageEventPathFinishBatch, event, messageEventResultCacheMiss, time.Since(start))
		n.setMessageEventStreamCache(n.messageEventStreamCache.observation())
		return metadb.MessageEventAppendResult{}, ErrMessageEventStreamCacheMiss
	}
	stageStart = time.Now()
	events := make([]metadb.MessageEventAppend, 0, len(openStates)+1)
	for _, state := range openStates {
		events = append(events, finishFlushMessageEvent(event, state))
	}
	events = append(events, event)
	batchBuildDur := time.Since(stageStart)
	var (
		result metadb.MessageEventAppendResult
		path   string
		err    error
	)
	result, path, err = n.appendMessageEventFinishPrepared(ctx, event, events)
	appendResult := messageEventResultForError(err)
	n.observeMessageEventAppendStage(path, appendResult, messageEventAppendStageFinishCacheOpen, cacheOpenDur)
	n.observeMessageEventAppendStage(path, appendResult, messageEventAppendStageFinishBatchBuild, batchBuildDur)
	if err != nil {
		n.observeMessageEventAppend(path, event, appendResult, time.Since(start))
		return metadb.MessageEventAppendResult{}, err
	}
	stageStart = time.Now()
	observation := n.messageEventStreamCache.removeObserved(event)
	n.observeMessageEventAppendStage(path, messageEventResultOK, messageEventAppendStageFinishCacheRemove, time.Since(stageStart))
	n.setMessageEventStreamCache(observation)
	n.observeMessageEventAppend(path, event, messageEventResultOK, time.Since(start))
	return result, nil
}

func (n *Node) appendMessageEventFinishPrepared(ctx context.Context, event metadb.MessageEventAppend, events []metadb.MessageEventAppend) (metadb.MessageEventAppendResult, string, error) {
	if n == nil || n.messageEventFinishCoalescer == nil {
		return n.appendMessageEventFinishPreparedDirect(ctx, events)
	}
	return n.messageEventFinishCoalescer.append(ctx, n, event, events)
}

func (n *Node) appendMessageEventFinishPreparedDirect(ctx context.Context, events []metadb.MessageEventAppend) (metadb.MessageEventAppendResult, string, error) {
	if len(events) == 1 {
		result, err := n.appendMessageEventDurable(ctx, events[0])
		return result, messageEventPathDurable, err
	}
	results, err := n.appendMessageEventsDurableResults(ctx, events, messageEventPathFinishBatch)
	if err != nil {
		return metadb.MessageEventAppendResult{}, messageEventPathFinishBatch, err
	}
	return results[len(results)-1], messageEventPathFinishBatch, nil
}

func (c *messageEventFinishCoalescer) append(ctx context.Context, n *Node, event metadb.MessageEventAppend, events []metadb.MessageEventAppend) (metadb.MessageEventAppendResult, string, error) {
	if c == nil {
		return n.appendMessageEventFinishPreparedDirect(ctx, events)
	}
	req := &messageEventFinishCoalesceRequest{
		ctx:    ctx,
		event:  event,
		events: cloneMessageEventAppends(events),
		done:   make(chan messageEventFinishCoalesceResult, 1),
	}
	key := messageEventFinishCoalesceKey{channelID: event.ChannelID, channelType: event.ChannelType}
	c.mu.Lock()
	group := c.groups[key]
	if group == nil {
		group = &messageEventFinishCoalesceGroup{}
		c.groups[key] = group
		time.AfterFunc(c.window, func() { c.flush(n, key) })
	}
	group.requests = append(group.requests, req)
	c.mu.Unlock()

	select {
	case result := <-req.done:
		return result.result, result.path, result.err
	case <-ctx.Done():
		if c.remove(key, req) {
			return metadb.MessageEventAppendResult{}, messageEventPathFinishBatch, ctx.Err()
		}
		result := <-req.done
		return result.result, result.path, result.err
	}
}

func (c *messageEventFinishCoalescer) remove(key messageEventFinishCoalesceKey, req *messageEventFinishCoalesceRequest) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	group := c.groups[key]
	if group == nil {
		return false
	}
	for i, candidate := range group.requests {
		if candidate != req {
			continue
		}
		group.requests = append(group.requests[:i], group.requests[i+1:]...)
		if len(group.requests) == 0 {
			delete(c.groups, key)
		}
		return true
	}
	return false
}

func (c *messageEventFinishCoalescer) flush(n *Node, key messageEventFinishCoalesceKey) {
	c.mu.Lock()
	group := c.groups[key]
	if group == nil {
		c.mu.Unlock()
		return
	}
	delete(c.groups, key)
	requests := append([]*messageEventFinishCoalesceRequest(nil), group.requests...)
	c.mu.Unlock()

	if len(requests) == 0 {
		return
	}
	live := requests[:0]
	for _, req := range requests {
		if req.ctx != nil && req.ctx.Err() != nil {
			req.done <- messageEventFinishCoalesceResult{path: messageEventPathFinishBatch, err: req.ctx.Err()}
			continue
		}
		live = append(live, req)
	}
	if len(live) == 0 {
		return
	}
	path := messageEventPathFinishBatch
	ctx := messageEventFinishCoalesceContext(live)
	allEvents := make([]metadb.MessageEventAppend, 0, len(live))
	for _, req := range live {
		allEvents = append(allEvents, req.events...)
	}
	results, err := n.appendMessageEventsCoalesced(ctx, allEvents)
	if len(allEvents) == 1 {
		path = messageEventPathDurable
	}
	if err != nil {
		for _, req := range live {
			req.done <- messageEventFinishCoalesceResult{path: path, err: err}
		}
		return
	}
	byEvent := make(map[messageEventResultKey]metadb.MessageEventAppendResult, len(results))
	for _, result := range results {
		byEvent[messageEventResultKey{clientMsgNo: result.ClientMsgNo, eventID: result.EventID}] = result
	}
	for _, req := range live {
		result, ok := byEvent[messageEventResultKey{clientMsgNo: req.event.ClientMsgNo, eventID: req.event.EventID}]
		if !ok {
			req.done <- messageEventFinishCoalesceResult{path: path, err: metadb.ErrCorruptValue}
			continue
		}
		req.done <- messageEventFinishCoalesceResult{result: result, path: path}
	}
}

func (n *Node) appendMessageEventsCoalesced(ctx context.Context, events []metadb.MessageEventAppend) ([]metadb.MessageEventAppendResult, error) {
	if len(events) == 1 {
		result, err := n.appendMessageEventDurable(ctx, events[0])
		if err != nil {
			return nil, err
		}
		return []metadb.MessageEventAppendResult{result}, nil
	}
	return n.appendMessageEventsDurableResults(ctx, events, messageEventPathFinishBatch)
}

func messageEventFinishCoalesceContext(requests []*messageEventFinishCoalesceRequest) context.Context {
	for _, req := range requests {
		if req.ctx != nil && req.ctx.Err() == nil {
			return req.ctx
		}
	}
	return context.Background()
}

type messageEventResultKey struct {
	clientMsgNo string
	eventID     string
}

func (n *Node) clearMessageEventStreamCacheForLostLocalAuthority(before, after *routing.Table) {
	lost := n.messageEventLostLocalAuthorityHashSlots(before, after)
	if len(lost) == 0 {
		return
	}
	n.setMessageEventStreamCache(n.messageEventStreamCache.removeHashSlotsObserved(lost, before.HashSlotCount))
}

func (n *Node) messageEventLostLocalAuthorityHashSlots(before, after *routing.Table) map[uint16]struct{} {
	if n == nil || n.messageEventStreamCache == nil || before == nil || after == nil || before.HashSlotCount == 0 {
		return nil
	}
	lost := make(map[uint16]struct{})
	for hashSlot, slotID := range before.HashToSlot {
		if slotID == 0 || before.SlotLeaders[slotID] != n.cfg.NodeID {
			continue
		}
		current, ok := routeAuthorityFromTable(after, uint16(hashSlot))
		if !ok || current.leaderNodeID != n.cfg.NodeID {
			lost[uint16(hashSlot)] = struct{}{}
		}
	}
	return lost
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
	encodeStart := time.Now()
	command, err := metafsm.EncodeAppendMessageEventCommandChecked(event)
	encodeDur := time.Since(encodeStart)
	if err != nil {
		n.observeMessageEventProposeStage(messageEventPathDurable, messageEventResultForError(err), messageEventProposeStageEncode, encodeDur)
		return metadb.MessageEventAppendResult{}, err
	}
	start := time.Now()
	proposeStart := time.Now()
	resultBytes, err := n.ProposeResult(n.messageEventProposeStageContext(ctx, messageEventPathDurable), ProposeRequest{Key: event.ChannelID, Command: command})
	proposeDur := time.Since(proposeStart)
	if err != nil {
		resultClass := messageEventResultForError(err)
		n.observeMessageEventProposeStage(messageEventPathDurable, resultClass, messageEventProposeStageEncode, encodeDur)
		n.observeMessageEventProposeStage(messageEventPathDurable, resultClass, messageEventProposeStageSlotProposeWait, proposeDur)
		n.observeMessageEventPropose(messageEventPathDurable, resultClass, 1, time.Since(start))
		return metadb.MessageEventAppendResult{}, err
	}
	decodeStart := time.Now()
	result, err := metafsm.DecodeAppendMessageEventResult(resultBytes)
	decodeDur := time.Since(decodeStart)
	if err != nil {
		resultClass := messageEventResultForError(err)
		if string(resultBytes) == metafsm.ApplyResultStaleMeta {
			resultClass = messageEventResultInvalid
			n.observeMessageEventProposeStage(messageEventPathDurable, resultClass, messageEventProposeStageEncode, encodeDur)
			n.observeMessageEventProposeStage(messageEventPathDurable, resultClass, messageEventProposeStageSlotProposeWait, proposeDur)
			n.observeMessageEventProposeStage(messageEventPathDurable, resultClass, messageEventProposeStageDecode, decodeDur)
			n.observeMessageEventPropose(messageEventPathDurable, resultClass, 1, time.Since(start))
			return metadb.MessageEventAppendResult{}, metadb.ErrStaleMeta
		}
		n.observeMessageEventProposeStage(messageEventPathDurable, resultClass, messageEventProposeStageEncode, encodeDur)
		n.observeMessageEventProposeStage(messageEventPathDurable, resultClass, messageEventProposeStageSlotProposeWait, proposeDur)
		n.observeMessageEventProposeStage(messageEventPathDurable, resultClass, messageEventProposeStageDecode, decodeDur)
		n.observeMessageEventPropose(messageEventPathDurable, resultClass, 1, time.Since(start))
		return metadb.MessageEventAppendResult{}, err
	}
	n.observeMessageEventProposeStage(messageEventPathDurable, messageEventResultOK, messageEventProposeStageEncode, encodeDur)
	n.observeMessageEventProposeStage(messageEventPathDurable, messageEventResultOK, messageEventProposeStageSlotProposeWait, proposeDur)
	n.observeMessageEventProposeStage(messageEventPathDurable, messageEventResultOK, messageEventProposeStageDecode, decodeDur)
	n.observeMessageEventPropose(messageEventPathDurable, messageEventResultOK, 1, time.Since(start))
	return result, nil
}

func (n *Node) appendMessageEventsDurable(ctx context.Context, events []metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error) {
	results, err := n.appendMessageEventsDurableResults(ctx, events, messageEventPathFinishBatch)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	return results[len(results)-1], nil
}

func (n *Node) appendMessageEventsDurableResults(ctx context.Context, events []metadb.MessageEventAppend, path string) ([]metadb.MessageEventAppendResult, error) {
	encodeStart := time.Now()
	command, err := metafsm.EncodeAppendMessageEventsCommandChecked(events)
	encodeDur := time.Since(encodeStart)
	if err != nil {
		n.observeMessageEventProposeStage(path, messageEventResultForError(err), messageEventProposeStageEncode, encodeDur)
		return nil, err
	}
	start := time.Now()
	proposeStart := time.Now()
	resultBytes, err := n.ProposeResult(n.messageEventProposeStageContext(ctx, path), ProposeRequest{Key: events[len(events)-1].ChannelID, Command: command})
	proposeDur := time.Since(proposeStart)
	if err != nil {
		resultClass := messageEventResultForError(err)
		n.observeMessageEventProposeStage(path, resultClass, messageEventProposeStageEncode, encodeDur)
		n.observeMessageEventProposeStage(path, resultClass, messageEventProposeStageSlotProposeWait, proposeDur)
		n.observeMessageEventPropose(path, resultClass, len(events), time.Since(start))
		return nil, err
	}
	decodeStart := time.Now()
	results, err := metafsm.DecodeAppendMessageEventResults(resultBytes)
	decodeDur := time.Since(decodeStart)
	if err != nil {
		resultClass := messageEventResultForError(err)
		if string(resultBytes) == metafsm.ApplyResultStaleMeta {
			resultClass = messageEventResultInvalid
			n.observeMessageEventProposeStage(path, resultClass, messageEventProposeStageEncode, encodeDur)
			n.observeMessageEventProposeStage(path, resultClass, messageEventProposeStageSlotProposeWait, proposeDur)
			n.observeMessageEventProposeStage(path, resultClass, messageEventProposeStageDecode, decodeDur)
			n.observeMessageEventPropose(path, resultClass, len(events), time.Since(start))
			return nil, metadb.ErrStaleMeta
		}
		n.observeMessageEventProposeStage(path, resultClass, messageEventProposeStageEncode, encodeDur)
		n.observeMessageEventProposeStage(path, resultClass, messageEventProposeStageSlotProposeWait, proposeDur)
		n.observeMessageEventProposeStage(path, resultClass, messageEventProposeStageDecode, decodeDur)
		n.observeMessageEventPropose(path, resultClass, len(events), time.Since(start))
		return nil, err
	}
	n.observeMessageEventProposeStage(path, messageEventResultOK, messageEventProposeStageEncode, encodeDur)
	n.observeMessageEventProposeStage(path, messageEventResultOK, messageEventProposeStageSlotProposeWait, proposeDur)
	n.observeMessageEventProposeStage(path, messageEventResultOK, messageEventProposeStageDecode, decodeDur)
	n.observeMessageEventPropose(path, messageEventResultOK, len(events), time.Since(start))
	return results, nil
}

func (n *Node) messageEventProposeStageContext(ctx context.Context, path string) context.Context {
	next := propose.StageObserverFromContext(ctx)
	if n == nil || n.cfg.MessageEvent.Observer == nil {
		return ctx
	}
	if _, ok := n.cfg.MessageEvent.Observer.(MessageEventProposeStageObserver); !ok && next == nil {
		return ctx
	}
	return propose.WithStageObserver(ctx, messageEventProposeStageAdapter{
		node: n,
		path: path,
		next: next,
	})
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

func messageEventPayloadHasSnapshot(payload []byte) bool {
	body := map[string]json.RawMessage{}
	if len(payload) == 0 || json.Unmarshal(payload, &body) != nil {
		return false
	}
	raw, exists := body["snapshot"]
	return exists && !isEmptyMessageEventSnapshotRaw(raw)
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

func cloneMessageEventAppends(events []metadb.MessageEventAppend) []metadb.MessageEventAppend {
	out := make([]metadb.MessageEventAppend, len(events))
	for i, event := range events {
		out[i] = event
		out[i].Payload = cloneBytes(event.Payload)
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
