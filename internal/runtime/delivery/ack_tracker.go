package delivery

import (
	"sync"
	"sync/atomic"
	"time"
)

const defaultAckTrackerShardCount = 32

const ackBindBatchStackShards = 64
const ackBindBatchStackEntries = 128
const localAckFinishBatchStackEntries = 512

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
	// Token identifies only this bind reservation and is opaque to callers.
	Token AckBindToken
	// PendingCount is the owner-local pending ack count after the mutation.
	PendingCount int
}

// AckBindToken is an opaque reservation owned by one successful bind attempt.
// A failed delivery may cancel only the token returned by its own bind.
type AckBindToken struct {
	id uint64
}

// Valid reports whether the token owns one successful bind reservation.
func (t AckBindToken) Valid() bool {
	return t.id != 0
}

// AckBindBatchResult describes the item-aligned outcome of binding pending recvacks in one batch.
type AckBindBatchResult struct {
	// Tokens preserves input alignment; a zero token means that item was rejected.
	Tokens []AckBindToken
	// Added is the number of new pending ack entries created by the batch.
	Added int
	// PendingCount is the owner-local pending ack count after the batch mutation.
	PendingCount int
}

// AckTracker tracks delivered messages that still need recipient recvacks.
type AckTracker struct {
	// now returns Unix seconds for pending entries that omit DeliveredAt.
	now func() int64
	// maxPendingPerSession bounds distinct pending message keys per owner session.
	maxPendingPerSession int
	// shards partition owner-session state and its lock contention.
	shards []ackTrackerShard
	// pendingCount tracks distinct pending message keys across all shards.
	pendingCount atomic.Int64
	// nextBindToken allocates non-zero ownership tokens for in-flight binds.
	nextBindToken atomic.Uint64
}

type ackTrackerShard struct {
	// mu protects both indexes in this shard.
	mu sync.Mutex
	// byMessage stores protocol state and bind ownership by exact message identity.
	byMessage map[ackMessageKey]ackTrackerEntry
	// bySession indexes pending message IDs for session cleanup and limits.
	bySession map[ackSessionKey]map[uint64]struct{}
}

// ackTrackerEntry keeps protocol-visible pending state separate from internal
// per-bind rollback ownership. The common one-bind case needs no extra slice.
type ackTrackerEntry struct {
	// pending is the metadata returned when the message is acked or removed.
	pending PendingRecvAck
	// committed reports that at least one delivery attempt completed successfully.
	committed bool
	// primary owns the allocation-free common in-flight bind reservation.
	primary AckBindToken
	// extraAttempts holds overlapping bind reservations for the same message key.
	extraAttempts []ackBindAttempt
}

// ackBindAttempt retains tentative metadata only until that delivery finishes.
type ackBindAttempt struct {
	// token identifies the delivery attempt allowed to finish or cancel this reservation.
	token AckBindToken
	// pending preserves that attempt's delivery metadata until its outcome is known.
	pending PendingRecvAck
}

// AckCancelResult reports one token-scoped failed-delivery rollback.
type AckCancelResult struct {
	// Canceled reports whether the matching bind token was still present.
	Canceled bool
	// Removed reports whether canceling the token removed the last reservation.
	Removed bool
	// PendingCount is the owner-local pending key count after cancellation.
	PendingCount int
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
		tracker.shards[i].byMessage = make(map[ackMessageKey]ackTrackerEntry)
		tracker.shards[i].bySession = make(map[ackSessionKey]map[uint64]struct{})
	}
	return tracker
}

// Bind records a delivered message as an already-successful compatibility bind.
func (t *AckTracker) Bind(pending PendingRecvAck) bool {
	result := t.BindResult(pending)
	if !result.Bound {
		return false
	}
	// Identity cleanup may win between reserve and finish; the compatibility
	// call still reports the bind accepted in that case.
	t.FinishBind(pending, result.Token)
	return true
}

// BindResult reserves one delivery attempt and reports whether it changed the
// pending set. Every successful result must receive exactly one FinishBind or
// CancelBind call unless identity-based ack/session/expiry cleanup wins first.
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
	entry, existed := shard.byMessage[messageKey]
	if t.maxPendingPerSession > 0 && !existed && len(messages) >= t.maxPendingPerSession {
		return AckBindResult{PendingCount: int(t.pendingCount.Load())}
	}
	if messages == nil {
		messages = make(map[uint64]struct{})
		shard.bySession[sessionKey] = messages
	}
	token := t.newBindToken()
	entry.addAttempt(pending, token)
	shard.byMessage[messageKey] = entry
	messages[pending.MessageID] = struct{}{}
	if !existed {
		return AckBindResult{Bound: true, Added: true, Token: token, PendingCount: int(t.pendingCount.Add(1))}
	}
	return AckBindResult{Bound: true, Token: token, PendingCount: int(t.pendingCount.Load())}
}

// BindBatch reserves item-aligned delivery attempts while acquiring each
// affected shard once. Every non-zero token must receive exactly one finish or
// cancel unless identity-based ack/session/expiry cleanup wins first.
func (t *AckTracker) BindBatch(pending []PendingRecvAck) AckBindBatchResult {
	result := AckBindBatchResult{Tokens: make([]AckBindToken, len(pending))}
	if t == nil {
		return result
	}
	if len(pending) == 0 {
		result.PendingCount = t.PendingCount()
		return result
	}

	// Group input indexes by shard without allocating one slice per shard. After
	// the fill pass, shardEnds contains cumulative end offsets for orderedIndexes.
	var shardEndsStack [ackBindBatchStackShards]int
	var shardEnds []int
	if len(t.shards) <= len(shardEndsStack) {
		shardEnds = shardEndsStack[:len(t.shards)]
	} else {
		shardEnds = make([]int, len(t.shards))
	}
	validCount := 0
	for i := range pending {
		if !validPendingRecvAck(pending[i]) {
			continue
		}
		shardEnds[t.shardIndex(pending[i].SessionID)]++
		validCount++
	}
	if validCount == 0 {
		result.PendingCount = t.PendingCount()
		return result
	}
	var orderedIndexesStack [ackBindBatchStackEntries]int
	var orderedIndexes []int
	if validCount <= len(orderedIndexesStack) {
		orderedIndexes = orderedIndexesStack[:validCount]
	} else {
		orderedIndexes = make([]int, validCount)
	}
	offset := 0
	for shardIndex := range shardEnds {
		count := shardEnds[shardIndex]
		shardEnds[shardIndex] = offset
		offset += count
	}
	for i := range pending {
		if !validPendingRecvAck(pending[i]) {
			continue
		}
		shardIndex := t.shardIndex(pending[i].SessionID)
		orderedIndexes[shardEnds[shardIndex]] = i
		shardEnds[shardIndex]++
	}

	start := 0
	for shardIndex, end := range shardEnds {
		if start == end {
			continue
		}
		shard := &t.shards[shardIndex]
		shard.mu.Lock()
		addedInShard := 0
		for _, inputIndex := range orderedIndexes[start:end] {
			item := pending[inputIndex]
			if item.DeliveredAt == 0 {
				item.DeliveredAt = t.now()
			}
			messageKey := ackMessageKey{uid: item.UID, sessionID: item.SessionID, messageID: item.MessageID}
			sessionKey := ackSessionKey{uid: item.UID, sessionID: item.SessionID}
			messages := shard.bySession[sessionKey]
			entry, existed := shard.byMessage[messageKey]
			if t.maxPendingPerSession > 0 && !existed && len(messages) >= t.maxPendingPerSession {
				continue
			}
			if messages == nil {
				messages = make(map[uint64]struct{})
				shard.bySession[sessionKey] = messages
			}
			token := t.newBindToken()
			entry.addAttempt(item, token)
			shard.byMessage[messageKey] = entry
			messages[item.MessageID] = struct{}{}
			result.Tokens[inputIndex] = token
			if !existed {
				addedInShard++
			}
		}
		if addedInShard > 0 {
			result.Added += addedInShard
			t.pendingCount.Add(int64(addedInShard))
		}
		shard.mu.Unlock()
		start = end
	}
	result.PendingCount = t.PendingCount()
	return result
}

// FinishBind marks one successful delivery committed and releases its
// in-flight reservation. A fast client ack may remove the identity first, in
// which case finishing the stale token is a no-op.
func (t *AckTracker) FinishBind(pending PendingRecvAck, token AckBindToken) bool {
	if t == nil || !validPendingRecvAck(pending) || !token.Valid() {
		return false
	}
	shard := t.shard(pending.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	return t.finishBindLocked(shard, pending, token)
}

// FinishBindBatch commits successful item indexes while acquiring each
// affected tracker shard once. Pending rows and tokens remain input-aligned.
func (t *AckTracker) FinishBindBatch(pending []PendingRecvAck, tokens []AckBindToken, indexes []int) int {
	if t == nil || len(indexes) == 0 || len(pending) == 0 || len(tokens) == 0 {
		return 0
	}
	var shardEndsStack [ackBindBatchStackShards]int
	var shardEnds []int
	if len(t.shards) <= len(shardEndsStack) {
		shardEnds = shardEndsStack[:len(t.shards)]
	} else {
		shardEnds = make([]int, len(t.shards))
	}
	validCount := 0
	for _, inputIndex := range indexes {
		if inputIndex < 0 || inputIndex >= len(pending) || inputIndex >= len(tokens) || !validPendingRecvAck(pending[inputIndex]) || !tokens[inputIndex].Valid() {
			continue
		}
		shardEnds[t.shardIndex(pending[inputIndex].SessionID)]++
		validCount++
	}
	if validCount == 0 {
		return 0
	}
	var orderedIndexesStack [localAckFinishBatchStackEntries]int
	var orderedIndexes []int
	if validCount <= len(orderedIndexesStack) {
		orderedIndexes = orderedIndexesStack[:validCount]
	} else {
		orderedIndexes = make([]int, validCount)
	}
	offset := 0
	for shardIndex := range shardEnds {
		count := shardEnds[shardIndex]
		shardEnds[shardIndex] = offset
		offset += count
	}
	for _, inputIndex := range indexes {
		if inputIndex < 0 || inputIndex >= len(pending) || inputIndex >= len(tokens) || !validPendingRecvAck(pending[inputIndex]) || !tokens[inputIndex].Valid() {
			continue
		}
		shardIndex := t.shardIndex(pending[inputIndex].SessionID)
		orderedIndexes[shardEnds[shardIndex]] = inputIndex
		shardEnds[shardIndex]++
	}

	finished := 0
	start := 0
	for shardIndex, end := range shardEnds {
		if start == end {
			continue
		}
		shard := &t.shards[shardIndex]
		shard.mu.Lock()
		for _, inputIndex := range orderedIndexes[start:end] {
			if t.finishBindLocked(shard, pending[inputIndex], tokens[inputIndex]) {
				finished++
			}
		}
		shard.mu.Unlock()
		start = end
	}
	return finished
}

// CancelBind cancels only one failed delivery's bind reservation. The pending
// key remains while any earlier or concurrent bind token is still present.
func (t *AckTracker) CancelBind(pending PendingRecvAck, token AckBindToken) AckCancelResult {
	if t == nil || !validPendingRecvAck(pending) || !token.Valid() {
		if t == nil {
			return AckCancelResult{}
		}
		return AckCancelResult{PendingCount: t.PendingCount()}
	}
	shard := t.shard(pending.SessionID)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	messageKey := ackMessageKey{uid: pending.UID, sessionID: pending.SessionID, messageID: pending.MessageID}
	entry, ok := shard.byMessage[messageKey]
	if !ok || !entry.cancelAttempt(token) {
		return AckCancelResult{PendingCount: int(t.pendingCount.Load())}
	}
	if entry.committed || entry.hasAttempts() {
		shard.byMessage[messageKey] = entry
		return AckCancelResult{Canceled: true, PendingCount: int(t.pendingCount.Load())}
	}
	delete(shard.byMessage, messageKey)
	t.deleteSessionMessageLocked(shard, ackSessionKey{uid: pending.UID, sessionID: pending.SessionID}, pending.MessageID)
	return AckCancelResult{Canceled: true, Removed: true, PendingCount: int(t.pendingCount.Add(-1))}
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
	entry, ok := shard.byMessage[messageKey]
	if !ok {
		return PendingRecvAck{}, false
	}
	delete(shard.byMessage, messageKey)
	t.deleteSessionMessageLocked(shard, ackSessionKey{uid: ack.UID, sessionID: ack.SessionID}, ack.MessageID)
	t.pendingCount.Add(-1)
	return entry.pending, true
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
		if entry, ok := shard.byMessage[messageKey]; ok {
			removed = append(removed, entry.pending)
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
		for messageKey, entry := range shard.byMessage {
			if entry.pending.DeliveredAt > cutoff {
				continue
			}
			removed = append(removed, entry.pending)
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
	return &t.shards[t.shardIndex(sessionID)]
}

func (t *AckTracker) shardIndex(sessionID uint64) int {
	return int(sessionID % uint64(len(t.shards)))
}

func validPendingRecvAck(pending PendingRecvAck) bool {
	return pending.UID != "" && pending.SessionID != 0 && pending.MessageID != 0
}

func (t *AckTracker) newBindToken() AckBindToken {
	for {
		id := t.nextBindToken.Add(1)
		if id != 0 {
			return AckBindToken{id: id}
		}
	}
}

func (t *AckTracker) finishBindLocked(shard *ackTrackerShard, pending PendingRecvAck, token AckBindToken) bool {
	messageKey := ackMessageKey{uid: pending.UID, sessionID: pending.SessionID, messageID: pending.MessageID}
	entry, ok := shard.byMessage[messageKey]
	if !ok || !entry.finishAttempt(token) {
		return false
	}
	shard.byMessage[messageKey] = entry
	return true
}

func (e *ackTrackerEntry) addAttempt(pending PendingRecvAck, token AckBindToken) {
	if !e.committed && !e.primary.Valid() {
		e.pending = pending
		e.primary = token
		return
	}
	e.extraAttempts = append(e.extraAttempts, ackBindAttempt{token: token, pending: pending})
}

func (e *ackTrackerEntry) finishAttempt(token AckBindToken) bool {
	if e.primary == token {
		e.primary = AckBindToken{}
		e.committed = true
		return true
	}
	for i := range e.extraAttempts {
		if e.extraAttempts[i].token != token {
			continue
		}
		finished := e.extraAttempts[i]
		if !e.committed && e.primary.Valid() {
			// An overlapping attempt finished before the fresh primary attempt.
			// Reuse the finishing attempt's slot to retain the primary candidate;
			// this keeps last-successful-finish metadata semantics without adding
			// another allocation to the already-concurrent path.
			e.extraAttempts[i] = ackBindAttempt{token: e.primary, pending: e.pending}
			e.primary = AckBindToken{}
		} else {
			e.removeExtraAttempt(i)
		}
		e.pending = finished.pending
		e.committed = true
		return true
	}
	return false
}

func (e *ackTrackerEntry) cancelAttempt(token AckBindToken) bool {
	if e.primary == token {
		e.primary = AckBindToken{}
		if !e.committed && len(e.extraAttempts) > 0 {
			last := len(e.extraAttempts) - 1
			promoted := e.extraAttempts[last]
			e.pending = promoted.pending
			e.primary = promoted.token
			e.removeExtraAttempt(last)
		}
		return true
	}
	for i := range e.extraAttempts {
		if e.extraAttempts[i].token != token {
			continue
		}
		e.removeExtraAttempt(i)
		return true
	}
	return false
}

func (e *ackTrackerEntry) removeExtraAttempt(index int) {
	last := len(e.extraAttempts) - 1
	if index != last {
		e.extraAttempts[index] = e.extraAttempts[last]
	}
	e.extraAttempts[last] = ackBindAttempt{}
	if last == 0 {
		e.extraAttempts = nil
	} else {
		e.extraAttempts = e.extraAttempts[:last]
	}
}

func (e ackTrackerEntry) hasAttempts() bool {
	return e.primary.Valid() || len(e.extraAttempts) > 0
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
