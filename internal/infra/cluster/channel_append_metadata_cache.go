package cluster

import (
	"container/list"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	personAppendMetadataChannelType = 1
	// Preserve the active person working set while evicting one-shot channels.
	channelAppendMetadataEntries = 32 * 1024
	channelAppendMetadataIDBytes = 4 << 20
)

// ChannelAppendMetadata stores low-churn recipient fanout metadata for one channel.
type ChannelAppendMetadata struct {
	// Large reports whether the channel should use paged subscriber fanout.
	Large bool
	// SubscriberMutationVersion identifies the subscriber-list version used for recipient cache invalidation.
	SubscriberMutationVersion uint64
	// DirectoryProjectionState records whether canonical person-channel
	// membership projection is absent, pending, or complete.
	DirectoryProjectionState metadb.DirectoryProjectionState
}

// ChannelAppendMetadataCache retains only person recipient hints. Mutable
// group membership is read from its authority on each routed batch.
type ChannelAppendMetadataCache struct {
	mu         sync.RWMutex
	generation uint64
	entries    map[channelappend.ChannelID]*list.Element
	recency    list.List
	idBytes    int
}

type channelAppendMetadataEntry struct {
	id       channelappend.ChannelID
	metadata ChannelAppendMetadata
}

// NewChannelAppendMetadataCache creates an empty channel append metadata cache.
func NewChannelAppendMetadataCache() *ChannelAppendMetadataCache {
	return &ChannelAppendMetadataCache{}
}

// Lookup returns cached recipient fanout metadata for a channel.
func (c *ChannelAppendMetadataCache) Lookup(id channelappend.ChannelID) (ChannelAppendMetadata, bool) {
	if c == nil {
		return ChannelAppendMetadata{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[id]
	if element == nil {
		return ChannelAppendMetadata{}, false
	}
	c.recency.MoveToFront(element)
	return element.Value.(*channelAppendMetadataEntry).metadata, true
}

// Store records a person hint if its identity fits the cache.
func (c *ChannelAppendMetadataCache) Store(id channelappend.ChannelID, metadata ChannelAppendMetadata) {
	if c == nil || !cacheableChannelAppendMetadata(id) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storeLocked(id, metadata)
}

// Generation returns the current logical cache generation. A caller that is
// about to perform an authoritative load can use it to reject a late result
// after restore switches the underlying database generation.
func (c *ChannelAppendMetadataCache) Generation() uint64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// StoreIfGeneration records a person hint when no restore reset occurred
// after the caller started its authoritative load and the identity fits.
func (c *ChannelAppendMetadataCache) StoreIfGeneration(id channelappend.ChannelID, metadata ChannelAppendMetadata, generation uint64) bool {
	if c == nil || !cacheableChannelAppendMetadata(id) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return false
	}
	c.storeLocked(id, metadata)
	return true
}

func cacheableChannelAppendMetadata(id channelappend.ChannelID) bool {
	return id.Type == personAppendMetadataChannelType && len(id.ID) <= channelAppendMetadataIDBytes
}

func (c *ChannelAppendMetadataCache) storeLocked(id channelappend.ChannelID, metadata ChannelAppendMetadata) {
	if element := c.entries[id]; element != nil {
		element.Value.(*channelAppendMetadataEntry).metadata = metadata
		c.recency.MoveToFront(element)
		return
	}
	if c.entries == nil {
		c.entries = make(map[channelappend.ChannelID]*list.Element)
	}
	id.ID = strings.Clone(id.ID)
	c.entries[id] = c.recency.PushFront(&channelAppendMetadataEntry{id: id, metadata: metadata})
	c.idBytes += len(id.ID)
	for len(c.entries) > channelAppendMetadataEntries || c.idBytes > channelAppendMetadataIDBytes {
		c.removeLocked(c.recency.Back())
	}
}

func (c *ChannelAppendMetadataCache) removeLocked(element *list.Element) {
	id := element.Value.(*channelAppendMetadataEntry).id
	delete(c.entries, id)
	c.recency.Remove(element)
	c.idBytes -= len(id.ID)
}

// ResetAfterRestore starts a new logical cache generation and discards all
// metadata resolved from the previous restored database.
func (c *ChannelAppendMetadataCache) ResetAfterRestore() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries = nil
	c.recency.Init()
	c.idBytes = 0
}

// Delete removes cached recipient fanout metadata for a channel.
func (c *ChannelAppendMetadataCache) Delete(id channelappend.ChannelID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[id]; element != nil {
		c.removeLocked(element)
	}
}

func (c *ChannelAppendMetadataCache) storeChannel(channel metadb.Channel) {
	if channel.ChannelID == "" {
		return
	}
	c.Store(channelappend.ChannelID{ID: channel.ChannelID, Type: uint8(channel.ChannelType)}, ChannelAppendMetadata{
		Large:                     channel.Large != 0,
		SubscriberMutationVersion: channel.SubscriberMutationVersion,
		DirectoryProjectionState:  channel.DirectoryProjectionState,
	})
}
