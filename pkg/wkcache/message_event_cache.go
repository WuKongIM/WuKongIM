package wkcache

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

var (
	ErrMessageEventSessionNotFound = errors.New("message event session not found")
	ErrMessageEventLaneNotFound    = errors.New("message event lane not found")
	ErrMessageEventChannelMismatch = errors.New("message event channel mismatch")
	ErrInvalidClientMsgNo          = errors.New("invalid client_msg_no")
	ErrStreamClosed                = errors.New("stream closed")
)

const (
	DefaultMessageEventCacheMaxSessions     = 50000
	DefaultMessageEventCacheSessionTTL      = 10 * time.Minute
	DefaultMessageEventCacheCleanupInterval = 1 * time.Minute
)

type MessageEventCacheOptions struct {
	MaxSessions     int
	SessionTTL      time.Duration
	CleanupInterval time.Duration
}

type MessageEventSessionMeta struct {
	ClientMsgNo string
	ChannelId   string
	ChannelType uint8
	FromUid     string
	MessageID   int64
	MessageSeq  uint64
}

type MessageEventLaneState struct {
	LaneID          string
	PersistedSeq    uint64
	Status          string
	LastEventID     string
	LastEventType   string
	LastVisibility  string
	LastOccurredAt  int64
	TextSnapshot    string
	SnapshotPayload []byte
	UpdatedAt       time.Time
}

type messageEventSession struct {
	meta      MessageEventSessionMeta
	lanes     map[string]*MessageEventLaneState
	createdAt time.Time
	updatedAt time.Time
}

type MessageEventCache struct {
	opts     MessageEventCacheOptions
	sessions map[string]*messageEventSession
	mu       sync.RWMutex
	stopCh   chan struct{}
	doneCh   chan struct{}
	wklog.Log
}

func NewMessageEventCache(opts *MessageEventCacheOptions) *MessageEventCache {
	cfg := MessageEventCacheOptions{}
	if opts != nil {
		cfg = *opts
	}
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = DefaultMessageEventCacheMaxSessions
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = DefaultMessageEventCacheSessionTTL
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = DefaultMessageEventCacheCleanupInterval
	}
	c := &MessageEventCache{
		opts:     cfg,
		sessions: make(map[string]*messageEventSession),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		Log:      wklog.NewWKLog("MessageEventCache"),
	}
	go c.cleanupLoop()
	return c
}

func (c *MessageEventCache) Close() error {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
	<-c.doneCh
	return nil
}

func (c *MessageEventCache) GetSessionMeta(clientMsgNo, channelId string, channelType uint8) (*MessageEventSessionMeta, bool) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	if clientMsgNo == "" {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	s, ok := c.sessions[clientMsgNo]
	if !ok {
		return nil, false
	}
	if s.meta.ChannelId != channelId || s.meta.ChannelType != channelType {
		return nil, false
	}
	meta := s.meta
	return &meta, true
}

func (c *MessageEventCache) UpsertSession(meta MessageEventSessionMeta, laneID string, persistedSeq uint64) (*MessageEventLaneState, error) {
	meta.ClientMsgNo = strings.TrimSpace(meta.ClientMsgNo)
	laneID = normalizeMessageEventLaneID(laneID)
	if meta.ClientMsgNo == "" {
		return nil, ErrInvalidClientMsgNo
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[meta.ClientMsgNo]
	if !ok {
		if len(c.sessions) >= c.opts.MaxSessions {
			c.evictOneLocked()
		}
		s = &messageEventSession{
			meta:      meta,
			lanes:     make(map[string]*MessageEventLaneState),
			createdAt: now,
			updatedAt: now,
		}
		c.sessions[meta.ClientMsgNo] = s
	} else {
		if s.meta.ChannelId != meta.ChannelId || s.meta.ChannelType != meta.ChannelType {
			return nil, ErrMessageEventChannelMismatch
		}
		if strings.TrimSpace(meta.FromUid) != "" {
			s.meta.FromUid = meta.FromUid
		}
		if meta.MessageID > 0 {
			s.meta.MessageID = meta.MessageID
		}
		if meta.MessageSeq > 0 {
			s.meta.MessageSeq = meta.MessageSeq
		}
		s.updatedAt = now
	}

	lane := s.lanes[laneID]
	if lane == nil {
		lane = &MessageEventLaneState{
			LaneID:    laneID,
			Status:    "open",
			UpdatedAt: now,
		}
		s.lanes[laneID] = lane
	}
	if persistedSeq > lane.PersistedSeq {
		lane.PersistedSeq = persistedSeq
	}
	if lane.Status == "" {
		lane.Status = "open"
	}
	lane.UpdatedAt = now
	return cloneLaneState(lane), nil
}

func (c *MessageEventCache) AppendTextDelta(clientMsgNo, channelId string, channelType uint8, laneID, eventID, eventType, visibility string, occurredAt int64, delta string) (*MessageEventLaneState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	laneID = normalizeMessageEventLaneID(laneID)
	if clientMsgNo == "" {
		return nil, ErrInvalidClientMsgNo
	}
	if strings.TrimSpace(delta) == "" {
		return nil, nil
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[clientMsgNo]
	if !ok {
		return nil, ErrMessageEventSessionNotFound
	}
	if s.meta.ChannelId != channelId || s.meta.ChannelType != channelType {
		return nil, ErrMessageEventChannelMismatch
	}

	lane := s.lanes[laneID]
	if lane == nil {
		return nil, ErrMessageEventLaneNotFound
	}
	if isMessageEventLaneTerminal(lane.Status) {
		return nil, ErrStreamClosed
	}

	lane.TextSnapshot += delta
	lane.LastEventID = strings.TrimSpace(eventID)
	lane.LastEventType = strings.TrimSpace(eventType)
	lane.LastVisibility = strings.TrimSpace(visibility)
	lane.LastOccurredAt = occurredAt
	lane.Status = "open"
	lane.UpdatedAt = now
	s.updatedAt = now

	return cloneLaneState(lane), nil
}

// AppendDelta handles any stream.delta event via cache. For text kind, it accumulates
// the delta string. For other kinds, it stores the raw payload as the snapshot.
func (c *MessageEventCache) AppendDelta(clientMsgNo, channelId string, channelType uint8, laneID, eventID, eventType, visibility string, occurredAt int64, payload []byte) (*MessageEventLaneState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	laneID = normalizeMessageEventLaneID(laneID)
	if clientMsgNo == "" {
		return nil, ErrInvalidClientMsgNo
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[clientMsgNo]
	if !ok {
		return nil, ErrMessageEventSessionNotFound
	}
	if s.meta.ChannelId != channelId || s.meta.ChannelType != channelType {
		return nil, ErrMessageEventChannelMismatch
	}

	lane := s.lanes[laneID]
	if lane == nil {
		return nil, ErrMessageEventLaneNotFound
	}
	if isMessageEventLaneTerminal(lane.Status) {
		return nil, ErrStreamClosed
	}

	if textDelta := extractCacheTextDelta(payload); textDelta != "" {
		lane.TextSnapshot += textDelta
	} else if len(payload) > 0 {
		lane.SnapshotPayload = append([]byte(nil), payload...)
	}

	lane.LastEventID = strings.TrimSpace(eventID)
	lane.LastEventType = strings.TrimSpace(eventType)
	lane.LastVisibility = strings.TrimSpace(visibility)
	lane.LastOccurredAt = occurredAt
	lane.Status = "open"
	lane.UpdatedAt = now
	s.updatedAt = now

	return cloneLaneState(lane), nil
}

func (c *MessageEventCache) BuildTerminalPayload(clientMsgNo, channelId string, channelType uint8, laneID string, payload []byte, eventID, eventType, visibility string, occurredAt int64) ([]byte, *MessageEventLaneState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	laneID = normalizeMessageEventLaneID(laneID)
	if clientMsgNo == "" {
		return payload, nil, ErrInvalidClientMsgNo
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[clientMsgNo]
	if !ok {
		return payload, nil, ErrMessageEventSessionNotFound
	}
	if s.meta.ChannelId != channelId || s.meta.ChannelType != channelType {
		return payload, nil, ErrMessageEventChannelMismatch
	}
	lane := s.lanes[laneID]
	if lane == nil {
		return payload, nil, ErrMessageEventLaneNotFound
	}

	lane.LastEventID = strings.TrimSpace(eventID)
	lane.LastEventType = strings.TrimSpace(eventType)
	lane.LastVisibility = strings.TrimSpace(visibility)
	lane.LastOccurredAt = occurredAt
	lane.UpdatedAt = now
	s.updatedAt = now

	if lane.TextSnapshot == "" && len(lane.SnapshotPayload) == 0 {
		return payload, cloneLaneState(lane), nil
	}
	return mergeTerminalPayloadWithSnapshot(payload, lane.TextSnapshot, lane.SnapshotPayload), cloneLaneState(lane), nil
}

func (c *MessageEventCache) MarkLanePersisted(clientMsgNo, channelId string, channelType uint8, laneID, status string, persistedSeq uint64) error {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	laneID = normalizeMessageEventLaneID(laneID)
	if clientMsgNo == "" {
		return ErrInvalidClientMsgNo
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[clientMsgNo]
	if !ok {
		return ErrMessageEventSessionNotFound
	}
	if s.meta.ChannelId != channelId || s.meta.ChannelType != channelType {
		return ErrMessageEventChannelMismatch
	}

	lane := s.lanes[laneID]
	if lane == nil {
		lane = &MessageEventLaneState{
			LaneID:    laneID,
			UpdatedAt: now,
		}
		s.lanes[laneID] = lane
	}
	if persistedSeq > lane.PersistedSeq {
		lane.PersistedSeq = persistedSeq
	}
	if strings.TrimSpace(status) != "" {
		lane.Status = strings.TrimSpace(status)
	}
	lane.UpdatedAt = now
	s.updatedAt = now

	if allMessageEventLanesTerminal(s.lanes) {
		delete(c.sessions, clientMsgNo)
	}
	return nil
}

func (c *MessageEventCache) cleanupLoop() {
	defer close(c.doneCh)

	ticker := time.NewTicker(c.opts.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.cleanupExpired()
		}
	}
}

func (c *MessageEventCache) cleanupExpired() {
	expireBefore := time.Now().Add(-c.opts.SessionTTL)

	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range c.sessions {
		if v.updatedAt.Before(expireBefore) {
			delete(c.sessions, k)
		}
	}
}

func (c *MessageEventCache) evictOneLocked() {
	var (
		oldestKey string
		oldestAt  time.Time
	)
	for k, v := range c.sessions {
		if oldestKey == "" || v.updatedAt.Before(oldestAt) {
			oldestKey = k
			oldestAt = v.updatedAt
		}
	}
	if oldestKey != "" {
		delete(c.sessions, oldestKey)
		c.Debug("evict one session from MessageEventCache", zap.String("clientMsgNo", oldestKey))
	}
}

func normalizeMessageEventLaneID(laneID string) string {
	laneID = strings.TrimSpace(laneID)
	if laneID == "" {
		return "main"
	}
	return laneID
}

func isMessageEventLaneTerminal(status string) bool {
	switch status {
	case "closed", "error", "cancelled":
		return true
	default:
		return false
	}
}

func allMessageEventLanesTerminal(lanes map[string]*MessageEventLaneState) bool {
	if len(lanes) == 0 {
		return false
	}
	for _, lane := range lanes {
		if lane == nil {
			continue
		}
		if !isMessageEventLaneTerminal(lane.Status) {
			return false
		}
	}
	return true
}

func cloneLaneState(lane *MessageEventLaneState) *MessageEventLaneState {
	if lane == nil {
		return nil
	}
	cp := *lane
	if len(lane.SnapshotPayload) > 0 {
		cp.SnapshotPayload = append([]byte(nil), lane.SnapshotPayload...)
	}
	return &cp
}

func mergeTerminalPayloadWithSnapshot(payload []byte, textSnapshot string, snapshotPayload []byte) []byte {
	body := map[string]interface{}{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &body); err != nil {
			body["raw_payload"] = string(payload)
		}
	}
	if textSnapshot != "" {
		body["snapshot"] = map[string]interface{}{
			"kind": "text",
			"text": textSnapshot,
		}
	} else if len(snapshotPayload) > 0 {
		var snap interface{}
		if err := json.Unmarshal(snapshotPayload, &snap); err == nil {
			body["snapshot"] = snap
		} else {
			body["snapshot"] = string(snapshotPayload)
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return data
}

func extractCacheTextDelta(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	kind, _ := m["kind"].(string)
	if kind != "text" {
		return ""
	}
	delta, _ := m["delta"].(string)
	return delta
}
