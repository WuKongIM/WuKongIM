package cluster

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelAppendMetadata stores low-churn recipient fanout metadata for one channel.
type ChannelAppendMetadata struct {
	// Large reports whether the channel should use paged subscriber fanout.
	Large bool
	// SubscriberMutationVersion identifies the subscriber-list version used for recipient cache invalidation.
	SubscriberMutationVersion uint64
}

// ChannelAppendMetadataCache caches recipient fanout metadata outside the foreground metadata DB path.
type ChannelAppendMetadataCache struct {
	mu      sync.RWMutex
	entries map[channelappend.ChannelID]ChannelAppendMetadata
}

// NewChannelAppendMetadataCache creates an empty channel append metadata cache.
func NewChannelAppendMetadataCache() *ChannelAppendMetadataCache {
	return &ChannelAppendMetadataCache{entries: make(map[channelappend.ChannelID]ChannelAppendMetadata)}
}

// Lookup returns cached recipient fanout metadata for a channel.
func (c *ChannelAppendMetadataCache) Lookup(id channelappend.ChannelID) (ChannelAppendMetadata, bool) {
	if c == nil {
		return ChannelAppendMetadata{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	metadata, ok := c.entries[id]
	return metadata, ok
}

// Store records recipient fanout metadata for a channel.
func (c *ChannelAppendMetadataCache) Store(id channelappend.ChannelID, metadata ChannelAppendMetadata) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = metadata
}

// Delete removes cached recipient fanout metadata for a channel.
func (c *ChannelAppendMetadataCache) Delete(id channelappend.ChannelID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

func (c *ChannelAppendMetadataCache) storeChannel(channel metadb.Channel) {
	if channel.ChannelID == "" {
		return
	}
	c.Store(channelappend.ChannelID{ID: channel.ChannelID, Type: uint8(channel.ChannelType)}, ChannelAppendMetadata{
		Large:                     channel.Large != 0,
		SubscriberMutationVersion: channel.SubscriberMutationVersion,
	})
}
