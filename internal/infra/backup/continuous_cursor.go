package backup

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	maxMessageCursorChainSegments   = 1024
	maxMessageCursorChainChannels   = 1 << 20
	maxMessageCursorGenerationSize  = 128
	maxMessageCursorCacheEntries    = 256
	maxMessageCursorCacheBytes      = 64 << 20
	messageCursorCacheEntryBytes    = 512
	messageCursorCacheBoundaryBytes = 64
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
	mu     sync.Mutex
	cache  map[uint16]messageCursorCacheEntry
	// cacheBytes and cacheUse bound and order the rebuild acceleration cache.
	cacheBytes int64
	cacheUse   uint64
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

// NewMessageCursorResolver creates a repository-backed cursor resolver.
func NewMessageCursorResolver(loader ContinuousSegmentLoader) (*MessageCursorResolver, error) {
	if loader == nil {
		return nil, fmt.Errorf("backup cursor resolver: segment loader is required")
	}
	return &MessageCursorResolver{loader: loader, cache: make(map[uint16]messageCursorCacheEntry)}, nil
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
		return append([]backupartifact.ChannelBoundary(nil), cached.boundaries...), nil
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
