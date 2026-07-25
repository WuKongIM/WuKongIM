package backup

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	maxMessageCursorChainSegments         = 1024
	maxMessageCursorChainChannels         = 1 << 20
	maxMessageCursorGenerationSize        = 128
	maxMessageCursorCacheEntries          = 256
	maxMessageCursorCacheBytes            = 64 << 20
	messageCursorCacheEntryBytes          = 512
	messageCursorCacheBoundaryBytes       = 64
	maxBaselineCacheEntries               = 16
	maxBaselineCacheBytes           int64 = 512 << 20
	baselineBoundaryBytes           int64 = 128
	minChannelIndexEntryBytes       int64 = 28
	channelIndexEnvelopeBytes       int64 = 16
)

// ContinuousSegmentLoader authenticates and opens one dual-repository segment.
type ContinuousSegmentLoader interface {
	// Load returns verified portable plaintext for reference.
	Load(context.Context, backupartifact.SegmentReference) ([]byte, error)
}

// MessageCursorResolveRequest fences reconstruction to one exact message stream tip.
type MessageCursorResolveRequest struct {
	// Head is the committed immutable message segment at Sequence.
	Head backupartifact.SegmentReference
	// HashSlot and Generation identify the expected segment graph.
	HashSlot   uint16
	Generation string
	// Sequence is the expected tip sequence.
	Sequence uint64
	// SourceCursor must equal the tip batch's next cursor.
	SourceCursor string
	// SourceHighWatermark must equal the exact position represented by the tip.
	SourceHighWatermark uint64
}

// MessageCursorResolver reconstructs current Channel boundaries from small
// immutable cursor sidecars, never from Controller state or message payload.
type MessageCursorResolver struct {
	loader ContinuousSegmentLoader
	budget runtimebackup.CaptureMemoryBudget
	mu     sync.Mutex
	cache  map[uint16]messageCursorCacheEntry
	// cacheBytes and cacheUse bound and order the rebuild acceleration cache.
	cacheBytes int64
	cacheUse   uint64
	// baselineCache retains immutable decoded materialized roots by their full
	// authenticated reference. Its memory remains charged to the shared budget.
	baselineCache      map[baselineCacheKey]*baselineCacheEntry
	baselineCacheBytes int64
	baselineCacheUse   uint64
}

type messageCursorCacheEntry struct {
	reference     backupartifact.SegmentReference
	generation    string
	sequence      uint64
	sourceCursor  string
	highWatermark uint64
	boundaries    []backupartifact.ChannelBoundary
	bytes         int64
	lastUsed      uint64
}

type baselineCacheKey struct {
	hashSlot  uint16
	reference backupartifact.SegmentReference
}

type baselineCacheEntry struct {
	boundaries []backupartifact.ChannelBoundary
	bytes      int64
	lastUsed   uint64
	refs       int
}

// NewMessageCursorResolver creates a repository-backed cursor resolver.
func NewMessageCursorResolver(
	loader ContinuousSegmentLoader,
	budget runtimebackup.CaptureMemoryBudget,
) (*MessageCursorResolver, error) {
	if loader == nil || budget == nil {
		return nil, fmt.Errorf("backup cursor resolver: segment loader and shared memory budget are required")
	}
	return &MessageCursorResolver{
		loader: loader, budget: budget,
		cache:         make(map[uint16]messageCursorCacheEntry),
		baselineCache: make(map[baselineCacheKey]*baselineCacheEntry),
	}, nil
}

// Resolve authenticates the complete newest-to-oldest chain and returns one
// sorted latest boundary per Channel.
func (r *MessageCursorResolver) Resolve(ctx context.Context, request MessageCursorResolveRequest) ([]backupartifact.ChannelBoundary, error) {
	if r == nil || r.loader == nil || request.Sequence == 0 ||
		strings.TrimSpace(request.Generation) == "" ||
		len(request.Generation) > maxMessageCursorGenerationSize {
		return nil, fmt.Errorf("backup cursor resolver: invalid request")
	}
	type channelIdentity struct {
		channelType uint8
		channelID   string
	}
	cached, cachedOK := r.cached(request.HashSlot)
	if cachedOK && cached.reference == request.Head && cached.generation == request.Generation &&
		cached.sequence == request.Sequence && cached.sourceCursor == request.SourceCursor &&
		cached.highWatermark == request.SourceHighWatermark {
		// Cache entries are immutable after publication. Callers receive a
		// shared read-only view so repeated Channel cuts do not copy the full
		// Hash-Slot index.
		return cached.boundaries, nil
	}
	latest := make(map[channelIdentity]backupartifact.ChannelBoundary)
	reference := request.Head
	expectedSequence := request.Sequence
	expectedNextCursor := request.SourceCursor
	var newerHighWatermark uint64
	var tipHighWatermark uint64
	for segments := 0; ; segments++ {
		if segments >= maxMessageCursorChainSegments {
			return nil, fmt.Errorf("backup cursor resolver: segment chain exceeds limit")
		}
		if cachedOK && cached.reference == reference && cached.generation == request.Generation &&
			cached.sequence == expectedSequence && cached.sourceCursor == expectedNextCursor {
			if newerHighWatermark != 0 && cached.highWatermark > newerHighWatermark {
				return nil, fmt.Errorf("backup cursor resolver: cached message watermark regressed")
			}
			for _, boundary := range cached.boundaries {
				identity := channelIdentity{channelType: boundary.ChannelType, channelID: boundary.ChannelID}
				if _, found := latest[identity]; !found {
					latest[identity] = boundary
				}
			}
			break
		}
		body, err := r.loader.Load(ctx, reference)
		if err != nil {
			return nil, err
		}
		batch, err := backupartifact.LoadMessageCursorBatch(body)
		if err != nil {
			return nil, err
		}
		if batch.HashSlot != request.HashSlot ||
			batch.Generation != request.Generation ||
			batch.Sequence != expectedSequence ||
			batch.NextCursor != expectedNextCursor ||
			(segments == 0 && batch.SourceHighWatermark != request.SourceHighWatermark) ||
			(newerHighWatermark != 0 && batch.SourceHighWatermark > newerHighWatermark) {
			return nil, fmt.Errorf("backup cursor resolver: broken message segment chain")
		}
		if segments == 0 {
			tipHighWatermark = batch.SourceHighWatermark
		}
		for _, boundary := range batch.Boundaries {
			identity := channelIdentity{channelType: boundary.ChannelType, channelID: boundary.ChannelID}
			if _, found := latest[identity]; found {
				continue
			}
			if len(latest) >= maxMessageCursorChainChannels {
				return nil, fmt.Errorf("backup cursor resolver: Channel cursor count exceeds limit")
			}
			latest[identity] = boundary
		}
		if batch.Checkpoint {
			if batch.Previous != nil {
				return nil, fmt.Errorf("backup cursor resolver: checkpoint has predecessor")
			}
			break
		}
		if expectedSequence == 1 {
			if batch.Previous != nil {
				return nil, fmt.Errorf("backup cursor resolver: first message segment has predecessor")
			}
			break
		}
		if batch.Previous == nil {
			return nil, fmt.Errorf("backup cursor resolver: message segment predecessor is missing")
		}
		reference = *batch.Previous
		expectedSequence--
		expectedNextCursor = batch.FromCursor
		newerHighWatermark = batch.SourceHighWatermark
	}
	out := make([]backupartifact.ChannelBoundary, 0, len(latest))
	for _, boundary := range latest {
		out = append(out, boundary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChannelType != out[j].ChannelType {
			return out[i].ChannelType < out[j].ChannelType
		}
		return out[i].ChannelID < out[j].ChannelID
	})
	r.storeCached(request, tipHighWatermark, out)
	return out, nil
}

// ResolvedBaseline owns a shared-budget reservation until its caller finishes
// indexing and merging the complete materialized Channel boundary set.
type ResolvedBaseline struct {
	Boundaries []backupartifact.ChannelBoundary
	budget     runtimebackup.CaptureMemoryBudget
	held       int64
	release    func()
	once       sync.Once
}

// Release returns the complete baseline working-set reservation once.
func (r *ResolvedBaseline) Release() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.budget != nil && r.held > 0 {
			r.budget.Release(r.held)
		}
		r.held = 0
		if r.release != nil {
			r.release()
		}
		r.Boundaries = nil
	})
}

// ResolveBaseline loads one complete materialized Channel index while holding
// its decode/output memory against the shared node capture budget.
func (r *MessageCursorResolver) ResolveBaseline(
	ctx context.Context,
	hashSlot uint16,
	reference backupartifact.SegmentReference,
) (*ResolvedBaseline, error) {
	if r == nil || r.loader == nil || r.budget == nil ||
		reference.PlaintextBytes <= 0 ||
		reference.PlaintextBytes > runtimebackup.MaxCaptureSegmentBytes {
		return nil, fmt.Errorf("backup cursor resolver: invalid baseline request")
	}
	key := baselineCacheKey{hashSlot: hashSlot, reference: reference}
	if cached := r.acquireBaselineCache(key); cached != nil {
		return &ResolvedBaseline{
			Boundaries: cached.boundaries,
			budget:     r.budget,
			release:    func() { r.releaseBaselineCache(key, cached) },
		}, nil
	}
	if reference.PlaintextBytes > math.MaxInt64/2 ||
		reference.PlaintextBytes < channelIndexEnvelopeBytes {
		return nil, runtimebackup.ErrInvalidCapture
	}
	maxCount := (reference.PlaintextBytes - channelIndexEnvelopeBytes) /
		minChannelIndexEntryBytes
	if maxCount > maxMessageCursorChainChannels {
		maxCount = maxMessageCursorChainChannels
	}
	if maxCount > math.MaxInt64/baselineBoundaryBytes {
		return nil, runtimebackup.ErrInvalidCapture
	}
	maxBoundaryBytes := maxCount * baselineBoundaryBytes
	if reference.PlaintextBytes*2 > math.MaxInt64-maxBoundaryBytes {
		return nil, runtimebackup.ErrInvalidCapture
	}
	held := reference.PlaintextBytes*2 + maxBoundaryBytes
	if !r.budget.TryAcquire(held) {
		r.evictIdleBaselineCache()
		if !r.budget.TryAcquire(held) {
			return nil, runtimebackup.ErrCaptureMemoryPressure
		}
	}
	releaseOnError := func(err error) (*ResolvedBaseline, error) {
		r.budget.Release(held)
		return nil, err
	}
	body, err := r.loader.Load(ctx, reference)
	if err != nil {
		return releaseOnError(err)
	}
	if int64(len(body)) != reference.PlaintextBytes {
		return releaseOnError(fmt.Errorf("backup cursor resolver: baseline size evidence mismatch"))
	}
	indexSlot, count, err := backupartifact.InspectChannelIndex(body)
	if err != nil {
		return releaseOnError(err)
	}
	if indexSlot != hashSlot || int64(count) > math.MaxInt64/baselineBoundaryBytes {
		return releaseOnError(fmt.Errorf("backup cursor resolver: baseline Hash Slot mismatch"))
	}
	boundaryBytes := int64(count) * baselineBoundaryBytes
	actualHeld := reference.PlaintextBytes*2 + boundaryBytes
	if actualHeld < held {
		r.budget.Release(held - actualHeld)
		held = actualHeld
	}
	slot, boundaries, err := backupartifact.LoadChannelIndex(body)
	if err != nil {
		return releaseOnError(err)
	}
	if slot != hashSlot {
		return releaseOnError(fmt.Errorf("backup cursor resolver: baseline Hash Slot mismatch"))
	}
	cacheBytes := reference.PlaintextBytes + boundaryBytes
	entry, inserted, cached := r.storeBaselineCache(key, boundaries, cacheBytes)
	if cached {
		if inserted {
			r.budget.Release(held - cacheBytes)
		} else {
			r.budget.Release(held)
		}
		return &ResolvedBaseline{
			Boundaries: entry.boundaries,
			budget:     r.budget,
			release:    func() { r.releaseBaselineCache(key, entry) },
		}, nil
	}
	r.budget.Release(held - cacheBytes)
	return &ResolvedBaseline{
		Boundaries: boundaries,
		budget:     r.budget,
		held:       cacheBytes,
	}, nil
}

func (r *MessageCursorResolver) evictIdleBaselineCache() {
	var released int64
	r.mu.Lock()
	for key, entry := range r.baselineCache {
		if entry.refs != 0 {
			continue
		}
		delete(r.baselineCache, key)
		r.baselineCacheBytes -= entry.bytes
		released += entry.bytes
	}
	r.mu.Unlock()
	if released > 0 {
		r.budget.Release(released)
	}
}

func (r *MessageCursorResolver) acquireBaselineCache(
	key baselineCacheKey,
) *baselineCacheEntry {
	r.mu.Lock()
	entry := r.baselineCache[key]
	if entry != nil {
		r.baselineCacheUse++
		entry.lastUsed = r.baselineCacheUse
		entry.refs++
	}
	r.mu.Unlock()
	return entry
}

func (r *MessageCursorResolver) releaseBaselineCache(
	key baselineCacheKey,
	entry *baselineCacheEntry,
) {
	r.mu.Lock()
	if current := r.baselineCache[key]; current == entry && entry.refs > 0 {
		entry.refs--
	}
	r.mu.Unlock()
}

func (r *MessageCursorResolver) storeBaselineCache(
	key baselineCacheKey,
	boundaries []backupartifact.ChannelBoundary,
	bytes int64,
) (*baselineCacheEntry, bool, bool) {
	if bytes <= 0 || bytes > maxBaselineCacheBytes {
		return nil, false, false
	}
	var released int64
	r.mu.Lock()
	if current := r.baselineCache[key]; current != nil {
		r.baselineCacheUse++
		current.lastUsed = r.baselineCacheUse
		current.refs++
		r.mu.Unlock()
		return current, false, true
	}
	for len(r.baselineCache) >= maxBaselineCacheEntries ||
		r.baselineCacheBytes > maxBaselineCacheBytes-bytes {
		var oldestKey baselineCacheKey
		var oldest *baselineCacheEntry
		for candidateKey, candidate := range r.baselineCache {
			if candidate.refs == 0 &&
				(oldest == nil || candidate.lastUsed < oldest.lastUsed) {
				oldestKey = candidateKey
				oldest = candidate
			}
		}
		if oldest == nil {
			r.mu.Unlock()
			if released > 0 {
				r.budget.Release(released)
			}
			return nil, false, false
		}
		delete(r.baselineCache, oldestKey)
		r.baselineCacheBytes -= oldest.bytes
		released += oldest.bytes
	}
	r.baselineCacheUse++
	entry := &baselineCacheEntry{
		boundaries: boundaries,
		bytes:      bytes,
		lastUsed:   r.baselineCacheUse,
		refs:       1,
	}
	r.baselineCache[key] = entry
	r.baselineCacheBytes += bytes
	r.mu.Unlock()
	if released > 0 {
		r.budget.Release(released)
	}
	return entry, true, true
}

func (r *MessageCursorResolver) cached(hashSlot uint16) (messageCursorCacheEntry, bool) {
	r.mu.Lock()
	entry, ok := r.cache[hashSlot]
	if ok {
		r.cacheUse++
		entry.lastUsed = r.cacheUse
		r.cache[hashSlot] = entry
	}
	r.mu.Unlock()
	return entry, ok
}

func (r *MessageCursorResolver) storeCached(request MessageCursorResolveRequest, highWatermark uint64, boundaries []backupartifact.ChannelBoundary) {
	entryBytes := int64(messageCursorCacheEntryBytes +
		len(request.Generation) + len(request.SourceCursor) +
		len(request.Head.SegmentID) + len(request.Head.CommitKey) + len(request.Head.CommitSHA256))
	for _, boundary := range boundaries {
		if entryBytes > maxMessageCursorCacheBytes-int64(len(boundary.ChannelID))-messageCursorCacheBoundaryBytes {
			return
		}
		entryBytes += int64(len(boundary.ChannelID)) + messageCursorCacheBoundaryBytes
	}
	if entryBytes > maxMessageCursorCacheBytes {
		return
	}
	entry := messageCursorCacheEntry{
		reference: request.Head, generation: request.Generation,
		sequence: request.Sequence, sourceCursor: request.SourceCursor,
		highWatermark: highWatermark,
		boundaries:    append([]backupartifact.ChannelBoundary(nil), boundaries...),
		bytes:         entryBytes,
	}
	r.mu.Lock()
	current, ok := r.cache[request.HashSlot]
	if ok && current.generation == entry.generation && current.sequence > entry.sequence {
		r.mu.Unlock()
		return
	}
	if ok {
		r.cacheBytes -= current.bytes
	}
	r.cacheUse++
	entry.lastUsed = r.cacheUse
	r.cache[request.HashSlot] = entry
	r.cacheBytes += entry.bytes
	for len(r.cache) > maxMessageCursorCacheEntries || r.cacheBytes > maxMessageCursorCacheBytes {
		var oldestHashSlot uint16
		var oldestUse uint64 = ^uint64(0)
		for hashSlot, candidate := range r.cache {
			if candidate.lastUsed < oldestUse {
				oldestHashSlot = hashSlot
				oldestUse = candidate.lastUsed
			}
		}
		oldest := r.cache[oldestHashSlot]
		delete(r.cache, oldestHashSlot)
		r.cacheBytes -= oldest.bytes
	}
	r.mu.Unlock()
}
