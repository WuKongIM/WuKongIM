package deliverytag

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// LookupReason explains why a local tag lookup can or cannot be reused.
type LookupReason string

const (
	LookupHit              LookupReason = "hit"
	LookupMissingRef       LookupReason = "missing_ref"
	LookupMissingTag       LookupReason = "missing_tag"
	LookupTagKeyMismatch   LookupReason = "tag_key_mismatch"
	LookupStaleRequest     LookupReason = "stale_request"
	LookupNewerRequest     LookupReason = "newer_request"
	LookupTopologyMismatch LookupReason = "topology_mismatch"
)

// Options configures a node-local delivery tag manager.
type Options struct {
	LocalNodeID uint64
	TTL         time.Duration
	Now         func() time.Time
	NewTagKey   func() string
}

// Manager owns node-local delivery tag bodies and channel refs.
type Manager struct {
	mu          sync.RWMutex
	localNodeID uint64
	ttl         time.Duration
	now         func() time.Time
	newTagKey   func() string
	cache       tagCache
}

// NewManager creates an empty node-local delivery tag manager.
func NewManager(options Options) *Manager {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	newTagKey := options.NewTagKey
	if newTagKey == nil {
		newTagKey = randomTagKey
	}
	return &Manager{
		localNodeID: options.LocalNodeID,
		ttl:         options.TTL,
		now:         now,
		newTagKey:   newTagKey,
		cache:       newTagCache(),
	}
}

// BuildLeaderTag stores an authoritative full-channel tag on the leader node.
func (m *Manager) BuildLeaderTag(request BuildRequest) (DeliveryTag, bool) {
	if m == nil {
		return DeliveryTag{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	ref, hasRef := m.cache.channelRef[request.ChannelKey]
	key := ""
	version := uint64(1)
	if hasRef && !request.MintFreshKey {
		key = ref.TagKey
		version = ref.TagVersion
		if requestRequiresNewMaterialization(ref, request) {
			version++
		}
	}
	if key == "" {
		key = m.newTagKey()
	}

	tag := DeliveryTag{
		Key:                             key,
		ChannelKey:                      request.ChannelKey,
		TagVersion:                      version,
		SubscriberMutationVersion:       request.SubscriberMutationVersion,
		SourceChannelKey:                request.SourceChannelKey,
		SourceSubscriberMutationVersion: request.SourceSubscriberMutationVersion,
		Topology:                        request.Topology.Clone(),
		Partitions:                      clonePartitions(request.Partitions),
		CreatedAt:                       now,
		LastAccess:                      now,
	}
	m.cache.put(tag)
	return tag.clone(), true
}

// BuildEphemeralTag stores a one-message tag body without replacing the reusable channel ref.
func (m *Manager) BuildEphemeralTag(request BuildRequest) (DeliveryTag, bool) {
	if m == nil {
		return DeliveryTag{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	tag := DeliveryTag{
		Key:                             m.newTagKey(),
		ChannelKey:                      request.ChannelKey,
		TagVersion:                      1,
		SubscriberMutationVersion:       request.SubscriberMutationVersion,
		SourceChannelKey:                request.SourceChannelKey,
		SourceSubscriberMutationVersion: request.SourceSubscriberMutationVersion,
		Topology:                        request.Topology.Clone(),
		Partitions:                      clonePartitions(request.Partitions),
		CreatedAt:                       now,
		LastAccess:                      now,
	}
	m.cache.tags[tag.Key] = tag.clone()
	return tag.clone(), true
}

// StoreFollowerPartition stores only this node's subscriber partition from a leader tag.
func (m *Manager) StoreFollowerPartition(tag DeliveryTag) (DeliveryTag, bool) {
	if m == nil {
		return DeliveryTag{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if current, ok := m.cache.channelRef[tag.ChannelKey]; ok {
		if staleIncomingTag(current, tag) {
			currentTag, hasTag := m.cache.tags[current.TagKey]
			if !hasTag {
				return DeliveryTag{}, false
			}
			return currentTag.clone(), false
		}
	}

	now := m.now()
	local := tag.clone()
	local.Partitions = []NodePartition{tag.PartitionForNode(m.localNodeID)}
	local.LastAccess = now
	if local.CreatedAt.IsZero() {
		local.CreatedAt = now
	}
	m.cache.put(local)
	return local.clone(), true
}

// LookupLocalPartitionRef checks the local partition fence without cloning the cached UID slices.
// The returned DeliveryTag shares storage with the cache and must be treated as read-only.
func (m *Manager) LookupLocalPartitionRef(request TagRef) (DeliveryTag, bool, LookupReason) {
	if m == nil {
		return DeliveryTag{}, false, LookupMissingRef
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.lookupLocalPartitionLocked(request)
}

// LookupLocalPartition checks whether a requested tag fence can reuse the local partition.
func (m *Manager) LookupLocalPartition(request TagRef) (DeliveryTag, bool, LookupReason) {
	if m == nil {
		return DeliveryTag{}, false, LookupMissingRef
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	tag, hit, reason := m.lookupLocalPartitionLocked(request)
	if hit {
		return tag.clone(), true, reason
	}
	return tag.clone(), false, reason
}

func (m *Manager) lookupLocalPartitionLocked(request TagRef) (DeliveryTag, bool, LookupReason) {
	current, ok := m.cache.channelRef[request.ChannelKey]
	if !ok {
		return DeliveryTag{}, false, LookupMissingRef
	}
	tag, ok := m.cache.tags[current.TagKey]
	if !ok {
		return DeliveryTag{}, false, LookupMissingTag
	}
	if current.TagKey != request.TagKey {
		return tag, false, LookupTagKeyMismatch
	}
	if request.TagVersion < current.TagVersion {
		return tag, false, LookupStaleRequest
	}
	if request.TagVersion > current.TagVersion {
		return tag, false, LookupNewerRequest
	}
	if request.SubscriberMutationVersion != 0 {
		if request.SubscriberMutationVersion < current.SubscriberMutationVersion {
			return tag, false, LookupStaleRequest
		}
		if request.SubscriberMutationVersion > current.SubscriberMutationVersion {
			return tag, false, LookupNewerRequest
		}
	}
	if request.SourceChannelKey != "" && request.SourceChannelKey != current.SourceChannelKey {
		return tag, false, LookupStaleRequest
	}
	if request.SourceSubscriberMutationVersion != 0 {
		if request.SourceSubscriberMutationVersion < current.SourceSubscriberMutationVersion {
			return tag, false, LookupStaleRequest
		}
		if request.SourceSubscriberMutationVersion > current.SourceSubscriberMutationVersion {
			return tag, false, LookupNewerRequest
		}
	}
	if !current.Topology.Equal(request.Topology) {
		return tag, false, LookupTopologyMismatch
	}
	tag.LastAccess = m.now()
	m.cache.tags[current.TagKey] = tag
	return tag, true, LookupHit
}

// LookupTag returns a cached tag body by tag key.
func (m *Manager) LookupTag(tagKey string) (DeliveryTag, bool) {
	if m == nil {
		return DeliveryTag{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tag, ok := m.cache.tags[tagKey]
	if !ok {
		return DeliveryTag{}, false
	}
	tag.LastAccess = m.now()
	m.cache.tags[tagKey] = tag
	return tag.clone(), true
}

// CurrentRef returns the current channel-to-tag reference.
func (m *Manager) CurrentRef(channelKey string) (TagRef, bool) {
	if m == nil {
		return TagRef{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cache.getRef(channelKey)
}

// IsSourceStale reports whether a derived tag lags its source channel version.
func (m *Manager) IsSourceStale(channelKey string, source SourceVersion) bool {
	if m == nil {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ref, ok := m.cache.channelRef[channelKey]
	if !ok || ref.SourceChannelKey == "" {
		return false
	}
	return ref.SourceChannelKey == source.ChannelKey && ref.SourceSubscriberMutationVersion < source.SubscriberMutationVersion
}

// CleanupExpired removes cold tags and any channel refs that point at them.
func (m *Manager) CleanupExpired() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cache.cleanupExpired(m.now(), m.ttl)
}

func requestRequiresNewMaterialization(ref TagRef, request BuildRequest) bool {
	return request.SubscriberMutationVersion != ref.SubscriberMutationVersion ||
		request.SourceChannelKey != ref.SourceChannelKey ||
		request.SourceSubscriberMutationVersion != ref.SourceSubscriberMutationVersion ||
		!request.Topology.Equal(ref.Topology)
}

func staleIncomingTag(current TagRef, tag DeliveryTag) bool {
	if tag.SubscriberMutationVersion < current.SubscriberMutationVersion {
		return true
	}
	if tag.SourceChannelKey == current.SourceChannelKey && tag.SourceSubscriberMutationVersion < current.SourceSubscriberMutationVersion {
		return true
	}
	return current.TagKey == tag.Key && tag.TagVersion < current.TagVersion
}

func randomTagKey() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf[:])
}
