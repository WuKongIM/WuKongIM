package wkcache

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

var (
	ErrMessageEventSessionNotFound = errors.New("message event session not found")
	ErrMessageEventKeyNotFound     = errors.New("message event key not found")
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
	ClientMsgNo       string
	ChannelId         string // 存储的是 fakeChannelId
	ChannelType       uint8
	FromUid           string
	OriginalChannelId string // 原始频道 ID（用于响应和推送）
	MessageId         int64  // 消息 ID（用于推送）
}

type EventKeyState struct {
	EventKey        string
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
	keyStates map[string]*EventKeyState
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

// RegisterStreamMessage 注册流式消息元信息（仅创建 session，不创建 event key），
// 用于 /message/send 时缓存元信息，供后续 eventappend 自动填充 from_uid、message_id。
func (c *MessageEventCache) RegisterStreamMessage(meta MessageEventSessionMeta) error {
	meta.ClientMsgNo = strings.TrimSpace(meta.ClientMsgNo)
	if meta.ClientMsgNo == "" {
		return ErrInvalidClientMsgNo
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.sessions[meta.ClientMsgNo]; ok {
		return nil // 已存在，跳过
	}
	if len(c.sessions) >= c.opts.MaxSessions {
		c.evictOneLocked()
	}
	c.sessions[meta.ClientMsgNo] = &messageEventSession{
		meta:      meta,
		keyStates: make(map[string]*EventKeyState),
		createdAt: now,
		updatedAt: now,
	}
	return nil
}

// GetSessionMetaByClientMsgNo 仅通过 client_msg_no 查找 session 元信息（不校验 channel），
// 用于 eventappend 自动填充缺失字段。
func (c *MessageEventCache) GetSessionMetaByClientMsgNo(clientMsgNo string) (*MessageEventSessionMeta, bool) {
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
	meta := s.meta
	return &meta, true
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

// GetEventKeyStates returns all EventKeyState entries for a session.
// Returns nil, false if the session does not exist or channel mismatches.
func (c *MessageEventCache) GetEventKeyStates(clientMsgNo, channelId string, channelType uint8) ([]EventKeyState, bool) {
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
	if len(s.keyStates) == 0 {
		return nil, false
	}

	result := make([]EventKeyState, 0, len(s.keyStates))
	for _, ks := range s.keyStates {
		if ks == nil {
			continue
		}
		result = append(result, *cloneEventKeyState(ks))
	}
	return result, true
}

func (c *MessageEventCache) UpsertSession(meta MessageEventSessionMeta, eventKey string, persistedSeq uint64) (*EventKeyState, error) {
	meta.ClientMsgNo = strings.TrimSpace(meta.ClientMsgNo)
	eventKey = normalizeEventKey(eventKey)
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
			keyStates: make(map[string]*EventKeyState),
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
		if strings.TrimSpace(meta.OriginalChannelId) != "" {
			s.meta.OriginalChannelId = meta.OriginalChannelId
		}
		if meta.MessageId != 0 {
			s.meta.MessageId = meta.MessageId
		}
		s.updatedAt = now
	}

	ks := s.keyStates[eventKey]
	if ks == nil {
		ks = &EventKeyState{
			EventKey:  eventKey,
			Status:    wkdb.EventStatusOpen,
			UpdatedAt: now,
		}
		s.keyStates[eventKey] = ks
	}
	if persistedSeq > ks.PersistedSeq {
		ks.PersistedSeq = persistedSeq
	}
	if ks.Status == "" {
		ks.Status = wkdb.EventStatusOpen
	}
	ks.UpdatedAt = now
	return cloneEventKeyState(ks), nil
}

// AppendDelta handles any stream.delta event via cache. For text kind, it accumulates
// the delta string. For other kinds, it stores the raw payload as the snapshot.
func (c *MessageEventCache) AppendDelta(clientMsgNo, channelId string, channelType uint8, eventKey, eventID, eventType, visibility string, occurredAt int64, payload []byte) (*EventKeyState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	eventKey = normalizeEventKey(eventKey)
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

	ks := s.keyStates[eventKey]
	if ks == nil {
		return nil, ErrMessageEventKeyNotFound
	}
	if isEventKeyTerminal(ks.Status) {
		return nil, ErrStreamClosed
	}

	if textDelta := extractCacheTextDelta(payload); textDelta != "" {
		ks.TextSnapshot += textDelta
	} else if len(payload) > 0 {
		ks.SnapshotPayload = append([]byte(nil), payload...)
	}

	ks.LastEventID = strings.TrimSpace(eventID)
	ks.LastEventType = strings.TrimSpace(eventType)
	ks.LastVisibility = strings.TrimSpace(visibility)
	ks.LastOccurredAt = occurredAt
	ks.Status = wkdb.EventStatusOpen
	ks.UpdatedAt = now
	s.updatedAt = now

	return cloneEventKeyState(ks), nil
}

func (c *MessageEventCache) BuildTerminalPayload(clientMsgNo, channelId string, channelType uint8, eventKey string, payload []byte, eventID, eventType, visibility string, occurredAt int64) ([]byte, *EventKeyState, error) {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	eventKey = normalizeEventKey(eventKey)
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
	ks := s.keyStates[eventKey]
	if ks == nil {
		return payload, nil, ErrMessageEventKeyNotFound
	}

	ks.LastEventID = strings.TrimSpace(eventID)
	ks.LastEventType = strings.TrimSpace(eventType)
	ks.LastVisibility = strings.TrimSpace(visibility)
	ks.LastOccurredAt = occurredAt
	ks.UpdatedAt = now
	s.updatedAt = now

	if ks.TextSnapshot == "" && len(ks.SnapshotPayload) == 0 {
		return payload, cloneEventKeyState(ks), nil
	}
	return mergeTerminalPayloadWithSnapshot(payload, ks.TextSnapshot, ks.SnapshotPayload), cloneEventKeyState(ks), nil
}

func (c *MessageEventCache) MarkEventKeyPersisted(clientMsgNo, channelId string, channelType uint8, eventKey, status string, persistedSeq uint64) error {
	clientMsgNo = strings.TrimSpace(clientMsgNo)
	eventKey = normalizeEventKey(eventKey)
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

	ks := s.keyStates[eventKey]
	if ks == nil {
		ks = &EventKeyState{
			EventKey:  eventKey,
			UpdatedAt: now,
		}
		s.keyStates[eventKey] = ks
	}
	if persistedSeq > ks.PersistedSeq {
		ks.PersistedSeq = persistedSeq
	}
	if strings.TrimSpace(status) != "" {
		ks.Status = strings.TrimSpace(status)
	}
	ks.UpdatedAt = now
	s.updatedAt = now

	if allEventKeysTerminal(s.keyStates) {
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

func normalizeEventKey(eventKey string) string {
	eventKey = strings.TrimSpace(eventKey)
	if eventKey == "" {
		return wkdb.EventKeyDefault
	}
	return eventKey
}

func isEventKeyTerminal(status string) bool {
	switch status {
	case wkdb.EventStatusClosed, wkdb.EventStatusError, wkdb.EventStatusCancelled:
		return true
	default:
		return false
	}
}

func allEventKeysTerminal(keyStates map[string]*EventKeyState) bool {
	if len(keyStates) == 0 {
		return false
	}
	for _, ks := range keyStates {
		if ks == nil {
			continue
		}
		if !isEventKeyTerminal(ks.Status) {
			return false
		}
	}
	return true
}

func cloneEventKeyState(ks *EventKeyState) *EventKeyState {
	if ks == nil {
		return nil
	}
	cp := *ks
	if len(ks.SnapshotPayload) > 0 {
		cp.SnapshotPayload = append([]byte(nil), ks.SnapshotPayload...)
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
			"kind": wkdb.SnapshotKindText,
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
	if kind != wkdb.SnapshotKindText {
		return ""
	}
	delta, _ := m["delta"].(string)
	return delta
}
