package delivery

import (
	"sync"
	"sync/atomic"
	"time"
)

const defaultAckTrackerShardCount = 32

// AckTrackerOptions configures an owner-local recvack tracker.
type AckTrackerOptions struct {
	// ShardCount controls the number of session ID shards; values <= 0 use the default.
	ShardCount int
	// Now returns the current Unix second; nil uses time.Now().Unix().
	Now func() int64
	// MaxPendingPerSession limits pending recvacks per UID/session; values <= 0 keep it unlimited.
	MaxPendingPerSession int
}

// AckBindResult describes the outcome of binding one pending recvack.
type AckBindResult struct {
	// Bound reports whether the pending ack was stored.
	Bound bool
	// Added reports whether this bind created a new pending ack entry.
	Added bool
	// PendingCount is the owner-local pending ack count after the mutation.
	PendingCount int
}

// AckTracker tracks delivered messages that still need recipient recvacks.
type AckTracker struct {
	now                  func() int64
	maxPendingPerSession int
	shards               []ackTrackerShard
	pendingCount         atomic.Int64
}

type ackTrackerShard struct {
	mu        sync.Mutex
	byMessage map[ackMessageKey]PendingRecvAck
	bySession map[ackSessionKey]map[uint64]struct{}
}

type ackMessageKey struct {
	uid       string
	sessionID uint64
	messageID uint64
}

type ackSessionKey struct {
	uid       string
	sessionID uint64
}

// NewAckTracker creates a sharded owner-local recvack tracker.
func NewAckTracker(opts AckTrackerOptions) *AckTracker {
	shardCount := opts.ShardCount
	if shardCount <= 0 {
		shardCount = defaultAckTrackerShardCount
	}
	now := opts.Now
	if now == nil {
		now = func() int64 {
			return time.Now().Unix()
		}
	}
	tracker := &AckTracker{
		now:                  now,
		maxPendingPerSession: opts.MaxPendingPerSession,
		shards:               make([]ackTrackerShard, shardCount),
	}
	for i := range tracker.shards {
		tracker.shards[i].byMessage = make(map[ackMessageKey]PendingRecvAck)
		tracker.shards[i].bySession = make(map[ackSessionKey]map[uint64]struct{})
	}
	return tracker
}

// Bind records a delivered message that is waiting for a recipient recvack.
func (t *AckTracker) Bind(pending PendingRecvAck) bool {
	return t.BindResult(pending).Bound
}

// BindResult records a delivered message and reports whether it changed the pending set.
func (t *AckTracker) BindResult(pending PendingRecvAck) AckBindResult {
	if t == nil || pending.UID == "" || pending.SessionID == 0 || pending.MessageID == 0 {
		if t == nil {
			return AckBindResult{}
		}
		return AckBindResult{PendingCount: t.PendingCount()}
	}
	if pending.DeliveredAt == 0 {
		pending.DeliveredAt = t.now()
	}
	shard := t.shard(pending.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	messageKey := ackMessageKey{uid: pending.UID, sessionID: pending.SessionID, messageID: pending.MessageID}
	sessionKey := ackSessionKey{uid: pending.UID, sessionID: pending.SessionID}
	messages := shard.bySession[sessionKey]
	_, existed := shard.byMessage[messageKey]
	if t.maxPendingPerSession > 0 && !existed && len(messages) >= t.maxPendingPerSession {
		return AckBindResult{PendingCount: int(t.pendingCount.Load())}
	}
	if messages == nil {
		messages = make(map[uint64]struct{})
		shard.bySession[sessionKey] = messages
	}
	shard.byMessage[messageKey] = pending
	messages[pending.MessageID] = struct{}{}
	if !existed {
		return AckBindResult{Bound: true, Added: true, PendingCount: int(t.pendingCount.Add(1))}
	}
	return AckBindResult{Bound: true, PendingCount: int(t.pendingCount.Load())}
}

// Ack clears and returns a pending recvack matched by UID, session ID, and message ID.
func (t *AckTracker) Ack(ack Recvack) (PendingRecvAck, bool) {
	if t == nil || ack.UID == "" || ack.SessionID == 0 || ack.MessageID == 0 {
		return PendingRecvAck{}, false
	}
	shard := t.shard(ack.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	messageKey := ackMessageKey{uid: ack.UID, sessionID: ack.SessionID, messageID: ack.MessageID}
	pending, ok := shard.byMessage[messageKey]
	if !ok {
		return PendingRecvAck{}, false
	}
	delete(shard.byMessage, messageKey)
	t.deleteSessionMessageLocked(shard, ackSessionKey{uid: ack.UID, sessionID: ack.SessionID}, ack.MessageID)
	t.pendingCount.Add(-1)
	return pending, true
}

// SessionClosed removes all pending recvacks for one recipient-owner session.
func (t *AckTracker) SessionClosed(uid string, sessionID uint64) []PendingRecvAck {
	if t == nil || uid == "" || sessionID == 0 {
		return nil
	}
	shard := t.shard(sessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	sessionKey := ackSessionKey{uid: uid, sessionID: sessionID}
	messageIDs := shard.bySession[sessionKey]
	if len(messageIDs) == 0 {
		return nil
	}
	removed := make([]PendingRecvAck, 0, len(messageIDs))
	for messageID := range messageIDs {
		messageKey := ackMessageKey{uid: uid, sessionID: sessionID, messageID: messageID}
		if pending, ok := shard.byMessage[messageKey]; ok {
			removed = append(removed, pending)
			delete(shard.byMessage, messageKey)
		}
	}
	delete(shard.bySession, sessionKey)
	if len(removed) > 0 {
		t.pendingCount.Add(-int64(len(removed)))
	}
	return removed
}

// Expire removes pending recvacks whose DeliveredAt is older than the ttl cutoff.
func (t *AckTracker) Expire(ttl time.Duration) []PendingRecvAck {
	if t == nil || ttl <= 0 {
		return nil
	}
	ttlSeconds := int64((ttl + time.Second - 1) / time.Second)
	cutoff := t.now() - ttlSeconds
	var removed []PendingRecvAck
	for i := range t.shards {
		shard := &t.shards[i]
		shard.mu.Lock()
		removedInShard := 0
		for messageKey, pending := range shard.byMessage {
			if pending.DeliveredAt > cutoff {
				continue
			}
			removed = append(removed, pending)
			delete(shard.byMessage, messageKey)
			t.deleteSessionMessageLocked(shard, ackSessionKey{uid: messageKey.uid, sessionID: messageKey.sessionID}, messageKey.messageID)
			removedInShard++
		}
		if removedInShard > 0 {
			t.pendingCount.Add(-int64(removedInShard))
		}
		shard.mu.Unlock()
	}
	return removed
}

// PendingCount returns the total number of pending recvacks across all shards.
func (t *AckTracker) PendingCount() int {
	if t == nil {
		return 0
	}
	return int(t.pendingCount.Load())
}

func (t *AckTracker) shard(sessionID uint64) *ackTrackerShard {
	return &t.shards[int(sessionID%uint64(len(t.shards)))]
}

func (t *AckTracker) deleteSessionMessageLocked(shard *ackTrackerShard, key ackSessionKey, messageID uint64) {
	messages := shard.bySession[key]
	if messages == nil {
		return
	}
	delete(messages, messageID)
	if len(messages) == 0 {
		delete(shard.bySession, key)
	}
}
