package conversation

import (
	"context"
	"sort"
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultActiveHintFlushInterval  = 5 * time.Second
	defaultActiveHintTTL            = time.Minute
	defaultActiveHintBarrierTTL     = time.Minute
	defaultActiveHintMaxHints       = 10000
	defaultActiveHintMaxHintsPerUID = 1000
	defaultActiveHintFlushBatchSize = 256
)

// ActiveHintStore persists recent user conversation activity hints.
type ActiveHintStore interface {
	TouchUserConversationActiveAt(ctx context.Context, patches []metadb.UserConversationActivePatch) error
}

// ActiveHintCacheOptions configures the in-memory UID-owner active hint cache.
type ActiveHintCacheOptions struct {
	// Store receives flushed active hint patches. Nil makes Flush a no-op.
	Store ActiveHintStore
	// FlushInterval controls the background flush cadence started by Start.
	FlushInterval time.Duration
	// HintTTL is how long an unflushed hint remains visible in the hot overlay.
	HintTTL time.Duration
	// BarrierTTL is how long delete barriers reject stale hints in memory.
	BarrierTTL time.Duration
	// MaxHints limits the total number of hints retained across all users.
	MaxHints int
	// MaxHintsPerUID limits the number of hints retained for a single UID.
	MaxHintsPerUID int
	// FlushBatchSize controls how many patches are sent to the store per call.
	FlushBatchSize int
	// Now supplies wall-clock time for TTL and deterministic tests.
	Now func() time.Time
	// Logger records background flush errors.
	Logger wklog.Logger
}

// ActiveHintCache stores best-effort recent conversation hints owned by UID.
type ActiveHintCache struct {
	store          ActiveHintStore
	flushInterval  time.Duration
	hintTTL        time.Duration
	barrierTTL     time.Duration
	maxHints       int
	maxHintsPerUID int
	flushBatchSize int
	now            func() time.Time
	logger         wklog.Logger

	mu       sync.Mutex
	hints    map[activeHintKey]activeHintEntry
	barriers map[activeHintKey]deleteBarrierEntry
	running  bool
	stopCh   chan struct{}
	doneCh   chan struct{}
	cancel   context.CancelFunc
}

type activeHintKey struct {
	uid         string
	channelID   string
	channelType int64
}

type activeHintEntry struct {
	hint      metadb.UserConversationActiveHint
	touchedAt time.Time
}

type deleteBarrierEntry struct {
	barrier   metadb.UserConversationDeleteBarrier
	expiresAt time.Time
}

// NewActiveHintCache creates an in-memory active hint cache with safe defaults.
func NewActiveHintCache(opts ActiveHintCacheOptions) *ActiveHintCache {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultActiveHintFlushInterval
	}
	if opts.HintTTL <= 0 {
		opts.HintTTL = defaultActiveHintTTL
	}
	if opts.BarrierTTL <= 0 {
		opts.BarrierTTL = defaultActiveHintBarrierTTL
	}
	if opts.MaxHints <= 0 {
		opts.MaxHints = defaultActiveHintMaxHints
	}
	if opts.MaxHintsPerUID <= 0 {
		opts.MaxHintsPerUID = defaultActiveHintMaxHintsPerUID
	}
	if opts.FlushBatchSize <= 0 {
		opts.FlushBatchSize = defaultActiveHintFlushBatchSize
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}

	return &ActiveHintCache{
		store:          opts.Store,
		flushInterval:  opts.FlushInterval,
		hintTTL:        opts.HintTTL,
		barrierTTL:     opts.BarrierTTL,
		maxHints:       opts.MaxHints,
		maxHintsPerUID: opts.MaxHintsPerUID,
		flushBatchSize: opts.FlushBatchSize,
		now:            opts.Now,
		logger:         opts.Logger,
		hints:          make(map[activeHintKey]activeHintEntry),
		barriers:       make(map[activeHintKey]deleteBarrierEntry),
	}
}

// Start begins periodic flushing. Calling Start more than once is a no-op.
func (c *ActiveHintCache) Start() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	runCtx, cancel := context.WithCancel(context.Background())
	c.stopCh = stopCh
	c.doneCh = doneCh
	c.cancel = cancel
	c.running = true
	interval := c.flushInterval
	c.mu.Unlock()

	go c.run(runCtx, stopCh, doneCh, interval)
	return nil
}

// Stop stops periodic flushing and performs one final Flush.
func (c *ActiveHintCache) Stop() error {
	return c.StopContext(context.Background())
}

// StopContext stops periodic flushing and bounds the final best-effort Flush with ctx.
func (c *ActiveHintCache) StopContext(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return c.Flush(ctx)
	}
	stopCh := c.stopCh
	doneCh := c.doneCh
	cancel := c.cancel
	c.running = false
	c.stopCh = nil
	c.doneCh = nil
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	close(stopCh)
	select {
	case <-doneCh:
		return c.Flush(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *ActiveHintCache) run(ctx context.Context, stopCh <-chan struct{}, doneCh chan<- struct{}, interval time.Duration) {
	defer close(doneCh)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Flush(ctx); err != nil {
				c.logger.Warn("active hint cache flush failed", wklog.Error(err))
			}
		case <-stopCh:
			return
		}
	}
}

// SubmitHints records active hints unless an in-memory delete barrier blocks them.
func (c *ActiveHintCache) SubmitHints(_ context.Context, hints []metadb.UserConversationActiveHint) error {
	if c == nil || len(hints) == 0 {
		return nil
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)

	for _, hint := range hints {
		key := keyFromHint(hint)
		if barrier, ok := c.barriers[key]; ok && hint.MessageSeq <= barrier.barrier.DeletedToSeq {
			continue
		}
		current, ok := c.hints[key]
		if ok && !hintNewer(hint, current.hint) {
			continue
		}
		c.hints[key] = activeHintEntry{hint: hint, touchedAt: now}
	}
	c.enforceCapacityLocked()
	return nil
}

// RemoveHints deletes pending hints and installs barriers for stale future hints.
func (c *ActiveHintCache) RemoveHints(_ context.Context, barriers []metadb.UserConversationDeleteBarrier) error {
	if c == nil || len(barriers) == 0 {
		return nil
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)

	for _, barrier := range barriers {
		key := keyFromBarrier(barrier)
		entry, ok := c.barriers[key]
		effectiveDeletedToSeq := barrier.DeletedToSeq
		if ok && entry.barrier.DeletedToSeq > effectiveDeletedToSeq {
			effectiveDeletedToSeq = entry.barrier.DeletedToSeq
		}
		if pending, ok := c.hints[key]; ok && hintBlockedByDeleteBarrier(pending.hint, effectiveDeletedToSeq) {
			delete(c.hints, key)
		}
		if !ok || barrier.DeletedToSeq >= entry.barrier.DeletedToSeq {
			c.barriers[key] = deleteBarrierEntry{barrier: barrier, expiresAt: now.Add(c.barrierTTL)}
		}
	}
	return nil
}

// ListHotUserConversationActive returns hot hints for a UID in active_at order.
func (c *ActiveHintCache) ListHotUserConversationActive(_ context.Context, uid string, limit int) ([]metadb.UserConversationActiveHint, error) {
	if c == nil || uid == "" || limit == 0 {
		return nil, nil
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)

	items := make([]metadb.UserConversationActiveHint, 0)
	for key, entry := range c.hints {
		if key.uid != uid {
			continue
		}
		if barrier, ok := c.barriers[key]; ok && entry.hint.MessageSeq <= barrier.barrier.DeletedToSeq {
			continue
		}
		items = append(items, entry.hint)
	}
	sortActiveHints(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// Flush persists hot hints in batches and removes only entries still matching the snapshot.
func (c *ActiveHintCache) Flush(ctx context.Context) error {
	if c == nil || c.store == nil {
		return nil
	}
	snapshot := c.snapshotHotHints()
	if len(snapshot) == 0 {
		return nil
	}

	for start := 0; start < len(snapshot); start += c.flushBatchSize {
		end := start + c.flushBatchSize
		if end > len(snapshot) {
			end = len(snapshot)
		}
		patches := make([]metadb.UserConversationActivePatch, 0, end-start)
		for _, entry := range snapshot[start:end] {
			patches = append(patches, patchFromHint(entry.hint))
		}
		if err := c.store.TouchUserConversationActiveAt(ctx, patches); err != nil {
			return err
		}
		c.removeFlushedEntries(snapshot[start:end])
	}

	return nil
}

func (c *ActiveHintCache) removeFlushedEntries(entries []activeHintEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range entries {
		key := keyFromHint(entry.hint)
		current, ok := c.hints[key]
		if ok && sameHint(current.hint, entry.hint) && current.touchedAt.Equal(entry.touchedAt) {
			delete(c.hints, key)
		}
	}
}

func (c *ActiveHintCache) snapshotHotHints() []activeHintEntry {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)

	entries := make([]activeHintEntry, 0, len(c.hints))
	for key, entry := range c.hints {
		if barrier, ok := c.barriers[key]; ok && entry.hint.MessageSeq <= barrier.barrier.DeletedToSeq {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return compareHints(entries[i].hint, entries[j].hint)
	})
	return entries
}

func (c *ActiveHintCache) pruneLocked(now time.Time) {
	for key, entry := range c.hints {
		if !entry.touchedAt.Add(c.hintTTL).After(now) {
			delete(c.hints, key)
		}
	}
	for key, entry := range c.barriers {
		if !entry.expiresAt.After(now) {
			delete(c.barriers, key)
		}
	}
}

func (c *ActiveHintCache) enforceCapacityLocked() {
	for len(c.hints) > c.maxHints {
		delete(c.hints, lowestActiveHintKey(c.hints, ""))
	}
	counts := make(map[string]int)
	for key := range c.hints {
		counts[key.uid]++
	}
	for uid, count := range counts {
		for count > c.maxHintsPerUID {
			delete(c.hints, lowestActiveHintKey(c.hints, uid))
			count--
		}
	}
}

func lowestActiveHintKey(hints map[activeHintKey]activeHintEntry, uid string) activeHintKey {
	var lowest activeHintKey
	set := false
	for key, entry := range hints {
		if uid != "" && key.uid != uid {
			continue
		}
		if !set || compareHints(hints[lowest].hint, entry.hint) {
			lowest = key
			set = true
		}
	}
	return lowest
}

func hintNewer(next, current metadb.UserConversationActiveHint) bool {
	if next.MessageSeq != current.MessageSeq {
		return next.MessageSeq > current.MessageSeq
	}
	return next.ActiveAt > current.ActiveAt
}

func hintBlockedByDeleteBarrier(hint metadb.UserConversationActiveHint, deletedToSeq uint64) bool {
	return hint.MessageSeq == 0 || hint.MessageSeq <= deletedToSeq
}

func sameHint(a, b metadb.UserConversationActiveHint) bool {
	return a.UID == b.UID && a.ChannelID == b.ChannelID && a.ChannelType == b.ChannelType && a.ActiveAt == b.ActiveAt && a.MessageSeq == b.MessageSeq
}

func keyFromHint(hint metadb.UserConversationActiveHint) activeHintKey {
	return activeHintKey{uid: hint.UID, channelID: hint.ChannelID, channelType: hint.ChannelType}
}

func keyFromBarrier(barrier metadb.UserConversationDeleteBarrier) activeHintKey {
	return activeHintKey{uid: barrier.UID, channelID: barrier.ChannelID, channelType: barrier.ChannelType}
}

func patchFromHint(hint metadb.UserConversationActiveHint) metadb.UserConversationActivePatch {
	return metadb.UserConversationActivePatch(hint)
}

func sortActiveHints(hints []metadb.UserConversationActiveHint) {
	sort.Slice(hints, func(i, j int) bool {
		return compareHints(hints[i], hints[j])
	})
}

func compareHints(a, b metadb.UserConversationActiveHint) bool {
	if a.ActiveAt != b.ActiveAt {
		return a.ActiveAt > b.ActiveAt
	}
	if a.ChannelType != b.ChannelType {
		return a.ChannelType < b.ChannelType
	}
	if a.ChannelID != b.ChannelID {
		return a.ChannelID < b.ChannelID
	}
	if a.UID != b.UID {
		return a.UID < b.UID
	}
	return a.MessageSeq > b.MessageSeq
}
