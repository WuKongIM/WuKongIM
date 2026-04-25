package app

import (
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

const (
	channelMetaPositiveCacheTTL = 5 * time.Second
	channelMetaNegativeCacheTTL = time.Second
)

type cachedChannelMeta struct {
	meta      channel.Meta
	expiresAt time.Time
}

type cachedChannelMetaError struct {
	err       error
	expiresAt time.Time
}

type channelMetaActivationCall struct {
	done chan struct{}
	meta channel.Meta
	err  error
}

type channelActivationCache struct {
	mu       sync.Mutex
	positive map[channel.ChannelKey]cachedChannelMeta
	negative map[channel.ChannelKey]cachedChannelMetaError
	calls    map[channel.ChannelKey]*channelMetaActivationCall
}

func (c *channelActivationCache) loadPositive(key channel.ChannelKey, now time.Time) (channel.Meta, bool) {
	if c == nil {
		return channel.Meta{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.positive[key]
	if !ok {
		return channel.Meta{}, false
	}
	if now.After(entry.expiresAt) {
		delete(c.positive, key)
		return channel.Meta{}, false
	}
	return entry.meta, true
}

func (c *channelActivationCache) storePositive(key channel.ChannelKey, meta channel.Meta, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.positive == nil {
		c.positive = make(map[channel.ChannelKey]cachedChannelMeta)
	}
	if c.negative != nil {
		delete(c.negative, key)
	}
	c.positive[key] = cachedChannelMeta{meta: meta, expiresAt: now.Add(channelMetaPositiveCacheTTL)}
}

func (c *channelActivationCache) loadNegative(key channel.ChannelKey, now time.Time) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.negative[key]
	if !ok {
		return nil
	}
	if now.After(entry.expiresAt) {
		delete(c.negative, key)
		return nil
	}
	return entry.err
}

func (c *channelActivationCache) storeNegative(key channel.ChannelKey, err error, now time.Time) {
	if c == nil || err == nil {
		return
	}
	if !errors.Is(err, metadb.ErrNotFound) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.negative == nil {
		c.negative = make(map[channel.ChannelKey]cachedChannelMetaError)
	}
	if c.positive != nil {
		delete(c.positive, key)
	}
	c.negative[key] = cachedChannelMetaError{err: err, expiresAt: now.Add(channelMetaNegativeCacheTTL)}
}

func (c *channelActivationCache) runSingleflight(key channel.ChannelKey, fn func() (channel.Meta, error)) (channel.Meta, error) {
	if c == nil {
		return fn()
	}
	c.mu.Lock()
	if c.calls == nil {
		c.calls = make(map[channel.ChannelKey]*channelMetaActivationCall)
	}
	if call, ok := c.calls[key]; ok {
		c.mu.Unlock()
		<-call.done
		return call.meta, call.err
	}
	call := &channelMetaActivationCall{done: make(chan struct{})}
	c.calls[key] = call
	c.mu.Unlock()

	call.meta, call.err = fn()
	close(call.done)

	c.mu.Lock()
	delete(c.calls, key)
	c.mu.Unlock()
	return call.meta, call.err
}

func (c *channelActivationCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.positive = nil
	c.negative = nil
	c.mu.Unlock()
}
