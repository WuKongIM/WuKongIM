package deliverytag

import "time"

type tagCache struct {
	tags       map[string]DeliveryTag
	channelRef map[string]TagRef
}

func newTagCache() tagCache {
	return tagCache{
		tags:       make(map[string]DeliveryTag),
		channelRef: make(map[string]TagRef),
	}
}

func (c *tagCache) put(tag DeliveryTag) {
	c.tags[tag.Key] = tag.clone()
	c.channelRef[tag.ChannelKey] = refFromTag(tag)
}

func (c *tagCache) getTag(key string) (DeliveryTag, bool) {
	tag, ok := c.tags[key]
	if !ok {
		return DeliveryTag{}, false
	}
	return tag.clone(), true
}

func (c *tagCache) getRef(channelKey string) (TagRef, bool) {
	ref, ok := c.channelRef[channelKey]
	if !ok {
		return TagRef{}, false
	}
	return ref.clone(), true
}

func (c *tagCache) cleanupExpired(now time.Time, ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	removed := 0
	for key, tag := range c.tags {
		if now.Sub(tag.LastAccess) <= ttl {
			continue
		}
		delete(c.tags, key)
		removed++
	}
	for channelKey, ref := range c.channelRef {
		if _, ok := c.tags[ref.TagKey]; !ok {
			delete(c.channelRef, channelKey)
		}
	}
	return removed
}
