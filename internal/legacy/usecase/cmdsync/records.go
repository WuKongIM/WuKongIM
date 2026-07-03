package cmdsync

import (
	"strings"
	"sync"
	"time"
)

const (
	defaultSyncRecordTTL       = 5 * time.Minute
	defaultSyncRecordMaxUIDs   = 4096
	defaultSyncRecordMaxPerUID = 2048
)

// SyncRecord stores the latest command-channel sequence returned by one sync.
type SyncRecord struct {
	// CommandChannelID is the durable command channel id retained for ack writes.
	CommandChannelID string
	// ChannelType is the durable command channel type.
	ChannelType uint8
	// LastReturnedMsgSeq is the highest sequence returned in the latest sync generation.
	LastReturnedMsgSeq uint64
	// Pending marks records that were returned from an owner-local pending overlay.
	Pending bool
	// PendingActiveAt records the pending activity timestamp needed for durable ack upserts.
	PendingActiveAt int64
}

// SyncRecordCacheOptions configures the UID-local latest sync generation cache.
type SyncRecordCacheOptions struct {
	// TTL bounds how long an unacked sync generation can be acknowledged.
	TTL time.Duration
	// MaxUIDs bounds the number of UID generations retained in memory.
	MaxUIDs int
	// MaxRecordsPerUID bounds command-channel records retained per UID generation.
	MaxRecordsPerUID int
	// Now supplies wall-clock time for deterministic tests.
	Now func() time.Time
}

// SyncRecordCache keeps only the latest unacked CMD sync generation per UID.
type SyncRecordCache struct {
	mu               sync.Mutex
	ttl              time.Duration
	maxUIDs          int
	maxRecordsPerUID int
	now              func() time.Time
	entries          map[string]syncRecordEntry
}

type syncRecordEntry struct {
	records   []SyncRecord
	expiresAt time.Time
	touchedAt time.Time
}

// NewSyncRecordCache creates a bounded latest-generation sync record cache.
func NewSyncRecordCache(opts SyncRecordCacheOptions) *SyncRecordCache {
	if opts.TTL <= 0 {
		opts.TTL = defaultSyncRecordTTL
	}
	if opts.MaxUIDs <= 0 {
		opts.MaxUIDs = defaultSyncRecordMaxUIDs
	}
	if opts.MaxRecordsPerUID <= 0 {
		opts.MaxRecordsPerUID = defaultSyncRecordMaxPerUID
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &SyncRecordCache{
		ttl:              opts.TTL,
		maxUIDs:          opts.MaxUIDs,
		maxRecordsPerUID: opts.MaxRecordsPerUID,
		now:              opts.Now,
		entries:          make(map[string]syncRecordEntry),
	}
}

// Replace stores a new latest generation for uid and discards the previous one.
func (c *SyncRecordCache) Replace(uid string, records []SyncRecord) {
	if c == nil {
		return
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return
	}

	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpiredLocked(now)
	if len(records) == 0 {
		delete(c.entries, uid)
		return
	}
	if len(records) > c.maxRecordsPerUID {
		records = records[:c.maxRecordsPerUID]
	}
	c.entries[uid] = syncRecordEntry{
		records:   append([]SyncRecord(nil), records...),
		expiresAt: now.Add(c.ttl),
		touchedAt: now,
	}
	c.evictOverflowLocked()
}

// Pop atomically returns and clears the latest unexpired generation for uid.
func (c *SyncRecordCache) Pop(uid string) []SyncRecord {
	if c == nil {
		return nil
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil
	}

	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[uid]
	if !ok {
		return nil
	}
	if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
		delete(c.entries, uid)
		return nil
	}
	delete(c.entries, uid)
	return append([]SyncRecord(nil), entry.records...)
}

// Peek returns the latest unexpired generation for uid without clearing it.
func (c *SyncRecordCache) Peek(uid string) []SyncRecord {
	if c == nil {
		return nil
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil
	}

	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[uid]
	if !ok {
		return nil
	}
	if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
		delete(c.entries, uid)
		return nil
	}
	return append([]SyncRecord(nil), entry.records...)
}

// DeleteIfUnchanged clears uid only when its latest generation still matches records.
func (c *SyncRecordCache) DeleteIfUnchanged(uid string, records []SyncRecord) {
	if c == nil {
		return
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return
	}

	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[uid]
	if !ok {
		return
	}
	if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
		delete(c.entries, uid)
		return
	}
	if syncRecordsEqual(entry.records, records) {
		delete(c.entries, uid)
	}
}

func (c *SyncRecordCache) pruneExpiredLocked(now time.Time) {
	for uid, entry := range c.entries {
		if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
			delete(c.entries, uid)
		}
	}
}

func (c *SyncRecordCache) evictOverflowLocked() {
	for len(c.entries) > c.maxUIDs {
		var oldestUID string
		var oldestTime time.Time
		first := true
		for uid, entry := range c.entries {
			if first || entry.touchedAt.Before(oldestTime) || (entry.touchedAt.Equal(oldestTime) && uid < oldestUID) {
				oldestUID = uid
				oldestTime = entry.touchedAt
				first = false
			}
		}
		if oldestUID == "" {
			return
		}
		delete(c.entries, oldestUID)
	}
}

func syncRecordsEqual(left, right []SyncRecord) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
